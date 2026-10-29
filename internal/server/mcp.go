package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/moyunteng/lxcode/internal/mcp"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// MCP 服务器的装配与对账（M4 后半段）。
//
// 一条 MCP 服务器进来要落到三处，缺一处就是半截功能：
//
//  1. **连接**（mcp.Manager）：起子进程 / 连端点，握手 + 列举工具；
//  2. **目录**（tools 表）：列举到的每个工具物化成一条 source=mcp 的条目
//     —— 这是「MCP 工具不手动创建」的实现：服务器是事实源，目录是它的投影；
//  3. **注册表**（tools.Registry 动态段）：物化出来的条目经 syncDynamicTools
//     注册，模型才看得见。
//
// 停用服务器 = 能力挂起：断开连接 + 目录条目一并撤掉（留着条目会让 Agent
// 白名单指向一个不存在的工具）。重新启用时对账会重新物化。
//
// 对账入口只有 syncMCPServers()（幂等）：启动时一次 + 每次 catalog.mcp.* 变更后
// 一次。增量维护三处状态必然漂移，整份对账天然收敛（与 SetDynamic 同款理由）。

// mcpSyncTimeout 是对账的总超时：连不上的服务器不能把整个 catalog 请求拖住
// （对账发生在协议请求路径上——用户点「保存」后要立刻拿到响应）。
const mcpSyncTimeout = 30 * time.Second

// errMCPNotAttached 是未装配 manager 时的错误（单测直接调 dispatch 的形态）。
var errMCPNotAttached = errors.New("MCP 客户端未装配")

// mcpCall 是注入给工具注册表的调用口（tools.MCPCallFn）。
func (s *Server) mcpCall(ctx context.Context, name string, args json.RawMessage) (string, error) {
	mgr := s.mcpManager()
	if mgr == nil {
		return "", errMCPNotAttached
	}
	res, err := mgr.Call(ctx, name, args)
	if err != nil {
		return "", err
	}
	text := res.Text()
	// isError 是**工具执行失败**（不是协议错误）：按本仓约定以文本回填模型，
	// 让它看见失败原因并自己决定下一步（与工具注册表的三层错误一致）。
	if res.IsError {
		if text == "" {
			text = "（MCP 工具报告失败，但没有给出说明）"
		}
		return "MCP 工具执行失败: " + text, nil
	}
	return text, nil
}

// mcpManager 返回 MCP 管理器（未装配时 nil）。
func (s *Server) mcpManager() *mcp.Manager {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	return s.mcpMgr
}

// syncMCPServers 对账 MCP 面：连接 → 物化目录 → 同步注册表。
//
// 幂等，可在协议请求路径上调用（内部有总超时）。
func (s *Server) syncMCPServers() {
	if s.st == nil {
		return
	}
	mgr := s.mcpManager()
	if mgr == nil {
		return
	}
	specs, err := s.st.ListMcServers()
	if err != nil {
		log.Printf("MCP 对账失败（连接保持原样）: %v", err)
		return
	}
	cfgs := make([]mcp.ServerConfig, 0, len(specs))
	for _, sp := range specs {
		// 停用的服务器不连：能力挂起（工具与目录条目一并撤掉）。
		if !sp.Enabled {
			continue
		}
		cfgs = append(cfgs, mcp.ServerConfig{
			ID:        sp.ID,
			Transport: sp.Transport,
			Command:   sp.Command,
			Args:      sp.Args,
			Env:       sp.Env,
			URL:       sp.URL,
		})
	}
	ctx, cancel := context.WithTimeout(s.Ctx(), mcpSyncTimeout)
	defer cancel()
	// Sync 返回的 error map 按 id 索引（一个连不上不影响其余）。
	for id, err := range mgr.Sync(ctx, cfgs) {
		log.Printf("MCP 服务器 %s 不可用: %v", id, err)
	}
	s.materializeMCPTools(mgr)
	// 目录刚变过，注册表必须跟着变——否则物化出来的工具模型看不见。
	s.syncDynamicTools()
}

