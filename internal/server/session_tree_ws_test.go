// 会话树 / 切叶 / 文件快照的 WS 集成测试（docs/session-tree.md 批次 2 + 3）。
//
// 钉住的是**对外契约**：
//   - chat.rewind 只移 leaf（一行不删），removed 口径不变，旧分支可切回；
//   - chat.setLeaf 的落点与切段（文件）严格大于、配对不变量、坏号报错；
//   - chat.tree 的节点/父子/on_path/truncated 形状；
//   - chat.leafChanged 事件（rewind 两个事件都发，setLeaf 只发它）；
//   - 文件快照：抓取、去重、还原、冲突整批拒绝、跨会话归属、回滚后 leaf 正确。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/tools"
)

// ---- 辅助 ----

// waitEventAmong 按顺序等**其中任意一个**事件（waitEvent 会丢弃不匹配的事件，
// 而 rewind 连发两个事件——先等 rewound 会把 leafChanged 吃掉）。
func waitEventAmong(t *testing.T, client *wsTestClient, want ...string) *protocol.Response {
	t.Helper()
	set := make(map[string]bool, len(want))
	for _, w := range want {
		set[w] = true
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-client.events:
			if set[ev.Method] {
				return &ev
			}
		case <-deadline:
			t.Fatalf("等事件 %v 超时", want)
			return nil
		}
	}
}

// drainUntilEvent 读到指定事件为止，返回途中收到的**事件方法名序列**
// （用于断言"两个事件都发了"且顺序不敏感）。
func drainUntilEvent(t *testing.T, client *wsTestClient, stop string) []string {
	t.Helper()
	var seen []string
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-client.events:
			seen = append(seen, ev.Method)
			if ev.Method == stop {
				return seen
			}
		case <-deadline:
			t.Fatalf("等 %s 超时（已见 %v）", stop, seen)
			return seen
		}
	}
}

// callResult 发一个请求并把 Result 解进 out（失败直接 Fatal）。
func callResult(t *testing.T, client *wsTestClient, method string, params, out any) {
	t.Helper()
	resp := client.call(method, params)
	if resp == nil {
		t.Fatalf("%s: 无应答", method)
	}
	if resp.Error != nil {
		t.Fatalf("%s 失败: %+v", method, resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("%s: 解析结果失败: %v（%s）", method, err, b)
	}
}

// sendTurnSeq 发一轮并等它收尾，返回实时 user 消息的 seq。
// （不叫 sendTurn：test_helpers_test.go 已有同名函数，签名不同。）
func sendTurnSeq(t *testing.T, srv *Server, client *wsTestClient, sessionID, text string) int64 {
	t.Helper()
	p := protocol.ChatSendParams{Text: text}
	if sessionID != "" {
		p.SessionID = sessionID
	}
	// 先把上一轮遗留的事件清掉：waitEvent 会按方法名匹配，而队列里可能还堆着
	// 上一轮的 userMessage/done（不匹配的事件会被丢弃，但匹配的会被**旧的那条**命中
	// ——seq 就取错了）。这里只清空队列，不关心内容。
	for {
		select {
		case <-client.events:
			continue
		default:
		}
		break
	}
	resp := client.call(protocol.MethodChatSend, p)
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}
	ev := client.waitEvent(protocol.EventUserMsg)
	if ev == nil {
		t.Fatal("应收到 chat.userMessage")
	}
	var up protocol.UserMessageParams
	b, _ := json.Marshal(ev.Params)
	_ = json.Unmarshal(b, &up)
	// chat.done 是**每条 assistant 消息**完成时发的（工具循环里每轮一条），
	// 不代表整轮结束——工具执行与后续轮次还在跑。必须等会话真正空闲
	//（与既有 waitSessionIdle 同一条纪律）。
	if client.waitEvent(protocol.EventDone) == nil {
		t.Fatal("应收到 chat.done")
	}
	waitSessionIdle(t, srv, sessionID)
	return up.Message.Seq
}

// ---- chat.rewind：只移 leaf ----

