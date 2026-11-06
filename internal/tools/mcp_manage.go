// mcp_manage：MCP 服务器的查询/安装/修改/删除（助手的 MCP 管理面）。
//
// 职责边界：MCP 服务器的编辑原本只有前端拓展页一条路（catalog.mcp.* RPC）；
// 用户要求助手有「装个 xxx 的 MCP」的能力且知道自己有这功能——本工具是它的
// 手脚。实现经注入（McpManageOps）：注册表只持回调，不 import store——与
// AgentCatalogOps 的 SetAgentCatalogOps 同款模式（tools 是叶子包，server 装配
// 时接线；共锁纪律见 registry.go 的 searchMu 注释）。
//
// 风险定级：单工具 RiskHigh + Mutates=true + Confirm 文案（写明「将执行
// command / 连接 url」的后果）。list/get 也走高危档是刻意取舍：一个 action
// 参数的工具拆成两个风险档会让确认门按 action 分叉（模型看到的工具声明只有
// 一份），且安装类动作必须过门——与 agent_write 同一条「单工具单档」逻辑。
// auto 审批档放行（与「完全访问」哲学一致）。
//
// 绑定纪律：add 的 bind 联动（把服务器 id 追加进 assistant.json 的 mcpServers）
// 在 server 装配闭包里做——配置路径归 server 装，工具层不知道路径；
// update_assistant_config 的 patch 维持不含 mcpServers（绑定变更只经本工具
// 一个口，防止两条写路径漂移）。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// McpManageToolName 是工具 id。必须匹配 `^[a-zA-Z0-9_-]{1,64}$`（与
// agent_dispatch 同一条网关约束）。
const McpManageToolName = "mcp_manage"

// McpManageOps 是工具的 store/server 侧实现（装配期注入一次，运行期只读）。
//
// 写操作返回给模型的结果文本（说清改了什么、绑定/生效情况）；校验拒绝时返回
// error（Execute 转自解释错误回填模型）。
type McpManageOps struct {
	// List 返回全部 MCP 服务器条目（磁盘形状）。
	List func() ([]sessiondata.McServerSpec, error)
	// Get 按 id 精确取一条（不存在时报错——工具层用它做 remove 前的存在性检查）。
	Get func(id string) (sessiondata.McServerSpec, error)
	// ToolCounts 返回「服务器 id → 已物化工具数」（list/get 输出用；nil = 不展示）。
	ToolCounts func() (map[string]int, error)
	// Add 落一条新服务器并处理 bind 联动（assistant.json 追加绑定 + 广播）。
	// bind 仅 add 有意义。
	Add func(m sessiondata.McServerSpec, bind bool) (string, error)
	// Update 全量替换一条服务器的配置（未提供的字段会被清空——工具层要求
	// 模型先 get 再传完整形状）。
	Update func(m sessiondata.McServerSpec) (string, error)
	// Remove 删除服务器（级联撤下目录条目）。
	Remove func(id string) (string, error)
	// SyncAndBroadcast 在任一写操作成功后由工具层调用：连接对账（syncMCPServers）
	// + 广播 catalog.changed {Kind:"mcp"}——前端零改动自动刷新。
	SyncAndBroadcast func(reason string)
}

// SetMcpManageOps 注入 MCP 管理实现（server 装配时调用）。与 agentCatalog /
// workspace 共用 searchMu：同一批「装配期写一次、运行期只读」的注入点。
func (r *Registry) SetMcpManageOps(ops McpManageOps) {
	r.searchMu.Lock()
	r.mcpManage = ops
	r.searchMu.Unlock()
}

func (r *Registry) getMcpManageOps() McpManageOps {
	r.searchMu.Lock()
	defer r.searchMu.Unlock()
	return r.mcpManage
}

// ---- 参数 ----