// materializeMCPTools 把 manager 已列举到的工具投影进工具目录。
//
// 投影而不是合并：source=mcp 的条目**整份由这里重建**（先算期望集合，再删
// 多出来的、补缺失的、更新变了描述的）——目录是连接的投影，连接变了目录就
// 必须跟着变，人工维护一份增量必然漂移。
//
// 物化出来的条目 custom=false：它们不是用户建的，用户也不该逐个删
// （要撤就走「停用/删除服务器」这条正道）。
func (s *Server) materializeMCPTools(mgr *mcp.Manager) {
	existing, err := s.st.ListTools()
	if err != nil {
		log.Printf("MCP 工具物化失败（目录保持原样）: %v", err)
		return
	}
	want := map[string]sessiondata.ToolSpec{}
	for _, entry := range mgr.Tools() {
		want[entry.Name] = mcpToolSpec(entry)
	}
	have := map[string]sessiondata.ToolSpec{}
	for _, t := range existing {
		if t.Source == "mcp" {
			have[t.ID] = t
		}
	}
	// 删：目录里有、连接没了（服务器停用/删除/工具被撤下）
	for id := range have {
		if _, keep := want[id]; keep {
			continue
		}
		if err := s.st.RemoveTool(id); err != nil {
			// 被 Agent 白名单引用时会拒删——这是对的（不能悄悄让白名单
			// 指向不存在的工具），记日志即可：重新启用服务器就一致了。
			log.Printf("MCP 工具 %s 撤下失败（可能仍被 Agent 引用）: %v", id, err)
		}
	}
	// 补/更：描述或参数变了就更新（inputSchema 可能随服务器版本变）
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Strings(ids) // 稳定顺序：日志与写入顺序不随 map 迭代漂移
	for _, id := range ids {
		spec := want[id]
		old, ok := have[id]
		if !ok {
			if err := s.st.AddTool(spec); err != nil {
				log.Printf("MCP 工具 %s 物化失败: %v", id, err)
			}
			continue
		}
		if old.Desc == spec.Desc && old.Server == spec.Server && sameParams(old.Params, spec.Params) {
			continue
		}
		if err := s.st.UpdateTool(spec); err != nil {
			log.Printf("MCP 工具 %s 更新失败: %v", id, err)
		}
	}
}

// mcpToolSpec 把一条 MCP 工具条目转成目录条目。
func mcpToolSpec(entry mcp.ToolEntry) sessiondata.ToolSpec {
	desc := strings.TrimSpace(entry.Tool.Description)
	if desc == "" {
		desc = "MCP 工具 " + entry.MCPName
	}
	return sessiondata.ToolSpec{
		ID:     entry.Name,
		Desc:   desc,
		Risk:   "high", // 注解不可信，一律高危（见 tools/mcp.go 文件头）
		Source: "mcp",
		Server: entry.ServerID,
		Params: mcpToolParams(entry),
		Doc:    mcpToolDoc(entry),
	}
}

// mcpToolDoc 生成扩展文档（服务器/原名/参数 schema 都留痕，便于用户对账）。
func mcpToolDoc(entry mcp.ToolEntry) string {
	var b strings.Builder
	b.WriteString("来自 MCP 服务器的工具。\n\n")
	b.WriteString("- 服务器: " + entry.ServerID + "\n")
	b.WriteString("- 原名: " + entry.MCPName + "\n")
	b.WriteString("- 暴露名: " + entry.Name + "（非 [A-Za-z0-9_-] 字符已替换为 _——工具 id 的字符集是网关硬约束）\n")
	b.WriteString("\n所有 MCP 工具一律按高危处理（服务器自报的只读注解不可信）；要免确认请显式切到 auto 审批档。\n")
	if schema := strings.TrimSpace(string(entry.Tool.InputSchema)); schema != "" {
		b.WriteString("\n参数 schema:\n\n```json\n" + schema + "\n```\n")
	}
	return b.String()
}