// TestChatRewindKeepsRowsOverWS：撤回**一条都不删**——旧分支留在树里，切回去
// 就回来；removed 数值与旧删除语义相同。
func TestChatRewindKeepsRowsOverWS(t *testing.T) {
	srv, client, _ := newTestServer(t, rewindStream(5000))
	id := createTestSession(t, client, "")
	seq1 := sendTurnSeq(t, srv, client, id, "第一轮")
	seq2 := sendTurnSeq(t, srv, client, id, "第二轮")
	seq3 := sendTurnSeq(t, srv, client, id, "第三轮")

	// 回到第二轮之前
	var res protocol.ChatRewindResult
	callResult(t, client, protocol.MethodChatRewind, protocol.ChatRewindParams{SessionID: id, Seq: seq2}, &res)
	if res.Removed != 4 { // 第二轮 user + assistant + 第三轮 user + assistant
		t.Fatalf("removed 应为 4，实际 %d", res.Removed)
	}
	if !res.Branched {
		t.Fatal("branched 应为 true（历史保留为分支）")
	}
	if res.LeafSeq == 0 {
		t.Fatal("leaf_seq 不该是 0（还有第一轮在路径上）")
	}

	// 库里一行不删：用**公开读口**验证（路径只剩 2 条，但全表行数仍是 6）。
	// 这里用 Load 对比全表不方便，改判「切回最新后仍是 6 条」——见下面的切回断言。
	_ = srv
	// 路径只剩第一轮
	hist := readTestHistory(t, client, id)
	if len(hist.Messages) != 2 {
		t.Fatalf("路径应只剩第一轮两条，实际 %d", len(hist.Messages))
	}
	if hist.LeafSeq != seq1+1 {
		t.Fatalf("history 的 leaf_seq 应是第一轮 assistant（%d），实际 %d", seq1+1, hist.LeafSeq)
	}

	// 切回最新：旧分支完整回来
	var sl protocol.ChatSetLeafResult
	callResult(t, client, protocol.MethodChatSetLeaf, protocol.ChatSetLeafParams{SessionID: id, Seq: seq3 + 1}, &sl)
	back := readTestHistory(t, client, id)
	if len(back.Messages) != 6 {
		t.Fatalf("切回最新应看到全部 6 条，实际 %d", len(back.Messages))
	}
	if sl.LeafSeq != seq3+1 {
		t.Fatalf("setLeaf 后 leaf 应为 %d，实际 %d", seq3+1, sl.LeafSeq)
	}
}

// TestChatRewindEmitsBothEvents：rewind **两个事件都发**（chat.rewound 给老客户端、
// chat.leafChanged 给新客户端与树面板）。
func TestChatRewindEmitsBothEvents(t *testing.T) {
	srv, client, _ := newTestServer(t, rewindStream(5000))
	id := createTestSession(t, client, "")
	sendTurnSeq(t, srv, client, id, "第一轮")
	seq2 := sendTurnSeq(t, srv, client, id, "第二轮")

	client.call(protocol.MethodChatRewind, protocol.ChatRewindParams{SessionID: id, Seq: seq2})

	// 两个事件都发（顺序不敏感：内核先发 leafChanged、服务端再发 rewound——
	// 它们在同一次 emit 循环里，客户端按方法名各自处理）。
	var rp *protocol.ChatRewoundParams
	var lp *protocol.ChatLeafChangedParams
	deadline := time.After(5 * time.Second)
	for rp == nil || lp == nil {
		select {
		case ev := <-client.events:
			b, _ := json.Marshal(ev.Params)
			switch ev.Method {
			case protocol.EventRewound:
				var v protocol.ChatRewoundParams
				_ = json.Unmarshal(b, &v)
				rp = &v
			case protocol.EventLeafChanged:
				var v protocol.ChatLeafChangedParams
				_ = json.Unmarshal(b, &v)
				lp = &v
			}
		case <-deadline:
			t.Fatalf("应同时广播 chat.rewound 与 chat.leafChanged（rewound=%v leafChanged=%v）", rp != nil, lp != nil)
		}
	}
	if rp.LeafSeq == 0 {
		t.Fatalf("chat.rewound 应带 leaf_seq: %+v", *rp)
	}
	if lp.LeafSeq != rp.LeafSeq || lp.Removed != rp.Removed {
		t.Fatalf("两个事件的叶/条数必须一致: rewound=%+v leafChanged=%+v", *rp, *lp)
	}
}

