// assistant_config.go —— 助手性格与拓展配置（config/assistant.json）：读取 /
// 设置 / 变更广播。照 behavior.go 同一条「现读」模式：文件只有几十字节，resolver
// 每次组装 Agent 语境时现读——手改或 set 后**下一轮**立即生效，不需要 reloadLoop
// 替它做状态替换；reloadLoop 只负责「手改后给其它客户端广播一次变更」。
//
// resolver merge 在 assistant.go（assistantAgentResolver）；本文件只管配置的
// 装配、协议方法域（assistant.config.get/set）与事件广播。
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// AttachAssistantConfig 装配助手配置路径（config/assistant.json，与 models.json
// 同目录；未装配 = 各字段空——merge 全走内置值，与 remote.json 未装配时
// Enabled=false 同一条降级语义）。顺带把助手工具绑定的引用检查注入 RemoveTool
//（工具被助手绑定 = 拒删，先解除绑定——与 agents 表白名单引用同一道闸）。
func (s *Server) AttachAssistantConfig(path string) {
	s.assistantCfgMu.Lock()
	s.assistantCfgPath = path
	s.assistantCfgLast = config.LoadAssistant(path)
	s.assistantCfgMu.Unlock()
	if s.st != nil {
		s.st.SetExtraToolRefCheck(s.checkAssistantToolRef)
	}
}

// checkAssistantToolRef 是 RemoveTool 的库外引用检查：id 出现在 assistant.json
// 的 toolWhitelist（完整白名单，builtin/binary/custom/mcp 都可勾）或 tools
//（自定义命令工具绑定）里就拒删。mcpServers 不在此查——它是**服务器粒度**的
// 绑定，删一个工具条目不悬空服务器绑定（服务器撤下走 RemoveMcServer，绑定照旧
// 保留声明）。
func (s *Server) checkAssistantToolRef(toolID string) error {
	cfg := s.assistantConfig()
	for _, id := range cfg.ToolWhitelist {
		if id == toolID {
			return fmt.Errorf("工具 %s 被助手工具白名单引用（助手配置），先在助手配置里取消勾选再删除", toolID)
		}
	}
	for _, id := range cfg.Tools {
		if id == toolID {
			return fmt.Errorf("工具 %s 被助手绑定（助手配置），先在助手配置里解除绑定再删除", toolID)
		}
	}
	return nil
}

// assistantConfig 现读助手配置（每次调用读一次文件——文件极小，开销可忽略；
// 未装配 = 零值 = 全内置）。读失败由 config.LoadAssistant 回落零值，不报错。
func (s *Server) assistantConfig() config.Assistant {
	s.assistantCfgMu.Lock()
	path := s.assistantCfgPath
	s.assistantCfgMu.Unlock()
	if path == "" {
		return config.Assistant{}
	}
	return config.LoadAssistant(path)
}

// assistantConfigResult 生成 get 应答 / changed 广播的载荷（已归一）。
// builtinDefaults 恒带回——内置默认 14 项的事实源在服务端（UI 预填与恢复默认
// 用它，前端不写死，防漂移）。
func (s *Server) assistantConfigResult() protocol.AssistantConfigResult {
	a := s.assistantConfig()
	return protocol.AssistantConfigResult{
		Name: a.Name, Persona: a.Persona, Skills: a.Skills, Workflow: a.Workflow,
		MCPServers: a.MCPServers, Tools: a.Tools, ToolWhitelist: a.ToolWhitelist,
		BuiltinDefaults: append([]string(nil), protocol.BuiltinAssistantToolIDs...),
	}
}

// CheckAssistantConfigChanged 供热加载周期调用：手改 assistant.json 后把变更
// 广播给其它客户端（set 路径自己已广播并同步 assistantCfgLast，这里不会重复）。
func (s *Server) CheckAssistantConfigChanged() {
	s.assistantCfgMu.Lock()
	path := s.assistantCfgPath
	if path == "" {
		s.assistantCfgMu.Unlock()
		return
	}
	now := config.LoadAssistant(path)
	before := s.assistantCfgLast
	changed := !assistantCfgEqual(before, now)
	if changed {
		s.assistantCfgLast = now
	}
	s.assistantCfgMu.Unlock()
	if changed {
		s.broadcast(protocol.EventAssistantConfigChanged, s.assistantConfigResult())
	}
}

