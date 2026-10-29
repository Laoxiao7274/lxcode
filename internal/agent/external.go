// 「外部操作数」的计数（docs/session-tree.md §C.1）：这一轮有 N 处改动**无法快照**，
// 回滚不到。口径必须单点定义——树面板与回滚确认文案都读它，两处各算一遍必然对不上。
//
// 计入规则（每条都对应一个理由）：
//   - 每个**成功执行**（isError == false 且 durationMs > 0）且 IsMutating(name) == true
//     且**不是** edit/write_file 的调用 → external++（bash / MCP / 自定义工具）；
//   - edit/write_file 但**快照被跳过**（大文件/二进制/落盘失败）→ external++（同样回滚不了）；
//   - 被拒绝 / 被 strict 拦 / 被取消的调用**不计**（它们没改动任何东西——这与
//     「工具耗时的 0 = 没执行，不计入统计」是同一条口径纪律）；
//   - 子会话的外部操作**也计入父会话那一轮**（经 DispatchResult.ExternalCount 带回）：
//     dispatch 那一次调用本身就是「父会话这一轮的操作」，而子 Agent 在里面跑的 bash
//     同样无法还原。
package agent

import (
	"sync"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
)

// snapshotCapableTools 是**能抓快照**的写工具（§C.1 的结论：只覆盖这两个）。
var snapshotCapableTools = map[string]bool{"edit": true, "write_file": true}

// turnCounters 是一轮的外部操作计数（按锚点 seq 记账，轮末汇总）。
//
// 为什么按轮记而不是全局累加：树面板要的是「这一轮几处」，而回滚确认文案要的是
// 「从锚点起共几处」——两个口径都从同一份按轮的账派生，不各算一遍。
type turnCounters struct {
	mu sync.Mutex
	// bySeq: 锚点 seq → 该轮的 external 计数（含子会话回填的部分）。
	bySeq map[int64]int
}

func newTurnCounters() *turnCounters {
	return &turnCounters{bySeq: make(map[int64]int)}
}

// add 给某一轮加 external 计数。
func (c *turnCounters) add(anchor int64, n int) {
	if anchor <= 0 || n <= 0 {
		return
	}
	c.mu.Lock()
	c.bySeq[anchor] += n
	c.mu.Unlock()
}

// countOf 读某一轮的 external 计数（0 = 没有无法快照的写）。
func (c *turnCounters) countOf(anchor int64) int {
	if anchor <= 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bySeq[anchor]
}

// snapshotSkippedTools 是「这一轮里 edit/write_file 中快照被跳过的调用 id」。
// finishToolCall 在结果落地时把工具自报的「未记快照」提示记进来（见 snapshotNotice）。
//
// 为什么不用工具返回值判断：工具层不认识快照（那是 ctx 注入的旁路），它只在
// Commit 失败/skip 时把一句人话附在结果文本里。用**同一句常量**匹配是最薄的耦合，
// 比让工具层回传一个结构体（那要改 Def.Exec 的签名，波及全部 24 个工具）便宜得多。
// 文案常量定义在 snapshot.go（toolsSnapshotSkipMarker）——两处引用同一个值，
// 不各写一遍字符串字面量（那是最容易漂移的一类重复）。
const snapshotSkipMarker = toolsSnapshotSkipMarker

// countExternal 判断一次**已执行**的工具调用是否计入 external。
//
// 三个输入：工具名、是否变更、结果正文（用于识别「快照被跳过」）。
// 只对**成功执行**的调用调用本函数（拒绝/取消不计——调用方保证）。
func countExternal(name string, mutates bool, result string) int {
	if !mutates {
		return 0
	}
	if snapshotCapableTools[name] {
		// edit/write_file：快照被跳过才算（大文件/二进制/落盘失败）。
		if containsMarker(result) {
			return 1
		}
		return 0
	}
	// 其余变更类工具（bash / MCP / 自定义 / workspace_*）：一律回滚不到。
	return 1
}

// containsMarker 报告结果正文里是否带「未记快照」的提示。
func containsMarker(result string) bool {
	return len(result) >= len(snapshotSkipMarker) && indexOf(result, snapshotSkipMarker) >= 0
}

// indexOf 是 strings.Index 的薄封装（避免在这个文件再引一次 strings 的命名歧义）。
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// externalCountFor 返回「这一轮（锚点 seq）有几处无法快照的写」。
func (s *Session) externalCountFor(anchor int64) int {
	s.mu.Lock()
	c := s.turnCounters
	s.mu.Unlock()
	if c == nil {
		return 0
	}
	return c.countOf(anchor)
}

// 编译期断言：llm 与 tools 在本文件被引用（DispatchResult 的字段与工具判定都来自它们）。
var _ = llm.Message{}
var _ = tools.DispatchResult{}
