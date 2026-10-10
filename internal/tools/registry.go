// Package tools 实现 agent 的工具体系：注册表 + 内置工具（read_file /
// search / web_search / session_search / todo）+ 风险分级执行。
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

	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/jsonrepair"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
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
	// webSearch 是注入的网页搜索实现（server 装配搜索渠道时接线）。
	// 与 sessionSearch 共用一把锁：两者都是「装配期写一次、运行期只读」的
	// 注入点，各配一把锁只是多一处可能忘记加锁的地方。
	webSearch WebSearchFn

	// jobsMgr 是注入的后台任务管理器（server 装配时接线；nil = 未装配）。
	// jobCursors 是 job_output 的读游标，键是「会话 id + 任务 id」——游标是
	// **会话级**状态：注册表是进程级单例，只按任务 id 存会让两个会话互相
	// 吃掉对方的输出（一个会话读过，另一个就看不到）。
	jobsMu     sync.Mutex
	jobsMgr    *jobs.Manager
	jobCursors map[string]int64

	// mergeStart 是注入的「起一个合并进程」实现（server 装配时接线；nil = 未装配）。
	// 与 sessionSearch / webSearch 共用 searchMu：三者都是「装配期写一次、运行期只读」
	// 的注入点，各配一把锁只是多一处可能忘记加锁的地方。
	mergeStart MergeStartFn
	// workspace 是注入的工作区三工具（status/sync/rollback）的 server 侧实现
	//（server 装配时接线；零值 = 未装配，工具回「未装配」自解释错误）。
	// 与 mergeStart 共用 searchMu（同一批装配期注入点）。
	workspace WorkspaceOps
}

// SessionSearchFn 是会话搜索的实现约定：在全部会话（含当前）的消息内容里
// 按正则搜索，返回给模型的结果文本。
//
// 参数用 sessiondata.SearchQuery（而不是 pattern+max 两个标量）：查询参数会
// 随能力增长（已加 role/context），每加一个就改一次函数签名会让注入方
// （server 与 agent 两处）与所有测试桩一起返工。
type SessionSearchFn func(ctx context.Context, q sessiondata.SearchQuery) (string, error)

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
// 读取类在前（read/search/web_search/web_fetch/session_search/read_skill），变更类在后
// （edit/write/bash），todo 收尾；agent_dispatch 是主 Agent 的调度
// 通道（子 Agent 白名单不含它——两类制深度恒 1）。
func New() *Registry {
	r := &Registry{defs: map[string]*Def{}, builtin: map[string]bool{}}
	r.register(readFileDef())
	r.register(searchDef())
	r.register(webSearchDef(r))
	r.register(webFetchDef())
	// 后台任务三件套（docs/jobs.md §3）：读输出 / 列任务 / 停任务。
	// 放在 web_fetch 之后、session_search 之前——清单顺序即系统提示词的顺序，
	// 读取类在前、调度类收尾。
	r.register(jobOutputDef(r))
	r.register(jobListDef(r))
	r.register(jobKillDef(r))
	r.register(sessionSearchDef(r))
	r.register(readSkillDef(r))
	r.register(editDef())
	r.register(writeFileDef())
	r.register(bashDef(r))
	r.register(todoDef(r))
	// ask_user（确认门的「提问」形态）：模型向用户提问、等用户回答——冲突
	// 抉择、需要用户决策时用。与 todo 同为会话级交互工具，排在变更类之后。
	r.register(askUserDef())
	r.register(dispatchDef(r))
	// 合并进程入口（第二个 producer）：起一个后台合并任务，任务体是内置合并 Agent
	// 的独立子会话。与 agent_dispatch 同为调度类，排在最后。
	r.register(mergeRequestDef(r))
	// 工作区三件套（用户无感链路的收尾）：status 查询（低危只读）、sync 提交推送
	// 与 rollback 回滚（中危走确认门）、publish 产物发布（走确认门）。
	// 排在 merge_request 之后——同一族能力。
	r.register(workspaceStatusDef(r))
	r.register(workspaceSyncDef(r))
	r.register(workspaceRollbackDef(r))
	r.register(workspacePublishDef(r))
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