// assistantCfgEqual 判两份配置是否等价（Skills/MCPServers/Tools/ToolWhitelist
// 是切片不能 == 直接比；顺序即用户的声明顺序，顺序变了算变更）。
func assistantCfgEqual(a, b config.Assistant) bool {
	return a.Name == b.Name && a.Persona == b.Persona && a.Workflow == b.Workflow &&
		slices.Equal(a.Skills, b.Skills) && slices.Equal(a.MCPServers, b.MCPServers) &&
		slices.Equal(a.Tools, b.Tools) && slices.Equal(a.ToolWhitelist, b.ToolWhitelist)
}

// saveAssistantConfig 写回 assistant.json，同步热加载比对基线（否则下一次
// reloadLoop 巡检会把它当「手改」再广播一遍）。
func (s *Server) saveAssistantConfig(a config.Assistant) error {
	s.assistantCfgMu.Lock()
	path := s.assistantCfgPath
	s.assistantCfgMu.Unlock()
	if path == "" {
		return errors.New("助手配置未装配")
	}
	if err := config.SaveAssistant(path, a); err != nil {
		return err
	}
	s.assistantCfgMu.Lock()
	s.assistantCfgLast = config.LoadAssistant(path)
	s.assistantCfgMu.Unlock()
	return nil
}

// handleAssistantConfig 分发 assistant.config.get / assistant.config.set；
// 返回 nil 表示方法不在此域。
//
// set 的 Skills/Workflow 校验是**现读**：id 必须存在于 modules 表（与
// agent_write 的「工具名必须真实存在」同一条目录一致性纪律——无效 id 会
// 被 resolver 过滤掉静默失效，打错就是一份空提示词，没有任何报错）。
// 空串 = 清空绑定（合法——用户不想绑了），所以只对**非空值**查表。
func (s *Server) handleAssistantConfig(req *protocol.Request, params json.RawMessage) *protocol.Response {
	switch req.Method {
	case protocol.MethodAssistantConfigGet:
		return protocol.NewResult(req.ID, s.assistantConfigResult())

	case protocol.MethodAssistantConfigSet:
		var p protocol.AssistantConfigSetParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		// 全量替换语义：set 带的就是新配置（省略字段 = 清空——前端一次发全量，
		// 工具 update_assistant_config 走的是自己的部分更新路径，不经这里）。
		next := config.Assistant{
			Name: p.Name, Persona: p.Persona, Skills: p.Skills, Workflow: p.Workflow,
			MCPServers: p.MCPServers, Tools: p.Tools, ToolWhitelist: p.ToolWhitelist,
		}.Normalized()
		if next.Workflow != "" || len(next.Skills) > 0 {
			if err := s.validateAssistantModuleIDs(next.Workflow, next.Skills); err != nil {
				return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
			}
		}
		if len(next.MCPServers) > 0 {
			if err := s.validateMCPServerIDs(next.MCPServers); err != nil {
				return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
			}
		}
		if len(next.Tools) > 0 {
			if err := s.validateAssistantToolIDs(next.Tools); err != nil {
				return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
			}
		}
		if len(next.ToolWhitelist) > 0 {
			if err := s.validateAssistantWhitelistIDs(next.ToolWhitelist); err != nil {
				return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
			}
		}
		if err := s.saveAssistantConfig(next); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, "保存助手配置失败: "+err.Error())
		}
		s.broadcast(protocol.EventAssistantConfigChanged, s.assistantConfigResult())
		return protocol.NewResult(req.ID, s.assistantConfigResult())
	}
	return nil
}

// bindAssistantMCPServer 把一个 MCP 服务器 id 追加进 assistant.json 的
// mcpServers（去重：已在绑定里就原样返回）。这是 mcp_manage 工具 add 联动的
// 专用口——update_assistant_config 的 patch 刻意不含 mcpServers，绑定变更
// 只经这里一个写路径（与工具装配注释同一条纪律）。
// 写盘后同步热加载比对基线（saveAssistantConfig 内做）+ 广播 configChanged。
func (s *Server) bindAssistantMCPServer(id string) error {
	cur := s.assistantConfig()
	for _, x := range cur.MCPServers {
		if x == id {
			return nil
		}
	}
	next := cur
	next.MCPServers = append(next.MCPServers, id)
	if err := s.saveAssistantConfig(next.Normalized()); err != nil {
		return err
	}
	s.broadcast(protocol.EventAssistantConfigChanged, s.assistantConfigResult())
	return nil
}