type mcpServerParams struct {
	ID        string            `json:"id"`
	Desc      string            `json:"desc"`
	Transport string            `json:"transport"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	URL       string            `json:"url"`
	Enabled   *bool             `json:"enabled"`
}

type mcpManageParams struct {
	Action string          `json:"action"`
	Server mcpServerParams `json:"server"`
	Bind   *bool           `json:"bind"`
}

// validTransport 是 transport 的合法值集合（与 store.AddMcServer 同一语义——
// 这里先拦一道给模型人话错误，store 层还有一道防其它写路径）。
var validTransport = map[string]bool{"stdio": true, "sse": true}

// validate 校验 add/update 的必填字段与 transport/command|url 配对
//（照 store.AddMcServer 的校验语义；store 层还有一道）。
func (p mcpServerParams) validate(action string) error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("%s 需要提供 server.id（服务器名）", action)
	}
	if !validTransport[p.Transport] {
		return fmt.Errorf("%s 需要提供 server.transport（stdio 或 sse）", action)
	}
	if p.Transport == "stdio" && strings.TrimSpace(p.Command) == "" {
		return fmt.Errorf("transport=stdio 需要提供 server.command（本机可执行命令）")
	}
	if p.Transport == "sse" && strings.TrimSpace(p.URL) == "" {
		return fmt.Errorf("transport=sse 需要提供 server.url（MCP 端点地址）")
	}
	return nil
}

// toSpec 转成磁盘形状（Enabled 缺省：add=true 新装的默认可用；update=false 由
// 调用方显式传——enable/disable 动作构造完整形状后走 update）。
func (p mcpServerParams) toSpec(defaultEnabled bool) sessiondata.McServerSpec {
	enabled := defaultEnabled
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	if p.Env == nil {
		p.Env = map[string]string{}
	}
	return sessiondata.McServerSpec{
		ID: strings.TrimSpace(p.ID), Desc: strings.TrimSpace(p.Desc),
		Transport: p.Transport, Command: strings.TrimSpace(p.Command),
		Args: p.Args, Env: p.Env, URL: strings.TrimSpace(p.URL),
		Enabled: enabled, Custom: true,
	}
}

// endpointText 是确认门与结果文本共用的「后果」描述（执行什么/连哪里）。
func (p mcpServerParams) endpointText() string {
	if p.Transport == "sse" {
		return "连接端点 " + p.URL
	}
	cmd := p.Command
	if len(p.Args) > 0 {
		cmd += " " + strings.Join(p.Args, " ")
	}
	return "在本机执行命令 `" + cmd + "`"
}

// ---- 工具定义 ----

func mcpManageDef(r *Registry) *Def {
	return &Def{
		Name: McpManageToolName,
		Description: "管理用户的 MCP 服务器：action=list 列出全部（id/传输/命令或端点/启用状态/工具数）；" +
			"action=get 带 server.id 读单条详情；action=add 安装新服务器（stdio 传 command+args，sse 传 url；" +
			"默认自动绑定到你的 MCP 列表，bind=false 跳过）；action=update 全量替换配置（先 get 现状再传完整 server）；" +
			"action=enable/disable 只切启用状态；action=remove 删除（目录条目级联撤下，绑定一并失效）。" +
			"用户说「装个 xxx 的 MCP / 看看我有哪些 MCP」时用它。安装会真实在本机启动命令/连接端点——" +
			"安装前向用户复述 command/url 并确认。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["list", "get", "add", "update", "enable", "disable", "remove"], "description": "要执行的操作"},
    "server": {
      "type": "object",
      "description": "服务器参数（get/remove 只需 id；add/update 需要完整形状；enable/disable 忽略）",
      "properties": {
        "id": {"type": "string", "description": "服务器名（唯一 id，如 exa）"},
        "desc": {"type": "string", "description": "一句话描述（可选）"},
        "transport": {"type": "string", "enum": ["stdio", "sse"], "description": "传输方式：stdio=本机子进程，sse=HTTP 端点"},
        "command": {"type": "string", "description": "stdio 的可执行命令（如 npx）"},
        "args": {"type": "array", "items": {"type": "string"}, "description": "stdio 的命令参数（可选）"},
        "env": {"type": "object", "additionalProperties": {"type": "string"}, "description": "环境变量（可选，键值对）"},
        "url": {"type": "string", "description": "sse 的端点 URL"},
        "enabled": {"type": "boolean", "description": "是否启用（缺省：add=启用，update=停用——显式传避免歧义）"}
      }
    },
    "bind": {"type": "boolean", "description": "仅 add 有意义，默认 true：装完自动加进你的 MCP 绑定列表（工具立即可用）"}
  },
  "required": ["action"]
}`),
		Risk:    RiskHigh,
		Mutates: true,
		Confirm: confirmMcpManage,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			ops := r.getMcpManageOps()
			if ops.List == nil {
				return "", fmt.Errorf("MCP 管理未装配（会话存储未启用）——无法管理 MCP 服务器")
			}
			var p mcpManageParams
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("参数解析失败: %v", err)
			}
			switch p.Action {
			case "list":
				return mcpManageList(ops)
			case "get":
				return mcpManageGet(ops, strings.TrimSpace(p.Server.ID))
			case "add":
				if err := p.Server.validate("add"); err != nil {
					return "", err
				}
				bind := true
				if p.Bind != nil {
					bind = *p.Bind
				}
				out, err := ops.Add(p.Server.toSpec(true), bind)
				if err != nil {
					return "", err
				}
				ops.syncAndBroadcast("update")
				return out, nil
			case "update":
				if err := p.Server.validate("update"); err != nil {
					return "", err
				}
				out, err := ops.Update(p.Server.toSpec(false))
				if err != nil {
					return "", err
				}
				ops.syncAndBroadcast("update")
				return out, nil
			case "enable", "disable":
				id := strings.TrimSpace(p.Server.ID)
				if id == "" {
					return "", fmt.Errorf("%s 需要提供 server.id", p.Action)
				}
				cur, err := ops.Get(id)
				if err != nil {
					return "", err
				}
				cur.Enabled = p.Action == "enable"
				out, err := ops.Update(cur)
				if err != nil {
					return "", err
				}
				ops.syncAndBroadcast("update")
				return out, nil
			case "remove":
				id := strings.TrimSpace(p.Server.ID)
				if id == "" {
					return "", fmt.Errorf("remove 需要提供 server.id")
				}
				if _, err := ops.Get(id); err != nil {
					list, lerr := ops.List()
					if lerr != nil {
						return "", err
					}
					return "", fmt.Errorf("MCP 服务器 %s 不存在（可用: %s）", id, mcpServerIDs(list))
				}
				out, err := ops.Remove(id)
				if err != nil {
					return "", err
				}
				ops.syncAndBroadcast("remove")
				return out, nil
			default:
				return "", fmt.Errorf("未知 action %q（可用: list/get/add/update/enable/disable/remove）", p.Action)
			}
		},
	}
}

