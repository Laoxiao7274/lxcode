// 会话统计的 wire 契约（chat.done / chat.history / chat.rewound）——**live 与 replay 必须
// 是同一份数字**，重启之后仍然给得出来，压缩改不了它、撤回会改它。
//
// 为什么要在 WS 这一层钉：会话统计由**服务端读库折叠**得出（agent 的内存历史只有当前
// 存活的那段，被压缩影子掉的消息不在里面），所以"前端拿到的数字"这件事只有走完整条链
// （store → server → wire）才测得出来。协议字段名漂移不会编译失败，只会静默变成一排 0。
package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// sendAndWaitDoneStats 发一条消息并等这一轮收尾，返回 chat.done 携带的会话统计。
func sendAndWaitDoneStats(t *testing.T, client *wsTestClient, sessionID, text string) protocol.SessionStats {
	t.Helper()
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: sessionID, Text: text})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send(%s) 失败: %+v", sessionID, resp)
	}
	ev := waitSessionEvent(t, client, protocol.EventDone, sessionID)
	var p protocol.DoneParams
	decodeServerResult(t, ev.Params, &p)
	if p.Stats == nil {
		t.Fatalf("主轮的 chat.done 应带 stats: %+v", p)
	}
	return *p.Stats
}

// TestSessionStatsOverWS：跑一轮 → chat.done 与 chat.history 给**同一份**统计 →
// 换一个服务端（同一份库 = 重启）→ 还是同一份数字。
func TestSessionStatsOverWS(t *testing.T) {
	srv, client, reg := newTestServer(t, usageStream(1111))
	id := createTestSession(t, client, "")

	live := sendAndWaitDoneStats(t, client, id, "你好")
	if live.Turns != 1 || live.Steps != 1 {
		t.Fatalf("一轮之后应是 1 轮 1 步: %+v", live)
	}
	if live.OutputTokens != 5 { // usageStream 每步报 5 个输出 token
		t.Fatalf("输出 token 应来自 provider 回报: %+v", live)
	}
	// 同进程回放：chat.history 与 chat.done 必须是同一份数字
	hist := readTestHistory(t, client, id).Stats
	if hist == nil || *hist != live {
		t.Fatalf("live 与 replay 必须是同一份统计: live=%+v replay=%+v", live, hist)
	}

	// 重启：同一个库上再起一个服务端（统计是读库折叠出来的，重启后仍在）
	_, client2 := newServerOnStore(t, srv.st, reg, nil)
	got := readTestHistory(t, client2, id).Stats
	if got == nil {
		t.Fatal("重启后 chat.history 的 stats 不该缺席")
	}
	if *got != live {
		t.Fatalf("重启后应是同一份数字: live=%+v replay=%+v", live, *got)
	}
}

// TestSessionStatsAbsentForEmptySession：还没有任何一步 → 整键缺席（前端不渲染统计胶囊，
// 不是显示一排 0）。
func TestSessionStatsAbsentForEmptySession(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	id := createTestSession(t, client, "")
	resp := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: id})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.history 失败: %+v", resp)
	}
	b, _ := json.Marshal(resp.Result)
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if v, hit := raw["stats"]; hit {
		t.Fatalf("空会话应整键缺席（不显示一排 0）: %v (%s)", v, b)
	}
}