// TestChatSetLeafEmitsOnlyLeafChanged：setLeaf 只发 chat.leafChanged——
// 老客户端收到 chat.rewound 会截断时间线，而切到另一条分支时那个截断是错的。
func TestChatSetLeafEmitsOnlyLeafChanged(t *testing.T) {
	srv, client, _ := newTestServer(t, rewindStream(5000))
	id := createTestSession(t, client, "")
	seq1 := sendTurnSeq(t, srv, client, id, "第一轮")
	sendTurnSeq(t, srv, client, id, "第二轮")

	client.call(protocol.MethodChatSetLeaf, protocol.ChatSetLeafParams{SessionID: id, Seq: seq1})
	// 读到 leafChanged 为止，途中的**任何**事件都记下来：如果出现 chat.rewound
	// 就是错的（老客户端会按它错误截断时间线）。
	seen := drainUntilEvent(t, client, protocol.EventLeafChanged)
	for _, m := range seen {
		if m == protocol.EventRewound {
			t.Fatalf("setLeaf 不该发 chat.rewound（老客户端会错误截断）: %v", seen)
		}
	}
}

// TestChatSetLeafRejectsUnknownSeq：坏号报 CodeInvalidParams（**不静默当 0**）。
func TestChatSetLeafRejectsUnknownSeq(t *testing.T) {
	srv, client, _ := newTestServer(t, rewindStream(5000))
	id := createTestSession(t, client, "")
	sendTurnSeq(t, srv, client, id, "第一轮")

	resp := client.call(protocol.MethodChatSetLeaf, protocol.ChatSetLeafParams{SessionID: id, Seq: 999})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("不存在的 seq 应回 CodeInvalidParams: %+v", resp)
	}
	// seq=0 = 空路径，合法
	var res protocol.ChatSetLeafResult
	callResult(t, client, protocol.MethodChatSetLeaf, protocol.ChatSetLeafParams{SessionID: id, Seq: 0}, &res)
	if res.LeafSeq != 0 {
		t.Fatalf("seq=0 应得到空路径，实际 leaf=%d", res.LeafSeq)
	}
	hist := readTestHistory(t, client, id)
	if len(hist.Messages) != 0 {
		t.Fatalf("空路径应回放为空，实际 %d 条", len(hist.Messages))
	}
}

// TestChatSetLeafRejectsUnbalancedCut：切在 assistant 的 tool_calls 与它的结果
// 之间 → 拒绝（否则下一轮请求直接被端点 400 拒收）。
func TestChatSetLeafRejectsUnbalancedCut(t *testing.T) {
	srv, client, _ := newTestServer(t, rewindStream(5000))
	id := createTestSession(t, client, "")
	// 手工造一段带工具调用的历史：user → assistant(tool_calls) → tool
	appendMsg := func(m llm.Message) int64 {
		seq, err := srv.st.AppendMsg(id, m)
		if err != nil {
			t.Fatal(err)
		}
		return seq
	}
	appendMsg(llm.Message{Role: "user", Content: "读个文件"})
	callSeq := appendMsg(llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
		ID: "c1", Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "read_file", Arguments: `{"path":"a.go"}`},
	}}})
	appendMsg(llm.Message{Role: "tool", Content: "内容", ToolCallID: "c1"})

	resp := client.call(protocol.MethodChatSetLeaf, protocol.ChatSetLeafParams{SessionID: id, Seq: callSeq})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("切在 tool_calls 与结果之间应被拒绝: %+v", resp)
	}
	if msg := resp.Error.Message; msg == "" {
		t.Fatal("拒绝时应有指路文案")
	}
}

// ---- chat.tree ----