// syncAndBroadcast 是 SyncAndBroadcast 的 nil 安全包装（未注入 = 跳过对账，
// 纯工具层测试不需要）。
func (ops McpManageOps) syncAndBroadcast(reason string) {
	if ops.SyncAndBroadcast != nil {
		ops.SyncAndBroadcast(reason)
	}
}

// mcpManageList 是列表视图：一行一台，字段只到「选服务器/报状态」需要的粒度
//（args/env 细节在单读里给——列表里带全量 env 会把上下文撑爆）。
func mcpManageList(ops McpManageOps) (string, error) {
	list, err := ops.List()
	if err != nil {
		return "", fmt.Errorf("读 MCP 服务器目录失败: %v", err)
	}
	counts, err := ops.toolCounts()
	if err != nil {
		counts = nil // 工具数拿不到不挡列表——主信息是服务器清单
	}
	if len(list) == 0 {
		return "目前没有配置任何 MCP 服务器。要安装新的，用 action=add（stdio 传 command，sse 传 url）。", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "共 %d 个 MCP 服务器：\n", len(list))
	for _, m := range list {
		b.WriteString("\n")
		writeServerSummary(&b, m, counts[m.ID])
	}
	b.WriteString("\n单台详情（含 args/env）用 action=get 带 id；安装用 action=add（高危，先向用户复述 command/url）。")
	return b.String(), nil
}

