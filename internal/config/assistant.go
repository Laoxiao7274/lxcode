// assistant.go —— 助手性格与拓展配置（config/assistant.json，与 models.json 同目录）。
//
// 照 behavior.go 同一条模式：**小而关键的覆盖配置**，文件缺失/损坏不拒绝启动——
// 助手配置的坏文件不该拖垮助手域，各字段独立回落空（空 = 用内置值，见
// internal/server/assistant.go 的 merge：Name 空 = 「助手」、Persona 空 = 无自定义段、
// Skills/Workflow 空 = 不绑定）。热加载不靠 reloadLoop 主动 Reload：resolver 每次
// 组装 Agent 语境时现读（文件只有几十字节），所以手改 / set 后**下一轮**立即生效；
// 广播由 server 侧负责（set 后与 reloadLoop 的变更检测各一处）。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/moyunteng/lxcode/internal/atomicfile"
)

// Assistant 是 assistant.json 的载荷。
//
// 与 Behavior 同一条「字段独立归一」纪律：只校验出一个非法值就把另一个合法值
// 一起丢掉是错的（set 只改一个字段时另一个必须原样保留）。字符串字段 omitempty：
// 空字段就是「用内置值」的信号，磁盘上不该带空键。
type Assistant struct {
	// Name 是助手显示名（覆盖内置「助手」）；空 = 用内置值。
	Name string `json:"name,omitempty"`
	// Persona 是性格/语气设定（灌进 AgentDef.Prompt 的第三层追加，非整段替换）；
	// 空 = 无自定义段。**首次初始化的判定源**：Persona 为空时系统提示词注入
	// 「主动询问称呼与性格偏好」的指令（见 compose.go）。
	Persona string `json:"persona,omitempty"`
	// Skills 是技能模块 id（多选注入）；空 = 不绑定。**存的是用户的声明**——
	// 有效性（id 是否存在于 modules 表）由读侧现查现过滤，配置层不校验。
	Skills []string `json:"skills,omitempty"`
	// Workflow 是流程模板 id（单选注入）；空 = 不绑定。同 Skills：读侧现查。
	Workflow string `json:"workflow,omitempty"`
	// MCPServers 是 MCP 服务器 id（服务器粒度绑定，多选）：合入助手工具白名单
	// 的是这些服务器的**全部已物化工具**（暴露名 = <server>_<tool>）。空 = 不绑
	// 定。同 Skills：**存的是用户的声明**——有效性（id 是否存在于 mcp_servers
	// 表、服务器是否在用）由读侧现查现过滤（resolver 每轮现取 st.ListTools()，
	// 服务器停用/撤下后其工具名悬空，compose 层的 missingTools 提示兜住）。
	MCPServers []string `json:"mcpServers,omitempty"`
	// Tools 是**自定义命令工具** id（条目粒度绑定，多选）：合入助手工具白名单
	// 的是目录里 custom=true && source=="binary" 的条目——内置写类工具
	//（bash/edit 等）全部排除在可选面外（UI 过滤 + set 校验 + resolver 过滤
	// 三闸齐全——内置条目也在 tools 表里，靠 custom/source 区分）。空 = 不绑定。
	// 同 Skills：**存的是用户的声明**——有效性（id 是否存在于 tools 表且仍是
	// custom binary）由读侧现查现过滤，配置层不校验；工具被删后名字悬空，
	// resolver 不再合入（按目录过滤的副作用）。
	//
	// **仅在 ToolWhitelist 为空时生效**（2026-12 用户拍板：助手默认工具完全可配
	// ——whitelist 非空 = 完整白名单覆盖，旧 tools 追加通道停用以防双源叠加）。
	Tools []string `json:"tools,omitempty"`
	// ToolWhitelist 是助手工具白名单的**完整覆盖**（2026-12 用户拍板：默认工具
	// 完全可配——「跟我们的都得一样，完全可控」）：非空 = 它就是助手的全部静态
	// 工具面（内置 bash/edit 也可勾入——用户担责；MCP 工具仍由 mcpServers 独立
	// 合入，动态面与静态面正交）；空 = 用内置默认 14 项（internal/server 的
	// assistantAgentDef.Tools——内置值不在这里写死，避免两处漂移）。同 Tools：
	// **存的是用户的声明**——有效性（id 是否存在于 tools 表，builtin/binary/
	// custom/mcp 都放行）由 set 现查校验；手改文件塞了悬空 id 时 resolver 按
	// 目录过滤掉。
	ToolWhitelist []string `json:"toolWhitelist,omitempty"`
}

// Normalized 返回裁剪后的配置：字符串字段去首尾空白，Skills/Tools/
// ToolWhitelist/MCPServers 逐项去空白并丢空项。
// **不校验** Skills/Workflow/Tools/ToolWhitelist/MCPServers 的有效性（那是读侧
// 对 modules / tools 表现查的事——配置层不知道目录里有哪些 id）；不报错、
// 不补默认值——空字段就是「用内置值」的信号。
func (a Assistant) Normalized() Assistant {
	n := a
	n.Name = strings.TrimSpace(n.Name)
	n.Persona = strings.TrimSpace(n.Persona)
	n.Workflow = strings.TrimSpace(n.Workflow)
	n.Tools = normalizeIDs(n.Tools)
	n.ToolWhitelist = normalizeIDs(n.ToolWhitelist)
	skills := make([]string, 0, len(n.Skills))
	for _, s := range n.Skills {
		if s = strings.TrimSpace(s); s != "" {
			skills = append(skills, s)
		}
	}
	// 全空时落 nil（而不是空切片）：JSON 序列化不带 "skills":[]，磁盘文件干净。
	if len(skills) == 0 {
		skills = nil
	}
	n.Skills = skills
	n.MCPServers = normalizeIDs(n.MCPServers)
	return n
}

// normalizeIDs 是 Skills/Tools/MCPServers 共用的清单归一：逐项去空白、丢空项、
// 全空落 nil（与 Skills 内联的那段同一条语义——抽出来供两处用）。
func normalizeIDs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// LoadAssistant 读助手配置。文件缺失/损坏 = 零值（各字段空 = 用内置值——
// 与 LoadBehavior 的容错语义一致：坏配置宁可回落也不让助手发不出话）。
func LoadAssistant(path string) Assistant {
	data, err := os.ReadFile(path)
	if err != nil {
		return Assistant{}
	}
	var a Assistant
	if err := json.Unmarshal(data, &a); err != nil {
		return Assistant{}
	}
	return a.Normalized()
}

// SaveAssistant 归一后原子写回（与 remote.json 同一份 atomicfile 实现）。
func SaveAssistant(path string, a Assistant) error {
	a = a.Normalized()
	data, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("序列化助手配置失败: %w", err)
	}
	return atomicfile.Write(path, data)
}