// TestSessionStatsFollowsRewindOverWS：撤回真删了行 → chat.rewound 带**重算后**的统计
// （步数跟着变小）；全撤光时统计回到零值 → 整键缺席（前端不渲染胶囊）。
func TestSessionStatsFollowsRewindOverWS(t *testing.T) {
	_, client, _ := newTestServer(t, usageStream(1111, 1111))
	id := createTestSession(t, client, "")

	sendAndWaitDoneStats(t, client, id, "第一轮")
	second := sendAndWaitDoneStats(t, client, id, "第二轮")
	if second.Turns != 2 || second.Steps != 2 {
		t.Fatalf("两轮之后应是 2 轮 2 步: %+v", second)
	}

	// 撤回第一条用户消息（seq=1）：它及其之后的全部历史被真删掉
	resp := client.call(protocol.MethodChatRewind, protocol.ChatRewindParams{SessionID: id, Seq: 1})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.rewind 失败: %+v", resp)
	}
	ev := waitSessionEvent(t, client, protocol.EventRewound, id)
	var rp protocol.ChatRewoundParams
	decodeServerResult(t, ev.Params, &rp)
	// 全撤光之后统计是零值 → 整键缺席（前端不渲染胶囊，不显示一排 0）。
	// 零值**不是**"已知的 0"：它与"还没有任何一步"在展示上是同一件事。
	if rp.Stats != nil && *rp.Stats != (protocol.SessionStats{}) {
		t.Fatalf("撤回全部历史后统计应回到零值或整键缺席: %+v", *rp.Stats)
	}
}

// TestSessionStatsShrinksOnPartialRewind：撤回**一部分**时，重算后的统计必须真的变小
// （这是"撤回会改统计"与"压缩不改统计"的分界——只有真删行才会变）。
func TestSessionStatsShrinksOnPartialRewind(t *testing.T) {
	_, client, _ := newTestServer(t, usageStream(1111, 1111, 1111))
	id := createTestSession(t, client, "")
	sendAndWaitDoneStats(t, client, id, "第一轮")
	sendAndWaitDoneStats(t, client, id, "第二轮")
	third := sendAndWaitDoneStats(t, client, id, "第三轮")
	if third.Turns != 3 || third.Steps != 3 {
		t.Fatalf("三轮之后应是 3 轮 3 步: %+v", third)
	}
	// 撤回第三轮那条用户消息（seq=5）：只删掉第三轮 → 统计回到 2 轮 2 步
	resp := client.call(protocol.MethodChatRewind, protocol.ChatRewindParams{SessionID: id, Seq: 5})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.rewind 失败: %+v", resp)
	}
	ev := waitSessionEvent(t, client, protocol.EventRewound, id)
	var rp protocol.ChatRewoundParams
	decodeServerResult(t, ev.Params, &rp)
	if rp.Stats == nil {
		t.Fatal("部分撤回后统计不该缺席（还有两轮在）")
	}
	if rp.Stats.Turns != 2 || rp.Stats.Steps != 2 {
		t.Fatalf("撤回一轮后统计应变成 2 轮 2 步: %+v", *rp.Stats)
	}
	// 回放路径同口径
	if got := readTestHistory(t, client, id).Stats; got == nil || got.Turns != 2 {
		t.Fatalf("撤回后 chat.history 的统计该跟着变小: %+v", got)
	}
}

// TestChildSessionStatsOverWS：子会话有自己的统计，且**不写主会话的**。
func TestChildSessionStatsOverWS(t *testing.T) {
	srv, client, _ := newTestServer(t, usageStream(1111, 1111))
	mainID := createTestSession(t, client, "")
	mainLive := sendAndWaitDoneStats(t, client, mainID, "主会话的一轮")

	childID, err := srv.st.CreateChild(mainID, "coder", "d1")
	if err != nil {
		t.Fatal(err)
	}
	childLive := sendAndWaitDoneStats(t, client, childID, "子任务")

	// 子会话自己的统计（它自己的日志折叠出来的一份）
	if childLive.Turns != 1 || childLive.Steps != 1 {
		t.Fatalf("子会话统计不符: %+v", childLive)
	}
	// 主会话的统计没有被子会话的那一轮改动
	if got := readTestHistory(t, client, mainID).Stats; got == nil || *got != mainLive {
		t.Fatalf("子会话的轮次不该写主会话的统计: got=%+v want=%+v", got, mainLive)
	}
	// 子会话的历史带它自己的统计
	if got := readTestHistory(t, client, childID).Stats; got == nil || got.Turns != 1 {
		t.Fatalf("chat.history{子会话} 应给它自己的统计: %+v", got)
	}
}

