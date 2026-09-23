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

	"github.com/moyunteng/lxcode/internal/jsonrepair"
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
	// Mutates 标记工具是否变更外部世界（文件/命令执行）。与 Risk 正交：
	// edit 是低危（old_string 唯一匹配约束 + 原子写兜底）但变更文件——
	// strict 只读模式按本字段拒绝，而不是按 Risk（否则 edit 会漏网）。
	Mutates bool
	// Confirm 返回需人工确认的提示文本；空串 = 本组参数无需确认。
	// 高危工具不一定每次都确认（如 write_file 只在覆盖已有文件时）。
	// ctx 携带会话工作目录——确认门必须与执行层解析同一个文件
	//（相对路径在两边各自解析会 stat 错目录，把覆盖误判成新文件）。
	Confirm func(ctx context.Context, args json.RawMessage) string
	// Exec 执行工具，返回给模型的结果文本。错误不 panic——
	// 由 Execute 转成自解释文本回填模型（三层错误的 L1，模型可自行纠正）。
	Exec func(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry 是工具注册表。
//
// 两段式：内置段（New 时注册，进程生命周期内不变）与动态段（目录里的
// 自定义工具——M4 起由 SetDynamic 按工具目录整体替换）。mu 保护两段：
// 目录变更可能发生在生成中的工具循环里（另一个客户端正在改目录），
// 而执行工具时不持锁（自定义工具可能跑几十秒）。
type Registry struct {
	mu      sync.RWMutex
	defs    map[string]*Def
	order   []string        // 内置注册顺序，工具列表输出稳定
	dyn     []string        // 动态段顺序（SetDynamic 维护）
	builtin map[string]bool // 内置名集合（动态段不可覆盖它们）

	// sessionSearch 是注入的会话搜索实现（agent 挂载会话存储时接线）。
	// 工具本身无状态——JSONL 格式归 agent 所有，这里只持有函数避免重复定义格式。
	searchMu      sync.Mutex
	sessionSearch SessionSearchFn
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
// 读取类在前（read/search/session_search/read_skill），变更类在后
// （edit/write/bash），todo 收尾；agent.dispatch 是主 Agent 的调度
// 通道（子 Agent 白名单不含它——两类制深度恒 1）。
func New() *Registry {
	r := &Registry{defs: map[string]*Def{}, builtin: map[string]bool{}}
	r.register(readFileDef())
	r.register(searchDef())
	r.register(sessionSearchDef(r))
	r.register(readSkillDef(r))
	r.register(editDef())
	r.register(writeFileDef())
	r.register(bashDef())
	r.register(todoDef(r))
	r.register(dispatchDef(r))
	return r
}

// register 注册内置工具（只在 New 里调用——单线程，无需持锁）。
func (r *Registry) register(d *Def) {
	r.defs[d.Name] = d
	r.order = append(r.order, d.Name)
	r.builtin[d.Name] = true
}

// SetDynamic 用工具目录里的自定义工具整体替换动态段（内置段不动）。
// 返回被跳过的名字（与内置同名——内置实现优先，目录条目只是元数据）。
//
// 整体替换而非增量注册：目录是事实源，删除/改名/停用都必须在注册表里
// 同步消失——增量注册会让删掉的自定义工具继续对模型可见。
func (r *Registry) SetDynamic(defs []*Def) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range r.dyn {
		delete(r.defs, name)
	}
	r.dyn = nil
	var skipped []string
	for _, d := range defs {
		if d == nil || strings.TrimSpace(d.Name) == "" {
			continue
		}
		if r.builtin[d.Name] {
			skipped = append(skipped, d.Name)
			continue
		}
		if _, dup := r.defs[d.Name]; dup {
			skipped = append(skipped, d.Name)
			continue
		}
		r.defs[d.Name] = d
		r.dyn = append(r.dyn, d.Name)
	}
	return skipped
}

// namesLocked 返回两段的名字（内置在前）。调用方须持锁。
func (r *Registry) namesLocked() []string {
	out := make([]string, 0, len(r.order)+len(r.dyn))
	out = append(out, r.order...)
	out = append(out, r.dyn...)
	return out
}

// Order 返回注册顺序的工具名列表（内置段在前，动态段在后；backend 生成
// 系统提示词的工具清单用）。
func (r *Registry) Order() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.namesLocked()
}

// Get 按名取工具。
func (r *Registry) Get(name string) (*Def, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.defs[name]
	return d, ok
}

// LLMTools 返回 wire 声明（给 llm.WithTools）。
func (r *Registry) LLMTools() []llm.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := r.namesLocked()
	out := make([]llm.Tool, 0, len(names))
	for _, name := range names {
		d := r.defs[name]
		out = append(out, llm.Tool{
			Name: d.Name, Description: d.Description, Parameters: d.Parameters,
		})
	}
	return out
}

// Confirm 返回需人工确认的提示；空串 = 无需确认。未知工具返回空串
// （Execute 会把未知工具报给模型）。
func (r *Registry) Confirm(ctx context.Context, call llm.ToolCall) string {
	d, ok := r.Get(call.Function.Name)
	if !ok || d.Confirm == nil {
		return ""
	}
	// 不持锁执行：确认回调会 stat 文件系统（可能与目录变更并发，无妨）
	return d.Confirm(ctx, []byte(call.Function.Arguments))
}

// IsMutating 报告工具是否变更外部世界（strict 只读模式的拒绝依据）。
// 未知工具返回 true（保守：不认识的变更面按危险处理）。
func (r *Registry) IsMutating(name string) bool {
	d, ok := r.Get(name)
	return !ok || d.Mutates
}

// Execute 校验并执行工具调用，返回给模型的结果文本。
func (r *Registry) Execute(ctx context.Context, call llm.ToolCall) string {
	d, ok := r.Get(call.Function.Name)
	if !ok {
		return fmt.Sprintf("错误: 未知工具 %q（可用: %v）", call.Function.Name, r.Order())
	}
	raw := call.Function.Arguments
	// 系统层硬校验 + 一次保守修复（internal/jsonrepair——agent 与 llm 适配器
	// 共用同一份实现）：本地小模型的 arguments 偶发不是合法 JSON（字符串里带
	// 裸换行、尾逗号、被截断）。直接拒绝会白白丢掉一整轮并诱发同样的错误重试，
	// 所以先试"原样"，再试修复，都不过才报错。
	args := []byte(raw)
	note := ""
	if !json.Valid(args) {
		if fixed, ok := jsonrepair.Repair(raw); ok {
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
