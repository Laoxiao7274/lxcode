// 会话统计在**内核侧**的两条接线（2026-09-30，对齐 DSH 的 sessionStats + tokenUsage）：
//
//   - 用量四桶 + 工具耗时必须**真的落到消息上**（会话统计的折叠输入就是消息上的
//     这几个字段）——漏盖一桶的表现是"统计里那一栏永远是 0"，而界面看着一切正常；
//   - 注入的提示条（后台任务通告 / 重复调用提醒）**不算用户轮**（notice 位），
//     否则每投递一次通告就凭空多一轮。
//
// 折叠本身（整段日志、压缩不改、撤回改）在 internal/store/stats_test.go 里钉住；
// 这里只钉"内核把该写的写进消息了"这一层。
package agent

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/store"
)

// textResultWithBuckets 造一段带完整用量分桶的假流（provider 回报的四桶）。
func textResultWithBuckets(content string, in, cacheRead, cacheWrite, output int) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.EventText, TextDelta: content},
		{Type: llm.EventDone, Result: &llm.ChatResult{
			Message:     llm.Message{Role: "assistant", Content: content},
			UsageTokens: output, PromptTokens: in + cacheRead + cacheWrite,
			InputTokens: in, CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
			FinishReason: llm.FinishStop,
		}},
	}
}

// TestRoundStampsUsageBucketsOnMessage：一轮跑完，assistant 消息上带着 provider 回报的
// 四桶与计时；会话统计读到的就是这几个数。
func TestRoundStampsUsageBucketsOnMessage(t *testing.T) {
	dir := t.TempDir()
	s, st := newUsageEnv(t, dir)
	s.SetStream((&fakeStream{script: [][]llm.StreamEvent{
		textResultWithBuckets("好", 200, 700, 100, 30),
	}}).stream)
	if err := s.Send("你好"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	// 消息上的四桶（落库的那一份）
	msgs, err := st.Load(s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	var assistant *llm.Message
	for i := range msgs {
		if msgs[i].Role == "assistant" {
			assistant = &msgs[i]
		}
	}
	if assistant == nil {
		t.Fatal("历史里应有 assistant 消息")
	}
	if assistant.UsageTokens != 30 || assistant.InputTokens != 200 ||
		assistant.CacheReadTokens != 700 || assistant.CacheWriteTokens != 100 {
		t.Fatalf("四桶没落到消息上: %+v", assistant)
	}
	if assistant.DurationMs <= 0 {
		t.Fatalf("本轮耗时应落库: %+v", assistant)
	}

	// 会话统计（server 直接读它上 wire）
	stats, err := st.SessionStatsOf(s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Turns != 1 || stats.Steps != 1 {
		t.Fatalf("轮/步数不符: %+v", stats)
	}
	if stats.InputTokens != 200 || stats.CacheReadTokens != 700 ||
		stats.CacheWriteTokens != 100 || stats.OutputTokens != 30 {
		t.Fatalf("统计四桶不符: %+v", stats)
	}
	if stats.LLMMs <= 0 {
		t.Fatalf("模型时间应非零: %+v", stats)
	}
}

// TestToolMessageCarriesDuration：工具结果消息上带执行耗时（统计的「工具时间」按它折叠）。
//
// 被拒绝/取消而**没执行**的调用没有这个值（0 = 未知，不计入）——把"没跑"算成
// "0ms"会让工具时间的均值失真。
func TestToolMessageCarriesDuration(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("read_file", `{"path":"no-such-file.txt"}`),
		textResult("文件不存在，我换个方法"),
	}}
	s.stream = fake.stream

	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	if err := s.Send("读个不存在的文件"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	msgs, err := st.Load(s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	var tool *llm.Message
	for i := range msgs {
		if msgs[i].Role == "tool" {
			tool = &msgs[i]
		}
	}
	if tool == nil {
		t.Fatal("历史里应有 tool 结果消息")
	}
	if tool.DurationMs <= 0 {
		t.Fatalf("工具消息应带执行耗时: %+v", tool)
	}

	stats, err := st.SessionStatsOf(s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolMs <= 0 {
		t.Fatalf("统计的工具时间应非零: %+v", stats)
	}
	if stats.Turns != 1 {
		t.Fatalf("轮数应为 1: %+v", stats)
	}
}

// TestNoticeInjectionIsNotATurn：后台任务通告（Notify）注入的 user 消息**不算一轮**。
//
// 判定按消息上的 notice 位（不是按文本前缀）：两个前缀常量分别在 agent 与 protocol
// 包里，而折叠统计的 store 谁都不能 import（分层规则）——所以判定记在写边界。
func TestNoticeInjectionIsNotATurn(t *testing.T) {
	dir := t.TempDir()
	s, st := newUsageEnv(t, dir)
	s.SetStream((&fakeStream{script: [][]llm.StreamEvent{
		textResultWithBuckets("好", 0, 0, 0, 5),
	}}).stream)

	// 空闲时投递通告：它开一轮（那条 user 消息带 notice 位）
	if err := s.Notify("[后台任务通告] 后台任务 dev 结束（退出码 0）。"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	stats, err := st.SessionStatsOf(s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Turns != 0 {
		t.Fatalf("通告不该算成一用户轮: %+v", stats)
	}
	if stats.Steps != 1 {
		t.Fatalf("模型调用仍应计入步数: %+v", stats)
	}
}

// TestNoticeFlagSetOnInjectedMessages：注入消息在库里带 notice 位（前端按前缀渲染提示条，
// 后端这个位是给统计用的——两边判的是同一件事）。
func TestNoticeFlagSetOnInjectedMessages(t *testing.T) {
	dir := t.TempDir()
	s, st := newUsageEnv(t, dir)
	s.SetStream((&fakeStream{script: [][]llm.StreamEvent{
		textResultWithBuckets("好", 0, 0, 0, 5),
	}}).stream)
	if err := s.Notify("[后台任务通告] 后台任务 dev 结束。"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	msgs, err := st.Load(s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range msgs {
		if m.Role == "user" && m.Notice {
			found = true
		}
	}
	if !found {
		t.Fatalf("注入的通告应带 notice 位: %+v", msgs)
	}
}

// TestEstimateSplitsToolsFromSystem：工具声明单列一类（"工具占了窗口多少"是用户最想
// 知道的一件事——DSH 的 ContextMeter 同样单列）。
func TestEstimateSplitsToolsFromSystem(t *testing.T) {
	toolsWire := []llm.Tool{{Name: "read_file", Description: "读文件", Parameters: []byte(`{"type":"object"}`)}}
	u := estimateContextUsage("系统提示词内容", toolsWire, nil)
	if u.Tools <= 0 {
		t.Fatalf("工具声明应单列一类: %+v", u)
	}
	// 系统提示词那一类不该再把工具声明算进去（否则两类恒相等，单列就没意义）
	only := estimateContextUsage("系统提示词内容", nil, nil)
	if u.System != only.System {
		t.Fatalf("工具声明应从 system 里拆出来: with=%d without=%d", u.System, only.System)
	}
	if usageTotal(u) != u.System+u.Tools+u.ToolResults+u.Messages+u.Reasoning {
		t.Fatalf("分类之和应含 Tools: %+v", u)
	}
	// 归一到真实用量时五个分类一起缩放，之和仍恒等于 Used
	got := anchoredUsage(u, 5000)
	if usageTotal(got) != 5000 || got.Tools <= 0 {
		t.Fatalf("归一时 Tools 不该丢: %+v", got)
	}
}
