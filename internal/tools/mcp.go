package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// MCP 工具的执行面（M4）：MCP 服务器暴露的工具在运行时注册进注册表——
// 与 source=binary 的自定义工具同一段（SetDynamic 整体替换），只是执行体
// 走 MCP 客户端而不是 spawn 本地进程。
//
// **风险一律按高危 + 变更处理**，不采信服务器自报的注解（readOnlyHint 等）：
// MCP 规范明说「clients MUST consider tool annotations to be untrusted unless
// they come from trusted servers」——服务器可以自称只读来换取自动执行。要放宽
// 只能靠用户显式选 auto 审批档，而不是让服务器自己给自己降级。
//
// 这个决定牺牲的是「只读 MCP 工具也要确认」的便利，换来的是「第三方服务器
// 不能借注解提权」——方向与「子 Agent 取严继承」一致。

// MCPCallFn 是 MCP 工具的执行约定：server 装配时把 manager 的调用接进来。
//
// 与 SessionSearchFn/WebSearchFn 同款注入：tools 包不持有 MCP 客户端
// （连接生命周期归 server 管理），只持有「按名字调用」这一个函数。
type MCPCallFn func(ctx context.Context, name string, args json.RawMessage) (string, error)

// MCPToolSpec 是一条 MCP 工具的描述（server 从 mcp.ToolEntry 映射而来）。
//
// 用扁平参数而不是直接吃 mcp.Tool：tools 包不 import mcp（依赖方向保持
// 「装配在 server」，与 sessiondata 只作中立类型同一条纪律）。
type MCPToolSpec struct {
	// Name 是暴露给模型的名字（已净化，见 mcp.ExposedName）。
	Name string
	// MCPName 是服务器上的原名（确认提示里显示，便于用户对账）。
	MCPName string
	// ServerID 是来源服务器 id（确认提示与错误说明里显示）。
	ServerID string
	// Description 是服务器给的说明（可能为空——空则给一句兜底）。
	Description string
	// InputSchema 是服务器给的参数 schema（坏 JSON 回落宽松 schema）。
	InputSchema json.RawMessage
}

// MCPDef 把一条 MCP 工具转成可执行的工具定义。
//
// call 为 nil 或 spec.Name 为空时返回错误（调用方跳过并记录，不让一条坏条目
// 把整份 MCP 面带下水——与 CustomDef 同款纪律）。
func MCPDef(spec MCPToolSpec, call MCPCallFn) (*Def, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return nil, fmt.Errorf("MCP 工具缺少名字")
	}
	if call == nil {
		return nil, fmt.Errorf("MCP 工具 %s 未接线（manager 未装配）", name)
	}
	desc := strings.TrimSpace(spec.Description)
	if desc == "" {
		desc = "MCP 服务器 " + spec.ServerID + " 提供的工具。"
	}
	desc += "\n来源: MCP 服务器 " + spec.ServerID + "（工具名 " + spec.MCPName + "）"

	callName := name
	return &Def{
		Name:        name,
		Description: desc,
		Parameters:  mcpSchema(spec.InputSchema),
		// 一律高危 + 变更：注解不可信（见文件头），所以不按服务器自报降级。
		Risk:    RiskHigh,
		Mutates: true,
		// 确认文本点名**服务器与原名**：用户要能看出这一下打到谁身上
		//（工具名是净化后的，原名才认得出是哪个服务器）。
		Confirm: func(_ context.Context, _ json.RawMessage) string {
			return fmt.Sprintf("调用 MCP 工具 %s（服务器 %s，原名 %s）", name, spec.ServerID, spec.MCPName)
		},
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			out, err := call(ctx, callName, args)
			if err != nil {
				// 错误回填模型而不是中断整轮：MCP 服务器不可用是常态
				//（进程崩了/网络断了），模型据此可以换别的办法。
				return "", err
			}
			if strings.TrimSpace(out) == "" {
				return "（MCP 工具返回了空内容）", nil
			}
			return out, nil
		},
	}, nil
}

// mcpSchema 校验服务器给的参数 schema。
//
// 坏 schema（不是合法 JSON、不是对象）回落成宽松 schema——**不报错**：
// 服务器给什么 schema 是它的事，我们不该因为它的 schema 不合口味就把
// 整个工具下架（工具本身还能用，模型拿不到参数提示而已）。
func mcpSchema(raw json.RawMessage) json.RawMessage {
	fallback := json.RawMessage(`{"type":"object","properties":{}}`)
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return fallback
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		return fallback
	}
	// 网关对 tools 的 schema 有硬约束：不是 object 类型的会被严格端点拒收整轮。
	if probe.Type != "object" {
		return fallback
	}
	return json.RawMessage(trimmed)
}