// TestSessionStatsCountsInjectedNotice：注入的通告（后台任务唤醒）在库里带 notice 位——
// 会话统计的轮数按它排除（通告不是用户说的话），但它开的那一轮的模型调用仍计入步数。
func TestSessionStatsCountsInjectedNotice(t *testing.T) {
	srv, client, _ := newTestServer(t, usageStream(1111))
	id := createTestSession(t, client, "")

	// 直接往库里塞一条带 notice 位的 user 消息 + 一轮 assistant：
	// 走的是**同一条**落库路径（AppendMsg 的 notice 列），折叠读的就是这个位
	if _, err := srv.st.AppendMsg(id, llm.Message{Role: "user", Content: "[后台任务通告] 后台任务 dev 结束。", Notice: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.st.AppendMsg(id, llm.Message{
		Role: "assistant", Content: "好", DurationMs: 100, FirstTokenMs: 10, UsageTokens: 5,
	}); err != nil {
		t.Fatal(err)
	}

	got := readTestHistory(t, client, id).Stats
	if got == nil {
		t.Fatal("有历史就该给统计")
	}
	if got.Turns != 0 {
		t.Fatalf("通告不该算成一用户轮: %+v", got)
	}
	if got.Steps != 1 {
		t.Fatalf("模型调用仍应计入步数: %+v", got)
	}
}

// TestSessionStatsSurvivesCompactionOverWS：压缩把一段历史换成摘要检查点，但统计**不变**
// （DSH 那条性质：整段日志折叠出来的数字，压缩与翻页都改不了它）。
func TestSessionStatsSurvivesCompactionOverWS(t *testing.T) {
	srv, client, _ := newTestServer(t, usageStream(1111, 1111))
	id := createTestSession(t, client, "")

	sendAndWaitDoneStats(t, client, id, "第一轮")
	sendAndWaitDoneStats(t, client, id, "第二轮")
	mid := readTestHistory(t, client, id).Stats
	if mid == nil || mid.Turns != 2 {
		t.Fatalf("两轮之后统计不符: %+v", mid)
	}

	// 把前两条（第一轮）压成一条摘要检查点：历史回放变短，统计一个字都不该动。
	// 注意**断言走库**（srv.st.Load）：chat.history 回放的是会话运行时的**内存历史**，
	// 而检查点是直接落库的（真实链路上它由 agent 追加并同步进内存）。
	if _, err := srv.st.AppendCheckpoint(id, llm.Message{
		Role: "user", Content: "<compacted-summary>\n摘要\n</compacted-summary>",
	}, 0, 2); err != nil {
		t.Fatal(err)
	}
	surface, err := srv.st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	// 压缩真的发生了：库里回放出来的历史从 4 条变成 3 条（摘要 + 第二轮两条）
	if len(surface) != 3 {
		t.Fatalf("压缩后库里的历史应变短: %d", len(surface))
	}
	after := readTestHistory(t, client, id).Stats
	if after == nil || *after != *mid {
		t.Fatalf("压缩不该改变统计: before=%+v after=%+v", mid, after)
	}
}

// TestSessionStatsPresentForSessionsWithOnlyCheckpoint：只有检查点（没有 assistant 步）时
// 统计仍是零值 → 整键缺席（不显示"0 轮 0 步"）。
func TestSessionStatsAbsentWithOnlyCheckpoint(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	id := createTestSession(t, client, "")
	if _, err := srv.st.AppendCheckpoint(id, llm.Message{
		Role: "user", Content: "<compacted-summary>\n摘要\n</compacted-summary>",
	}, 0, 0); err != nil {
		t.Fatal(err)
	}
	resp := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: id})
	b, _ := json.Marshal(resp.Result)
	if strings.Contains(string(b), "\"stats\"") {
		t.Fatalf("只有检查点时应整键缺席: %s", b)
	}
}
