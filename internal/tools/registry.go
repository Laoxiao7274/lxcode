// Package tools 实现 agent 的工具体系：注册表 + 内置工具（read_file /
// edit / write_file / bash / search / session_search / todo）+ 风险分级执行。
// 风险分级是硬约束（AGENTS.md §4）：低危自动执行；高危须经调用方的
// 人工确认门（CLI 的 y/n）。执行层硬校验：参数必须是合法 JSON（坏 JSON 先走
// 一次保守修复——本地/小模型坏参数是高频失败形态）、bash 有超时与输出上限、
// read 有大小上限——不依赖 LLM 自觉。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/moyunteng/lxcode/internal/llm"
)

// RiskLevel 工具风险等级。
type RiskLevel int

const (
	RiskLow  RiskLevel = iota // 低危：自动执行
	RiskHigh                  // 高危：须人工确认
)

// Def 是工具的完整定义：wire 声明 + 风险 + 执行器。
type Def struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema（透传给模型）
	Risk        RiskLevel
	// Confirm 返回需人工确认的提示文本；空串 = 本组参数无需确认。
	// 高危工具不一定每次都确认（如 write_file 只在覆盖已有文件时）。
	Confirm func(args json.RawMessage) string
	// Exec 执行工具，返回给模型的结果文本。错误不 panic——
	// 由 Execute 转成自解释文本回填模型（三层错误的 L1，模型可自行纠正）。
	Exec func(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry 是工具注册表。
type Registry struct {
	defs  map[string]*Def
	order []string // 注册顺序，工具列表输出稳定

	// sessionSearch 是注入的会话搜索实现（agent 挂载会话存储时接线）。
	// 工具本身无状态——JSONL 格式归 agent 所有，这里只持有函数避免重复定义格式。
	searchMu      sync.Mutex
	sessionSearch SessionSearchFn

	// todoSink 是注入的 todo 状态写入（agent.Session 接线）：会话持有清单
	// 状态供 UI 渲染，工具层只做校验与格式化。
	todoMu   sync.Mutex
	todoSink TodoWriteFn
}

// SessionSearchFn 是会话搜索的实现约定：在全部会话（含当前）的消息内容里
// 按正则搜索，返回给模型的结果文本。max 为命中上限（≤0 取默认）。
type SessionSearchFn func(ctx context.Context, pattern string, max int) (string, error)

// SetSessionSearch 注入会话搜索实现（backend.AttachSessionStore 时调用）。
func (r *Registry) SetSessionSearch(fn SessionSearchFn) {
	r.searchMu.Lock()
	r.sessionSearch = fn
	r.searchMu.Unlock()
}

func (r *Registry) getSessionSearch() SessionSearchFn {
	r.searchMu.Lock()
	defer r.searchMu.Unlock()
	return r.sessionSearch
}

// New 创建注册表并注册内置工具。顺序即系统提示词里工具清单的顺序：
// 读取类在前（read/search/session_search），变更类在后（edit/write/bash），
// todo 收尾（规划状态，非文件操作）。
func New() *Registry {
	r := &Registry{defs: map[string]*Def{}}
	r.register(readFileDef())
	r.register(searchDef())
	r.register(sessionSearchDef(r))
	r.register(editDef())
	r.register(writeFileDef())
	r.register(bashDef())
	r.register(todoDef(r))
	return r
}

func (r *Registry) register(d *Def) {
	r.defs[d.Name] = d
	r.order = append(r.order, d.Name)
}

// Order 返回注册顺序的工具名列表（backend 生成系统提示词的工具清单用）。
func (r *Registry) Order() []string {
	return append([]string(nil), r.order...)
}

// Get 按名取工具。
func (r *Registry) Get(name string) (*Def, bool) {
	d, ok := r.defs[name]
	return d, ok
}

// LLMTools 返回 wire 声明（给 llm.WithTools）。
func (r *Registry) LLMTools() []llm.Tool {
	out := make([]llm.Tool, 0, len(r.order))
	for _, name := range r.order {
		d := r.defs[name]
		out = append(out, llm.Tool{
			Name: d.Name, Description: d.Description, Parameters: d.Parameters,
		})
	}
	return out
}

// Confirm 返回需人工确认的提示；空串 = 无需确认。未知工具返回空串
// （Execute 会把未知工具报给模型）。
func (r *Registry) Confirm(call llm.ToolCall) string {
	d, ok := r.defs[call.Function.Name]
	if !ok || d.Confirm == nil {
		return ""
	}
	return d.Confirm([]byte(call.Function.Arguments))
}

// Execute 校验并执行工具调用，返回给模型的结果文本。
func (r *Registry) Execute(ctx context.Context, call llm.ToolCall) string {
	d, ok := r.defs[call.Function.Name]
	if !ok {
		return fmt.Sprintf("错误: 未知工具 %q（可用: %v）", call.Function.Name, r.order)
	}
	raw := call.Function.Arguments
	// 系统层硬校验 + 一次保守修复：本地小模型的 arguments 偶发不是合法 JSON
	// （字符串里带裸换行、尾逗号、被截断）。直接拒绝会白白丢掉一整轮并诱发
	// 同样的错误重试，所以先试"原样"，再试修复，都不过才报错。
	args := []byte(raw)
	note := ""
	if !json.Valid(args) {
		if fixed, ok := repairJSON(raw); ok {
			args, note = []byte(fixed), "（注：工具参数不是合法 JSON，已自动修复后执行）\n"
		} else {
			return fmt.Sprintf("错误: 工具 %s 的 arguments 不是合法 JSON: %.100s", call.Function.Name, raw)
		}
	}
	out, err := d.Exec(ctx, args)
	if err != nil {
		return "错误: " + err.Error()
	}
	return note + out
}

// repairJSON 保守修复小模型常见的几种坏 JSON，成功返回修复后的文本。
//
// 只做"几乎确定模型本意"的变换，不做语义猜测；任何一步失败就继续下一步。
// 修复顺序对应本地模型的真实失败形态：裸换行/裸制表符（多行命令最常见）、
// 尾逗号、单引号包 key、以及被 max_tokens 截断（补右侧括号补齐）。
func repairJSON(raw string) (string, bool) {
	candidates := []string{
		fixRawWhitespace(raw), // 字符串内裸换行/制表符 → \n \t，并去掉尾逗号
		fixSingleQuotes(raw),  // 单引号字符串 → 双引号
		closeTruncated(raw),   // 截断的 JSON：补右括号/右引号
		closeTruncated(fixRawWhitespace(raw)),
		closeTruncated(fixSingleQuotes(raw)),
	}
	for _, c := range candidates {
		if c != "" && json.Valid([]byte(c)) {
			return c, true
		}
	}
	return "", false
}

// fixRawWhitespace 把字符串字面量内部的裸换行/制表符转义，并删掉对象/数组
// 的尾逗号。扫描式实现：跟踪是否处于字符串内与是否被反斜杠转义。
func fixRawWhitespace(s string) string {
	var b strings.Builder
	inStr := false
	esc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
			b.WriteByte(c)
		case c == '\\' && inStr:
			esc = true
			b.WriteByte(c)
		case c == '"':
			inStr = !inStr
			b.WriteByte(c)
		case inStr && c == '\n':
			b.WriteString(`\n`)
		case inStr && c == '\r':
			// 归一：CRLF 视作一个换行
			if i+1 < len(s) && s[i+1] == '\n' {
				continue
			}
			b.WriteString(`\n`)
		case inStr && c == '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	out := stripTrailingCommas(b.String())
	return out
}

// stripTrailingCommas 删除 `,` 后紧跟 `}` 或 `]` 的尾逗号（JSON 规范不允许）。
func stripTrailingCommas(s string) string {
	var b strings.Builder
	inStr := false
	esc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
			b.WriteByte(c)
		case c == '\\' && inStr:
			esc = true
			b.WriteByte(c)
		case c == '"':
			inStr = !inStr
			b.WriteByte(c)
		case !inStr && c == ',':
			// 向前看下一个非空白字符
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\n' || s[j] == '\t' || s[j] == '\r') {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				continue // 丢弃这个逗号
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// fixSingleQuotes 把单引号包起来的字符串改成双引号（模型偶尔用 JS/Python 习惯）。
// 只在整体不含双引号时启用，避免误伤正常 JSON 里的撇号。
func fixSingleQuotes(s string) string {
	if strings.Contains(s, `"`) || !strings.Contains(s, "'") {
		return ""
	}
	return strings.ReplaceAll(s, "'", `"`)
}

// closeTruncated 处理被 max_tokens 截断的 JSON：先补未闭合的字符串引号，
// 再按栈补右括号。返回空串表示没有可补的东西（例如整体不是 JSON 开头）。
func closeTruncated(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || (s[0] != '{' && s[0] != '[') {
		return ""
	}
	// 先去掉可能残留的半个键值（截断点常落在 `"command": "ls` 这类中间）
	inStr := false
	esc := false
	stack := []byte{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case !inStr && (c == '{' || c == '['):
			stack = append(stack, c)
		case !inStr && (c == '}' || c == ']'):
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	out := s
	if inStr {
		out += `"` // 补上被截断的字符串引号
	}
	// 截断常留一个没有值的 key 或悬空逗号，补个占位值让结构合法
	trimmed := strings.TrimRight(out, " \t\r\n")
	if strings.HasSuffix(trimmed, ",") {
		out = trimmed
	} else if strings.HasSuffix(trimmed, ":") {
		out = trimmed + `""`
	} else if n := len(stack); n > 0 && strings.HasSuffix(trimmed, "{") {
		out = trimmed
	}
	out = stripTrailingCommas(out)
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			out += "}"
		} else {
			out += "]"
		}
	}
	return out
}