// mcpToolParams 从 inputSchema 推出参数表（目录详情层展示用）。
//
// 只认 properties 里的标量类型——schema 是服务器给的任意 JSON Schema，
// 这里只做**展示**用途，推不出来就空着（不影响执行：真正传给模型的是原始
// schema，见 tools/mcp.go 的 mcpSchema）。
func mcpToolParams(entry mcp.ToolEntry) []sessiondata.ToolParam {
	if len(entry.Tool.InputSchema) == 0 {
		return nil
	}
	var schema struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(entry.Tool.InputSchema, &schema); err != nil {
		return nil
	}
	req := map[string]bool{}
	for _, r := range schema.Required {
		req[r] = true
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names) // 稳定顺序：目录详情的参数表不该随 map 迭代乱跳
	out := make([]sessiondata.ToolParam, 0, len(names))
	for _, name := range names {
		p := schema.Properties[name]
		typ := p.Type
		if typ == "" {
			typ = "string"
		}
		out = append(out, sessiondata.ToolParam{
			Name:     name,
			Type:     typ,
			Required: req[name],
			Desc:     p.Description,
		})
	}
	return out
}

// sameParams 比较两份参数表（物化时的「要不要更新」判定）。
func sameParams(a, b []sessiondata.ToolParam) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mcpServerViews 生成给前端的服务器条目（含连接状态）。
//
// 状态是**运行期**信息（连接/工具数/最近错误），而 McServerSpec 是磁盘形状——
// 所以在这里合成协议载荷，不往磁盘类型上塞运行期字段。
func (s *Server) mcpServerViews(list []sessiondata.McServerSpec) []protocol.McServerEntry {
	out := make([]protocol.McServerEntry, 0, len(list))
	for _, sp := range list {
		out = append(out, toProtocolMcServer(sp))
	}
	statuses := map[string]mcp.Status{}
	if mgr := s.mcpManager(); mgr != nil {
		statuses = mgr.Statuses()
	}
	for i := range out {
		if !out[i].Enabled {
			// 停用 = 能力挂起：状态就说「已停止」，不必去问 manager。
			out[i].Status = mcp.StateStopped
			continue
		}
		if st, ok := statuses[out[i].ID]; ok {
			out[i].Status = st.State
			out[i].ToolCount = st.ToolCount
			out[i].LastError = st.LastError
			out[i].Stderr = st.Stderr
			continue
		}
		// 启用但不在 manager 里 = 还没对账到（或对账时被跳过）
		out[i].Status = mcp.StateStopped
	}
	return out
}

// mcpDefs 生成 MCP 工具的可执行定义（syncDynamicTools 的 MCP 分支数据源）。
//
// 数据源是 **manager 而不是工具目录**：manager 才知道「当前连着哪些工具、原名
// 是什么、schema 长什么样」——从目录条目反推原名是绕远路（目录里只有净化后的
// 名字）。目录那一步（materializeMCPTools）是给用户看与勾白名单用的投影，两者
// 同源（都来自 manager.Tools()）。
func (s *Server) mcpDefs() []*tools.Def {
	mgr := s.mcpManager()
	if mgr == nil {
		return nil
	}
	entries := mgr.Tools()
	defs := make([]*tools.Def, 0, len(entries))
	for _, entry := range entries {
		def, err := tools.MCPDef(tools.MCPToolSpec{
			Name:        entry.Name,
			MCPName:     entry.MCPName,
			ServerID:    entry.ServerID,
			Description: entry.Tool.Description,
			InputSchema: entry.Tool.InputSchema,
		}, s.mcpCall)
		if err != nil {
			log.Printf("MCP 工具 %s 不可用（已跳过）: %v", entry.Name, err)
			continue
		}
		defs = append(defs, def)
	}
	return defs
}
