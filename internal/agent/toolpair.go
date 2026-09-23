package agent

import "github.com/moyunteng/lxcode/internal/llm"

// ToolPairing 是一段历史的工具配对分析（DSH compaction 的 tool-pairing
// 不变量，Go 版）：**压缩的切割点绝不能落在 assistant 声明的 tool_calls
// 与它对应的 tool 结果之间**——切开就是畸形历史，严格端点会 400。
//
// 为什么单独成文件而不是塞进 session.go：这是纯函数（输入历史、输出切点
// 平衡性），既是压缩选区间的前置（P3），也是「取消时补配对」的依据（P5），
// 两条路径共用同一份判定——判定漂移的代价是静默产生畸形历史。
type ToolPairing struct {
	// Cuts[i] = 历史前 i 条之后的切点是否平衡（i ∈ [0, len]）。
	// 平衡 = 没有「已声明但还没回填」的工具调用横跨该切点。
	Cuts []bool
	// Unpaired 是始终没等到 tool 结果的调用 id（按出现顺序）——历史畸形
	// 的直接证据（用户库里真实存在这种残缺会话：取消时未补配对）。
	Unpaired []string
	// Orphans 是没有对应调用的 tool 结果下标（畸形，防御性记录）。
	Orphans []int
}

// AnalyzeToolPairing 扫描历史，得出每个切点的平衡性与配对诊断。
//
// 游标语义与 DSH 一致：assistant 带 N 个 tool_call 则 +N，tool 结果则 −1。
// 但**畸形数据不抛异常、而是钳到 0 并记录**：我们的历史来自 SQLite，残缺
// 记录（取消时未回填）是既成事实，加载路径上抛异常会让老会话直接打不开。
//
// 两条判定刻意分开：Cuts 用**计数**（对端点改写 id 鲁棒——它只回答「能不能
// 在这里切」），Unpaired 用 **id**（精确回答「哪一个调用没等到结果」）。二者
// 在 id 错配时会分叉，这不是 bug：一个管安全，一个管诊断。
func AnalyzeToolPairing(history []llm.Message) ToolPairing {
	cuts := make([]bool, len(history)+1)
	cuts[0] = true // 空历史/开头切点恒平衡
	open := 0
	pending := make([]string, 0, 4)
	var orphans []int
	for i, m := range history {
		switch m.Role {
		case "assistant":
			for _, tc := range m.ToolCalls {
				open++
				pending = append(pending, tc.ID)
			}
		case "tool":
			if open == 0 {
				// 没有前置调用：钳到 0（负游标会让之后所有切点失去意义，
				// 可用信号比精确崩溃更重要），下标另记诊断
				orphans = append(orphans, i)
			} else {
				open--
				pending = consumeID(pending, m.ToolCallID)
			}
		}
		cuts[i+1] = open == 0
	}
	return ToolPairing{Cuts: cuts, Unpaired: pending, Orphans: orphans}
}

// consumeID 按 id 精确摘除待回填项。id 对不上时**不动列表**——那个调用确实
// 没等到自己的结果（端点改写 id 也是畸形，精确报告比静默吞掉更有用）；
// 「能不能切」由计数决定，不受这里影响。
func consumeID(pending []string, id string) []string {
	for i, p := range pending {
		if p == id {
			return append(pending[:i:i], pending[i+1:]...)
		}
	}
	return pending
}

// BalancedBefore 返回「历史前 i 条之后」这个切点是否平衡（i ∈ [0, len]）。
func (p ToolPairing) BalancedBefore(i int) bool {
	if i < 0 || i >= len(p.Cuts) {
		return false
	}
	return p.Cuts[i]
}

// BalancedAfter 返回第 i 条之后（= 前 i+1 条）的切点是否平衡。
func (p ToolPairing) BalancedAfter(i int) bool { return p.BalancedBefore(i + 1) }

// NearestBalancedAtOrBefore 从 i 往前找最近的平衡切点，返回其下标；
// 找不到返回 -1。压缩选区间用它把「保留预算边界」回退到安全切割点。
func (p ToolPairing) NearestBalancedAtOrBefore(i int) int {
	if i > len(p.Cuts)-1 {
		i = len(p.Cuts) - 1
	}
	for ; i >= 0; i-- {
		if p.Cuts[i] {
			return i
		}
	}
	return -1
}
