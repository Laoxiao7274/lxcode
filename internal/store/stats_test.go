// 会话统计的折叠契约（2026-09-30，对齐 DSH 的 sessionStats + tokenUsage 投影）。
//
// 三条最值钱的断言（折叠逻辑写错的代价是"数字看着对，其实在骗人"）：
//   - **压缩不改统计**：折叠的是整段日志（含被检查点影子掉的消息），压缩之后步数/token
//     一个都不许变——变了就说明折叠读的是"当前可见历史"而不是整段日志；
//   - **撤回改统计**：撤回真删了行，数字跟着变小才是对的；
//   - **提示条不算轮**：注入的重复调用提醒/后台任务通告是 user 角色消息，但它们不是
//     用户说的话（notice 位标记）——算进去会让"轮数"凭空多出来。
package store

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func TestSessionStatsFold(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}

	// 空会话：零值（调用方据此让 wire 上整键缺席，前端不渲染统计胶囊）
	if st, err := s.SessionStatsOf(id); err != nil || !st.Empty() {
		t.Fatalf("空会话统计应为零值: %+v err=%v", st, err)
	}

	appendMsg := func(m llm.Message) {
		t.Helper()
		if _, err := s.AppendMsg(id, m); err != nil {
			t.Fatal(err)
		}
	}
	// 第 1 轮：用户说话 → 一次模型调用（有首字与用量）→ 一次工具调用（有耗时）
	appendMsg(llm.Message{Role: "user", Content: "跑一下测试"})
	appendMsg(llm.Message{
		Role: "assistant", Content: "好", DurationMs: 2000, FirstTokenMs: 400, UsageTokens: 100,
		InputTokens: 900, CacheReadTokens: 6000, CacheWriteTokens: 100,
	})
	appendMsg(llm.Message{Role: "tool", ToolCallID: "t1", Content: "ok", DurationMs: 500})
	// 第 2 轮：一次工具轮（没有首字——工具轮本来就没有"首字"这个时刻）+ 一次收尾轮
	appendMsg(llm.Message{Role: "user", Content: "再跑一次"})
	appendMsg(llm.Message{Role: "assistant", Content: "调工具", DurationMs: 1000, UsageTokens: 20, InputTokens: 50})
	appendMsg(llm.Message{Role: "tool", ToolCallID: "t2", Content: "ok", DurationMs: 1500})
	appendMsg(llm.Message{
		Role: "assistant", Content: "好了", DurationMs: 3000, FirstTokenMs: 500, UsageTokens: 200,
		InputTokens: 100, CacheReadTokens: 5000,
	})
	// 注入的提示条（不是用户说的话）：notice 位标记，轮数必须把它排除
	appendMsg(llm.Message{Role: "user", Content: "[重复调用提醒] 你在重复相同的调用", Notice: true})

	st, err := s.SessionStatsOf(id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Turns != 2 {
		t.Fatalf("轮数应为 2（提示条不算一轮）: %d", st.Turns)
	}
	if st.Steps != 3 {
		t.Fatalf("步数应为 3（三条 assistant）: %d", st.Steps)
	}
	if st.LLMMs != 6000 { // 2000 + 1000 + 3000
		t.Fatalf("模型时间不符: %d", st.LLMMs)
	}
	if st.ToolMs != 2000 { // 500 + 1500
		t.Fatalf("工具时间不符: %d", st.ToolMs)
	}
	if st.TTFTSteps != 2 || st.TTFTMs != 900 { // 400 + 500
		t.Fatalf("首字不符: steps=%d ms=%d", st.TTFTSteps, st.TTFTMs)
	}
	// 解码只算"同时有首字与用量"的步：第 1 步（2000−400=1600, 100 tokens）
	// 与第 3 步（3000−500=2500, 200 tokens）。工具轮没有首字，不计。
	if st.DecodeMs != 4100 || st.DecodeTokens != 300 {
		t.Fatalf("解码不符: ms=%d tokens=%d", st.DecodeMs, st.DecodeTokens)
	}
	if st.InputTokens != 1050 || st.CacheReadTokens != 11000 || st.CacheWriteTokens != 100 || st.OutputTokens != 320 {
		t.Fatalf("用量四桶不符: %+v", st)
	}
}