// mcpManageGet 是单读视图：完整配置 + 已物化工具数。
func mcpManageGet(ops McpManageOps, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("get 需要提供 server.id（要读哪台服务器）")
	}
	m, err := ops.Get(id)
	if err != nil {
		list, lerr := ops.List()
		if lerr != nil {
			return "", err
		}
		return "", fmt.Errorf("MCP 服务器 %s 不存在（可用: %s）", id, mcpServerIDs(list))
	}
	counts, _ := ops.toolCounts()
	var b strings.Builder
	fmt.Fprintf(&b, "# MCP 服务器 %s\n", m.ID)
	if m.Desc != "" {
		fmt.Fprintf(&b, "描述: %s\n", m.Desc)
	}
	writeServerSummary(&b, m, counts[m.ID])
	if len(m.Args) > 0 {
		fmt.Fprintf(&b, "启动参数: %v\n", m.Args)
	}
	if len(m.Env) > 0 {
		b.WriteString("环境变量:\n")
		for k, v := range m.Env {
			fmt.Fprintf(&b, "  %s=%s\n", k, v)
		}
	}
	b.WriteString("\n修改配置用 action=update（全量替换——带上要保留的全部字段）；删除用 action=remove。")
	return b.String(), nil
}

// writeServerSummary 是 list/get 共用的一台服务器的概要行。
func writeServerSummary(b *strings.Builder, m sessiondata.McServerSpec, toolCount int) {
	fmt.Fprintf(b, "## %s\n", m.ID)
	if m.Desc != "" {
		fmt.Fprintf(b, "描述: %s\n", m.Desc)
	}
	if m.Transport == "sse" {
		fmt.Fprintf(b, "传输: sse，端点: %s\n", m.URL)
	} else {
		cmd := m.Command
		if len(m.Args) > 0 {
			cmd += " " + strings.Join(m.Args, " ")
		}
		fmt.Fprintf(b, "传输: stdio，命令: `%s`\n", cmd)
	}
	fmt.Fprintf(b, "状态: %s\n", enabledText(m.Enabled))
	if toolCount > 0 {
		fmt.Fprintf(b, "已接入工具: %d 个\n", toolCount)
	}
}

// toolCounts 是 ToolCounts 的 nil 安全包装。
func (ops McpManageOps) toolCounts() (map[string]int, error) {
	if ops.ToolCounts == nil {
		return nil, nil
	}
	return ops.ToolCounts()
}

func mcpServerIDs(list []sessiondata.McServerSpec) string {
	names := make([]string, 0, len(list))
	for _, m := range list {
		names = append(names, m.ID)
	}
	return strings.Join(names, "、")
}

// confirmMcpManage 是确认门文案：写明「将执行 command / 连接 url」的后果。
func confirmMcpManage(_ context.Context, args json.RawMessage) string {
	var p mcpManageParams
	if err := json.Unmarshal(args, &p); err != nil {
		return fmt.Sprintf("即将操作 MCP 服务器（参数解析失败: %v）——请核对参数后重试。", err)
	}
	switch p.Action {
	case "add":
		var b strings.Builder
		fmt.Fprintf(&b, "即将安装 MCP 服务器「%s」：%s。\n\n", p.Server.ID, p.Server.endpointText())
		b.WriteString("安装后它会作为工具接入（新工具一律按高危处理）；默认会自动绑定到你的 MCP 列表。确认执行？")
		return b.String()
	case "update":
		var b strings.Builder
		fmt.Fprintf(&b, "即将修改 MCP 服务器「%s」的配置（全量替换）：%s。\n\n", p.Server.ID, p.Server.endpointText())
		b.WriteString("保存后会重新连接并对账工具。确认执行？")
		return b.String()
	case "enable":
		return fmt.Sprintf("即将启用 MCP 服务器「%s」：会重新连接端点/启动命令并恢复它的工具。确认执行？", p.Server.ID)
	case "disable":
		return fmt.Sprintf("即将停用 MCP 服务器「%s」：它的工具会从目录撤下（能力挂起，配置保留）。确认执行？", p.Server.ID)
	case "remove":
		return fmt.Sprintf("即将删除 MCP 服务器「%s」：目录里它的全部工具条目一并撤下，绑定项随之失效。确认删除？", p.Server.ID)
	case "list", "get":
		// 只读动作不该走到确认门（模型少给 enabled 字段等），文案说明无需操作。
		return fmt.Sprintf("action=%s 是只读查询，不会修改任何配置。", p.Action)
	default:
		return fmt.Sprintf("即将对 MCP 服务器执行未知操作 %q——请核对参数。", p.Action)
	}
}