// validateAssistantModuleIDs 现读 modules 表校验模板与技能 id（一次 ListModules
// 同时查两样）；无效 id 报人话错误（含无效的 id 清单）。
func (s *Server) validateAssistantModuleIDs(workflow string, skills []string) error {
	if s.st == nil {
		return errors.New("会话存储未启用，无法校验模块 id")
	}
	mods, err := s.st.ListModules()
	if err != nil {
		return errors.New("读模块目录失败: " + err.Error())
	}
	byID := map[string]bool{}
	for i := range mods {
		byID[mods[i].ID] = true
	}
	var bad []string
	if workflow != "" && !byID[workflow] {
		bad = append(bad, "workflow: "+workflow)
	}
	for _, id := range skills {
		if !byID[id] {
			bad = append(bad, "skill: "+id)
		}
	}
	if len(bad) > 0 {
		return errors.New("模块 id 不存在: " + joinComma(bad) + "（在「拓展」页里查看现有 id）")
	}
	return nil
}

// validateMCPServerIDs 现读 mcp_servers 表校验 MCP 服务器 id（与
// validateAssistantModuleIDs 同一条目录一致性纪律——无效 id 会被 resolver 的
// merge 静默过滤掉，打错就是一份空绑定，没有任何报错）。
func (s *Server) validateMCPServerIDs(servers []string) error {
	if s.st == nil {
		return errors.New("会话存储未启用，无法校验 MCP 服务器 id")
	}
	list, err := s.st.ListMcServers()
	if err != nil {
		return errors.New("读 MCP 服务器目录失败: " + err.Error())
	}
	byID := map[string]bool{}
	for i := range list {
		byID[list[i].ID] = true
	}
	var bad []string
	for _, id := range servers {
		if !byID[id] {
			bad = append(bad, id)
		}
	}
	if len(bad) > 0 {
		return errors.New("MCP 服务器 id 不存在: " + joinComma(bad) + "（在「拓展 → MCP」里查看现有 id）")
	}
	return nil
}

// validateAssistantToolIDs 现读 tools 表校验助手绑定的自定义工具 id（与
// validateAssistantModuleIDs 同一条目录一致性纪律）。**只放行 custom=true &&
// source=="binary" 的条目**——内置工具（bash/edit 等写类）也在 tools 表里
//（seedTools 含它们），仅按「存在性」查会把内置工具放进来绕过助手的
// 「改动必须经 run_task 派进项目」边界；其余 source（builtin/mcp）同理，
// mcp 走服务器粒度的 mcpServers、builtin 是注册表实现。无效/不可绑定的 id
// 报人话错误并点名条目。
func (s *Server) validateAssistantToolIDs(tools []string) error {
	if s.st == nil {
		return errors.New("会话存储未启用，无法校验工具 id")
	}
	list, err := s.st.ListTools()
	if err != nil {
		return errors.New("读工具目录失败: " + err.Error())
	}
	byID := map[string]bool{}
	for i := range list {
		if list[i].Custom && list[i].Source == "binary" {
			byID[list[i].ID] = true
		}
	}
	var bad []string
	for _, id := range tools {
		if !byID[id] {
			bad = append(bad, id)
		}
	}
	if len(bad) > 0 {
		return errors.New("工具 id 不存在或不是自定义命令工具（只允许 custom binary 条目）: " +
			joinComma(bad) + "（在「拓展 → 工具」里查看现有 id）")
	}
	return nil
}

// validateAssistantWhitelistIDs 现读 tools 表校验工具白名单（2026-12：助手默认
// 工具完全可配）。**只查 id 存在性**——builtin/binary/custom/mcp 四类都放行
//（白名单语义 = 完整覆盖，内置 bash/edit 勾入是用户担责的显式行为，不再有
// 「只许 custom binary」的边界）；mcp 工具也可直接勾（但 mcpServers 的服务器
// 粒度绑定仍是动态合入的正交通道）。不存在于目录的 id 报人话错误并点名。
func (s *Server) validateAssistantWhitelistIDs(ids []string) error {
	if s.st == nil {
		return errors.New("会话存储未启用，无法校验工具 id")
	}
	list, err := s.st.ListTools()
	if err != nil {
		return errors.New("读工具目录失败: " + err.Error())
	}
	byID := map[string]bool{}
	for i := range list {
		byID[list[i].ID] = true
	}
	var bad []string
	for _, id := range ids {
		if !byID[id] {
			bad = append(bad, id)
		}
	}
	if len(bad) > 0 {
		return errors.New("工具 id 不存在: " + joinComma(bad) + "（在「拓展 → 工具」里查看现有 id）")
	}
	return nil
}

func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += "、"
		}
		out += x
	}
	return out
}