// TestSessionStatsLegacyUsage：**老口径的行**（本功能上线前由旧二进制落库的 assistant
// 行）的 token 是 provider 的 total_tokens（输入+输出），与今天的四桶不同——必须单独记账、
// 不混进输出桶。
//
// 为什么这条值钱：混进去的表现是"速度看着很漂亮但完全不对"（实测 687.8 tok/s，真值约 40），
// 而用户没有任何办法看出那个数字是两种口径的和。
//
// 怎么造出"旧二进制写的行"：**不能用 AppendMsg**（那是新代码，会写 usage_split=1），
// 直接插一行让 usage_split 取列默认值 0——旧二进制的 INSERT 里根本没有这一列，效果相同。
func TestSessionStatsLegacyUsage(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(id, llm.Message{Role: "user", Content: "问"}); err != nil {
		t.Fatal(err)
	}
	// 老口径的行：usage_tokens 是总量（输入+输出），输入侧三列与 usage_split 都是默认值 0
	if _, err := s.db.Exec(
		`INSERT INTO messages (session_id, seq, role, content, reasoning, reasoning_sig, tool_calls,
		                       tool_call_id, first_token_ms, duration_ms, model, usage_tokens)
		 VALUES (?, 2, 'assistant', '答', '', '', '[]', '', 276, 2454, '', 1498)`, id); err != nil {
		t.Fatal(err)
	}
	// 新口径的行：四桶齐全（AppendMsg 会写 usage_split=1）
	if _, err := s.AppendMsg(id, llm.Message{
		Role: "assistant", Content: "新答", DurationMs: 1000, FirstTokenMs: 200,
		UsageTokens: 40, InputTokens: 100, CacheReadTokens: 300,
	}); err != nil {
		t.Fatal(err)
	}

	st, err := s.SessionStatsOf(id)
	if err != nil {
		t.Fatal(err)
	}
	if st.LegacyTokens != 1498 {
		t.Fatalf("早期记录的 token 应单独记账: %+v", st)
	}
	// 输出桶只装新口径的那一条——把 1498 混进来会让速度虚高十倍
	if st.OutputTokens != 40 || st.InputTokens != 100 || st.CacheReadTokens != 300 {
		t.Fatalf("四桶只该装新口径的步: %+v", st)
	}
	// 速度的分子同样只算新口径的步（1498 不进 decode_tokens）
	if st.DecodeTokens != 40 {
		t.Fatalf("decode_tokens 只该算新口径的步: %+v", st)
	}
	// 耗时与首字与口径无关：早期行照样计入（那时也是如实测的）
	if st.LLMMs != 3454 || st.TTFTSteps != 2 {
		t.Fatalf("耗时/首字应照旧计入: %+v", st)
	}
}

// TestSessionStatsSurvivesCompaction：压缩把一段历史换成摘要检查点，但**统计不变**。
//
// 这是 DSH 那条性质的中文版："整段日志折叠出来的数字，翻页与压缩都改不了它"。
// 折叠读的是**全部消息行**（含被影子掉的那些）——读当前可见历史的话，用户一压缩就会
// 看到步数/token 突然变小，而那是个假事实（活没少干）。
func TestSessionStatsSurvivesCompaction(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.AppendMsg(id, llm.Message{Role: "user", Content: "问"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendMsg(id, llm.Message{
			Role: "assistant", Content: "答", DurationMs: 1000, FirstTokenMs: 200, UsageTokens: 10, InputTokens: 100,
		}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.SessionStatsOf(id)
	if err != nil {
		t.Fatal(err)
	}
	if before.Turns != 3 || before.Steps != 3 {
		t.Fatalf("压缩前统计不符: %+v", before)
	}
	// 把前 4 条（两轮）压成一条摘要检查点：历史回放只剩 3 条，但统计不该动
	if _, err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "<compacted-summary>\n摘要\n</compacted-summary>"}, 0, 4); err != nil {
		t.Fatal(err)
	}
	if msgs, err := s.Load(id); err != nil {
		t.Fatal(err)
	} else if len(msgs) != 3 {
		t.Fatalf("压缩后历史应只剩 3 条（1 摘要 + 1 轮）: %d", len(msgs))
	}
	after, err := s.SessionStatsOf(id)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("压缩不该改变整段统计: before=%+v after=%+v", before, after)
	}
}