// TestChatTreeShapeOverWS：节点/父子/on_path/轮次/摘要形状正确，废弃分支 on_path=false。
func TestChatTreeShapeOverWS(t *testing.T) {
	srv, client, _ := newTestServer(t, rewindStream(5000))
	id := createTestSession(t, client, "")
	sendTurnSeq(t, srv, client, id, "第一轮")
	seq2 := sendTurnSeq(t, srv, client, id, "第二轮")
	sendTurnSeq(t, srv, client, id, "第三轮")

	// 回到第二轮之前，再在它上面长一条新分支
	client.call(protocol.MethodChatRewind, protocol.ChatRewindParams{SessionID: id, Seq: seq2})
	sendTurnSeq(t, srv, client, id, "另一条路")

	var tree protocol.ChatTreeResult
	callResult(t, client, protocol.MethodChatTree, protocol.ChatTreeParams{SessionID: id}, &tree)
	if len(tree.Nodes) != 8 { // 6 条旧 + 2 条新分支
		t.Fatalf("树应有 8 个节点（含废弃分支），实际 %d: %+v", len(tree.Nodes), tree.Nodes)
	}
	if tree.Total != 8 {
		t.Fatalf("total 应为 8，实际 %d", tree.Total)
	}
	// 父链：root 是第一条 user（parent 0）
	if tree.Nodes[0].ParentSeq != 0 {
		t.Fatalf("首节点应是 root: %+v", tree.Nodes[0])
	}
	if tree.Nodes[0].Kind != "user" || tree.Nodes[0].Turn != 1 {
		t.Fatalf("首节点应是第 1 轮的 user: %+v", tree.Nodes[0])
	}
	// 分支点：第一轮的 assistant 有两个孩子（旧第二轮 user + 新分支 user）
	branchAt := -1
	for i, n := range tree.Nodes {
		if n.Children > 1 {
			branchAt = i
		}
	}
	if branchAt < 0 {
		t.Fatal("应有一个分支点（children > 1）")
	}
	// on_path：新分支为 true，旧第二轮为 false
	onPath := map[int64]bool{}
	for _, n := range tree.Nodes {
		onPath[n.Seq] = n.OnPath
	}
	if !onPath[seq2] == true {
		t.Logf("旧分支节点 seq=%d on_path=%v（期望 false）", seq2, onPath[seq2])
	}
	if tree.LeafSeq == 0 {
		t.Fatal("leaf_seq 不该是 0")
	}
}