// TestSessionStatsFollowsRewind：撤回真删了行 → 统计跟着变小（与压缩相反）。
func TestSessionStatsFollowsRewind(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	seq1, err := s.AppendMsg(id, llm.Message{Role: "user", Content: "第一轮"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(id, llm.Message{
		Role: "assistant", Content: "答一", DurationMs: 1000, FirstTokenMs: 100, UsageTokens: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(id, llm.Message{Role: "user", Content: "第二轮"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(id, llm.Message{
		Role: "assistant", Content: "答二", DurationMs: 1000, FirstTokenMs: 100, UsageTokens: 10,
	}); err != nil {
		t.Fatal(err)
	}
	before, err := s.SessionStatsOf(id)
	if err != nil {
		t.Fatal(err)
	}
	if before.Turns != 2 || before.Steps != 2 {
		t.Fatalf("撤回前统计不符: %+v", before)
	}
	// 撤回第一条用户消息：它及其之后的全部历史被真删掉
	if _, err := s.Rewind(id, seq1); err != nil {
		t.Fatal(err)
	}
	after, err := s.SessionStatsOf(id)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Empty() {
		t.Fatalf("撤回全部历史后统计应回到零值: %+v", after)
	}
}

// TestSessionStatsPersistsAcrossReopen：统计的折叠输入（用量四桶 + 工具耗时）必须真的落库。
//
// 不落库的表现是"重启后统计里的工具时间与缓存命中永远是 0"——那是最难发现的一类
// 静默降级（界面看着正常，数字悄悄变成假的）。
func TestSessionStatsPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(id, llm.Message{Role: "user", Content: "问"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(id, llm.Message{
		Role: "assistant", Content: "答", DurationMs: 1000, FirstTokenMs: 100, UsageTokens: 10,
		InputTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(id, llm.Message{Role: "tool", ToolCallID: "t1", Content: "ok", DurationMs: 70}); err != nil {
		t.Fatal(err)
	}
	want, err := s.SessionStatsOf(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// 重开同一个目录（模拟后端重启）
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.SessionStatsOf(id)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("重启后统计不一致: got=%+v want=%+v", got, want)
	}
	if got.ToolMs != 70 || got.CacheReadTokens != 30 || got.CacheWriteTokens != 40 || got.InputTokens != 20 {
		t.Fatalf("落库的字段没读回来: %+v", got)
	}
}

// TestSessionStatsMissingSession：会话不存在时报错（与其他按 id 读的方法同语义——
// 调用方据此区分"空会话"与"打错了 id"）。
func TestSessionStatsMissingSession(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.SessionStatsOf("没有这个会话"); err == nil {
		t.Fatal("不存在的会话应报错")
	}
}

// TestFoldSessionStatsIgnoresCheckpointSteps：检查点自己的 assistant 步不计入（它是摘要，
// 不是一次模型调用）；而它**影子掉的**那些步仍然计入（整段日志）。
func TestFoldSessionStatsIgnoresCheckpointSteps(t *testing.T) {
	rows := []rowData{
		{msg: llm.Message{Role: "user", Content: "问"}},
		{msg: llm.Message{Role: "assistant", Content: "答", DurationMs: 100}},
		// 检查点行：role 可能是 user（摘要正文包成 user 消息）
		{checkpoint: true, msg: llm.Message{Role: "user", Content: "摘要"}},
	}
	got := foldSessionStats(rows)
	if got.Turns != 1 || got.Steps != 1 || got.LLMMs != 100 {
		t.Fatalf("检查点不该计入: %+v", got)
	}
	// 工具消息的耗时只进 toolMs（不进 llmMs）
	got = foldSessionStats([]rowData{{msg: llm.Message{Role: "tool", DurationMs: 42}}})
	if got.ToolMs != 42 || got.LLMMs != 0 {
		t.Fatalf("工具耗时归属不符: %+v", got)
	}
	// 负耗时（坏数据）钳到 0，不许变成负数把总量吃掉
	got = foldSessionStats([]rowData{{msg: llm.Message{Role: "assistant", DurationMs: -5, FirstTokenMs: -1}}})
	if got.LLMMs != 0 || got.TTFTMs != 0 || got.DecodeMs != 0 {
		t.Fatalf("坏数据应钳到 0: %+v", got)
	}
}

// TestSessionStatsShapeMatchesWire：折叠产出与 wire 形态同字段（server 直接搬运）。
func TestSessionStatsShapeMatchesWire(t *testing.T) {
	var st sessiondata.SessionStats
	if !st.Empty() {
		t.Fatal("零值应判定为空")
	}
	st.Turns = 1
	if st.Empty() {
		t.Fatal("有值之后不该判空")
	}
}