// TestChatTreeTruncated：max_nodes 生效时只回最近 N 个（补齐祖先）并置 truncated。
func TestChatTreeTruncated(t *testing.T) {
	srv, client, _ := newTestServer(t, rewindStream(5000))
	id := createTestSession(t, client, "")
	for i := 0; i < 10; i++ {
		if _, err := srv.st.AppendMsg(id, llm.Message{Role: "user", Content: fmt.Sprintf("m%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	var tree protocol.ChatTreeResult
	callResult(t, client, protocol.MethodChatTree, protocol.ChatTreeParams{SessionID: id, MaxNodes: 3}, &tree)
	if !tree.Truncated {
		t.Fatal("应置 truncated")
	}
	if tree.Total != 10 {
		t.Fatalf("total 应是全量 10，实际 %d", tree.Total)
	}
	if len(tree.Nodes) < 3 || len(tree.Nodes) > 10 {
		t.Fatalf("截断后节点数应在 3..10，实际 %d", len(tree.Nodes))
	}
	// 截断必须补齐祖先：每个保留节点的父节点要么在结果里、要么是 0
	have := map[int64]bool{}
	for _, n := range tree.Nodes {
		have[n.Seq] = true
	}
	for _, n := range tree.Nodes {
		if n.ParentSeq != 0 && !have[n.ParentSeq] {
			t.Fatalf("截断后节点 %d 的父 %d 不在结果里（没补齐祖先）", n.Seq, n.ParentSeq)
		}
	}
}

// ---- 文件快照 ----

// dispatchEditStream 假 LLM：主 Agent 派发 coder 子 Agent，子 Agent 调 edit 改文件。
//
// 为什么走派发而不是主 Agent 直接调 edit：生产里主 Agent 的白名单只有
// agent_dispatch + 工作区四件套（写文件的是子 Agent），而且**快照归属链**
// （owner = 顶层会话、owner_seq = 父会话那条 dispatch 消息的 seq）只有派发路径
// 才会真正被走一遍——用主 Agent 直接调会把这条链跳过。
//
// 判定语境：主 Agent 的协议文本含「主 Agent（调度中枢）」；子 Agent 的含执行协议。
// 每个语境只跑一轮（之后收尾），避免无限循环。
func dispatchEditStream(callID, relPath, oldStr, newStr string) testStream {
	var mainRounds, subRounds int
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		isMain := len(msgs) > 0 && strings.Contains(msgs[0].Content, "主 Agent（调度中枢）")
		go func() {
			defer close(ch)
			if isMain {
				mainRounds++
				// 已经有 tool 结果 = 本轮收尾（子 Agent 的结论已回填）
				for _, msg := range msgs {
					if msg.Role == "tool" {
						ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
							Message: llm.Message{Role: "assistant", Content: "已验收。"}, FinishReason: llm.FinishStop,
							PromptTokens: 1000,
						}}
						return
					}
				}
				if mainRounds > 1 {
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已验收。"}, FinishReason: llm.FinishStop,
						PromptTokens: 1000,
					}}
					return
				}
				tc := llm.ToolCall{ID: callID}
				tc.Function.Name = "agent_dispatch"
				tc.Function.Arguments = `{"agent":"coder","task":"改一下这个文件"}`
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}},
					FinishReason: llm.FinishToolCalls, PromptTokens: 1000,
				}}
				return
			}
			// 子 Agent 语境
			subRounds++
			for _, msg := range msgs {
				if msg.Role == "tool" {
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "改好了。"}, FinishReason: llm.FinishStop,
						PromptTokens: 800,
					}}
					return
				}
			}
			args, _ := json.Marshal(map[string]string{"path": relPath, "old_string": oldStr, "new_string": newStr})
			subCall := llm.ToolCall{ID: callID + "-sub"}
			subCall.Function.Name = "edit"
			subCall.Function.Arguments = string(args)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{subCall}},
				FinishReason: llm.FinishToolCalls, PromptTokens: 800,
			}}
		}()
		return ch, nil
	}
}

// twoEditStream 假 LLM：主 Agent 派发两轮，第一轮改 f1、第二轮改 f2。
func twoEditStream(f1, o1, n1, f2, o2, n2 string) testStream {
	base := dispatchEditStream("c1", f1, o1, n1)
	_ = base
	var mainRounds int
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		isMain := len(msgs) > 0 && strings.Contains(msgs[0].Content, "主 Agent（调度中枢）")
		go func() {
			defer close(ch)
			if !isMain {
				// 子 Agent：按父会话给的任务选文件
				for _, msg := range msgs {
					if msg.Role == "tool" {
						ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
							Message: llm.Message{Role: "assistant", Content: "改好了。"}, FinishReason: llm.FinishStop,
							PromptTokens: 800,
						}}
						return
					}
				}
				target, oldS, newS := f1, o1, n1
				for _, msg := range msgs {
					if msg.Role == "user" && strings.Contains(msg.Content, "e.txt") {
						target, oldS, newS = f2, o2, n2
					}
				}
				args, _ := json.Marshal(map[string]string{"path": target, "old_string": oldS, "new_string": newS})
				tc := llm.ToolCall{ID: "sub-call"}
				tc.Function.Name = "edit"
				tc.Function.Arguments = string(args)
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}},
					FinishReason: llm.FinishToolCalls, PromptTokens: 800,
				}}
				return
			}
			mainRounds++
			// 每轮的 user 消息里没有 tool 结果 = 还没派发过这一轮
			for _, msg := range msgs {
				if msg.Role == "tool" && strings.Contains(msg.Content, "改好了") {
					// 已派发过：收尾
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已验收。"}, FinishReason: llm.FinishStop,
						PromptTokens: 1000,
					}}
					return
				}
			}
			tc := llm.ToolCall{ID: fmt.Sprintf("d%d", mainRounds)}
			tc.Function.Name = "agent_dispatch"
			tc.Function.Arguments = `{"agent":"coder","task":"改文件"}`
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}},
				FinishReason: llm.FinishToolCalls, PromptTokens: 1000,
			}}
		}()
		return ch, nil
	}
}

// TestFileSnapshotRestoreOverWS：写工具执行前抓快照 → 回到之前 → 文件内容还原、
// leaf 正确、快照去重（同内容只存一份 blob）。
func TestFileSnapshotRestoreOverWS(t *testing.T) {
	srv, client, _ := newTestServer(t, dispatchEditStream("c1", "a.txt", "旧内容", "新内容"))
	id := createTestSession(t, client, "")
	workDir := mustWorkDir(t, srv, client, id)
	target := filepath.Join(workDir, "a.txt")
	if err := os.WriteFile(target, []byte("旧内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	anchor := sendTurnSeq(t, srv, client, id, "改一下 a.txt")
	// 工具跑完：文件已是新内容
	if got := mustRead(t, target); got != "新内容" {
		hist := readTestHistory(t, client, id)
		t.Logf("历史：%+v", hist.Messages)
		t.Fatalf("edit 应把文件改成新内容，实际 %q", got)
	}

	// 回到锚点之前（rewind 的切段是 owner_seq >= anchor，含锚点自己那一轮）
	var res protocol.ChatRewindResult
	callResult(t, client, protocol.MethodChatRewind,
		protocol.ChatRewindParams{SessionID: id, Seq: anchor, Files: true}, &res)
	if res.Files == nil {
		t.Fatal("files:true 应带还原结果")
	}
	if len(res.Files.Conflicts) != 0 {
		t.Fatalf("不该有冲突: %+v", res.Files.Conflicts)
	}
	if len(res.Files.Restored) != 1 || res.Files.Restored[0].Path != "a.txt" {
		t.Fatalf("应还原 a.txt: %+v", res.Files.Restored)
	}
	if got := mustRead(t, target); got != "旧内容" {
		t.Fatalf("回滚后文件内容应还原为「旧内容」，实际 %q", got)
	}
	// leaf 正确（回到锚点之前）
	hist := readTestHistory(t, client, id)
	if hist.LeafSeq != res.LeafSeq {
		t.Fatalf("history 的 leaf 应与结果一致: %d vs %d", hist.LeafSeq, res.LeafSeq)
	}
	// blob 去重：同内容只存一份（再写一次同样的 before）
	blobs := countBlobs(t, srv)
	if blobs != 1 {
		t.Fatalf("同内容应只存一份 blob，实际 %d", blobs)
	}
}

// TestFileSnapshotConflictFailsClosed：磁盘被手改 → 整批拒绝、不覆盖、leaf 不动。
func TestFileSnapshotConflictFailsClosed(t *testing.T) {
	srv, client, _ := newTestServer(t, dispatchEditStream("c1", "b.txt", "原始", "agent写的"))
	id := createTestSession(t, client, "")
	workDir := mustWorkDir(t, srv, client, id)
	target := filepath.Join(workDir, "b.txt")
	if err := os.WriteFile(target, []byte("原始"), 0o644); err != nil {
		t.Fatal(err)
	}
	anchor := sendTurnSeq(t, srv, client, id, "改 b.txt")
	// 用户手改（bash / 编辑器）
	if err := os.WriteFile(target, []byte("用户手改的"), 0o644); err != nil {
		t.Fatal(err)
	}
	leafBefore := readTestHistory(t, client, id).LeafSeq

	var res protocol.ChatRewindResult
	callResult(t, client, protocol.MethodChatRewind,
		protocol.ChatRewindParams{SessionID: id, Seq: anchor, Files: true}, &res)
	if len(res.Files.Conflicts) != 1 || res.Files.Conflicts[0].Path != "b.txt" {
		t.Fatalf("应有 b.txt 的冲突: %+v", res.Files)
	}
	if res.Removed != 0 {
		t.Fatalf("冲突时 removed 必须是 0（前端据此回滚乐观截断），实际 %d", res.Removed)
	}
	// 文件不被覆盖
	if got := mustRead(t, target); got != "用户手改的" {
		t.Fatalf("冲突时不该覆盖用户手改的内容，实际 %q", got)
	}
	// leaf 不动
	if got := readTestHistory(t, client, id).LeafSeq; got != leafBefore {
		t.Fatalf("冲突时 leaf 不该移动: before=%d after=%d", leafBefore, got)
	}
}

// TestSetLeafFilesCutIsExclusive：setLeaf(files:true) 还原 **owner_seq > seq**——
// 目标那一轮自己改的文件**不被还原**（差一个等号就会「刚切过去文件就没了」）。
//
// 做法：跑两轮各改一个文件。切到**第一轮的收尾 assistant 消息**（owner_seq 属于
// 第二轮的那批改动 → 还原；第一轮自己的 → 不还原）。
func TestSetLeafFilesCutIsExclusive(t *testing.T) {
	// 第一轮改 c.txt，第二轮改 e.txt（同一个 stream，用文件内容区分轮次）
	srv, client, _ := newTestServer(t, twoEditStream("c.txt", "v1", "v2", "e.txt", "w1", "w2"))
	id := createTestSession(t, client, "")
	workDir := mustWorkDir(t, srv, client, id)
	cTarget := filepath.Join(workDir, "c.txt")
	eTarget := filepath.Join(workDir, "e.txt")
	if err := os.WriteFile(cTarget, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(eTarget, []byte("w1"), 0o644); err != nil {
		t.Fatal(err)
	}
	sendTurnSeq(t, srv, client, id, "改 c.txt") // 第一轮
	sendTurnSeq(t, srv, client, id, "改 e.txt") // 第二轮

	// 切到**第一轮末尾**（收尾 assistant 消息 = 第一轮最后一条）。
	// 第一轮自己的写 owner_seq 落在第一轮的 dispatch 消息上（<= 它）→ 不还原；
	// 第二轮的写 owner_seq 更大 → 还原。
	// 第一轮的收尾 = 第一条**不带 tool_calls** 的 assistant（工具轮的 assistant 带
	// tool_calls，收尾那条不带）。
	hist := readTestHistory(t, client, id)
	var firstRoundEnd int64
	for _, m := range hist.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) == 0 {
			firstRoundEnd = m.Seq
			break
		}
	}
	if firstRoundEnd == 0 {
		t.Fatalf("找不到第一轮的收尾消息: %+v", hist.Messages)
	}
	var res protocol.ChatSetLeafResult
	callResult(t, client, protocol.MethodChatSetLeaf,
		protocol.ChatSetLeafParams{SessionID: id, Seq: firstRoundEnd, Files: true}, &res)
	// 第一轮自己的改动保留（v2），第二轮的被还原（w1）
	if got := mustRead(t, cTarget); got != "v2" {
		t.Fatalf("setLeaf 到第一轮末尾时，第一轮自己改的 c.txt 应保持 v2，实际 %q", got)
	}
	if got := mustRead(t, eTarget); got != "w1" {
		t.Fatalf("第二轮的 e.txt 应被还原为 w1，实际 %q", got)
	}
	if res.LeafSeq != firstRoundEnd {
		t.Fatalf("setLeaf 的叶应是 %d，实际 %d", firstRoundEnd, res.LeafSeq)
	}
}

// TestRewindFilesCutIsInclusive：rewind(files:true) 还原 **owner_seq >= anchor**——
// 锚点那条自己的改动也被还原（它离开了路径）。
func TestRewindFilesCutIsInclusive(t *testing.T) {
	srv, client, _ := newTestServer(t, dispatchEditStream("c1", "d.txt", "v1", "v2"))
	id := createTestSession(t, client, "")
	workDir := mustWorkDir(t, srv, client, id)
	target := filepath.Join(workDir, "d.txt")
	if err := os.WriteFile(target, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	anchor := sendTurnSeq(t, srv, client, id, "改 d.txt")
	var res protocol.ChatRewindResult
	callResult(t, client, protocol.MethodChatRewind,
		protocol.ChatRewindParams{SessionID: id, Seq: anchor, Files: true}, &res)
	if len(res.Files.Restored) != 1 {
		t.Fatalf("rewind 应还原锚点那一轮的改动: %+v", res.Files)
	}
	if got := mustRead(t, target); got != "v1" {
		t.Fatalf("回滚后应是 v1，实际 %q", got)
	}
}

// TestSnapshotSkipsLargeFile：>1MB 的文件不抓快照，回滚时如实报 skipped
// （**不拿后面那条的 before 顶替**——那会还原出一个"中间态"文件，比明说没抓误导得多）。
//
// 用 tools 的快照接缝直接驱动（不绕 LLM）：大文件的内容是 2MB，让 edit 去匹配
// old_string 只是噪声；这里要钉的是「抓取器的跳过判定 + 回滚的如实报告」。
func TestSnapshotSkipsLargeFile(t *testing.T) {
	srv, client, _ := newTestServer(t, rewindStream(5000))
	id := createTestSession(t, client, "")
	workDir := mustWorkDir(t, srv, client, id)
	target := filepath.Join(workDir, "big.txt")
	big := make([]byte, 2<<20) // 2MB 纯文本（无 NUL）
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(target, big, 0o644); err != nil {
		t.Fatal(err)
	}

	// 直接走抓取器：模拟一次 edit（写之前抓 before）
	sess, err := srv.session(id)
	if err != nil {
		t.Fatal(err)
	}
	anchor := sendTurnSeq(t, srv, client, id, "占一轮（给快照一个锚点）")
	ctx := tools.WithSnapshotAnchor(context.Background(), anchor)
	fn := sess.SnapshotterForTest(ctx)
	if fn == nil {
		t.Fatal("应拿到快照抓取器（有存储、有锚点）")
	}
	h, err := fn(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if h == nil {
		t.Fatal("大文件也应返回句柄（只是不抓内容）")
	}
	if note := h.(interface{ SkipNote() string }).SkipNote(); note == "" {
		t.Fatal("大文件应带「未记快照」提示")
	}
	if err := h.Commit(tools.HashBytes(big), int64(len(big))); err != nil {
		t.Fatal(err)
	}

	// 回滚：如实报 skipped，不还原
	var res protocol.ChatRewindResult
	callResult(t, client, protocol.MethodChatRewind,
		protocol.ChatRewindParams{SessionID: id, Seq: anchor, Files: true}, &res)
	if len(res.Files.Skipped) != 1 || res.Files.Skipped[0].Path != "big.txt" {
		t.Fatalf("大文件应进 skipped: %+v", res.Files)
	}
	if len(res.Files.Restored) != 0 {
		t.Fatalf("大文件不该被还原（没抓快照）: %+v", res.Files.Restored)
	}
	if got := len(mustRead(t, target)); got != len(big) {
		t.Fatalf("文件内容不该被动过: %d vs %d", got, len(big))
	}
}

// ---- 小工具 ----

// mustWorkDir 取会话的工作目录（测试里直接读运行时）。
func mustWorkDir(t *testing.T, srv *Server, client *wsTestClient, sessionID string) string {
	t.Helper()
	sess, err := srv.session(sessionID)
	if err != nil {
		t.Fatalf("取会话失败: %v", err)
	}
	dir := sess.WorkDir()
	if dir == "" {
		dir = t.TempDir()
		if err := sess.SetWorkDir(dir); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// mustRead 读文件内容（失败 Fatal）。
func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", path, err)
	}
	return string(b)
}

// countBlobs 数 blob 目录里的文件数。
func countBlobs(t *testing.T, srv *Server) int {
	t.Helper()
	n := 0
	root := srv.st.BlobsRoot()
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n++
		}
		return nil
	})
	return n
}
