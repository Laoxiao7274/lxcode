// rewind_test.go —— 会话回退（撤回）在会话层的契约：内存历史与库同口径、配对不变量、
// 忙时拒绝、上下文占用重算、幂等。
//
// 这些测试刻意都挂在**真实 store** 上（newPersistSession）：撤回的价值一半在"库里也删了"，
// 只在内存里截一刀的实现在纯内存会话上看着完全正常。
package agent

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// seedHistory 往会话里追加 n 条可辨消息（走 append，内存与库同时写入并分配序号）。
func seedHistory(t *testing.T, s *Session, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		s.append(llm.Message{Role: "user", Content: fmt.Sprintf("第%d句", i+1)})
	}
}

// TestRewindKeepsMemoryAndStoreInSync：撤回后**内存历史与库历史逐条一致**（条数 + 内容 + 序号）。
// 只删库不截内存（或反过来）都会让这里变红——这正是"当前的上下文里也得清理掉对应的"。
func TestRewindKeepsMemoryAndStoreInSync(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 5)

	before := s.History().Messages
	if len(before) != 5 || before[0].Seq == 0 {
		t.Fatalf("前置状态不符（落库的消息应带序号）: %+v", before)
	}
	anchor := before[2].Seq // 撤回第 3 条
	res, err := s.Rewind(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 3 || res.Seq != anchor {
		t.Fatalf("撤回结果不符: %+v", res)
	}
	mem := s.History().Messages
	disk, err := s.st.Load(s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if len(mem) != len(disk) {
		t.Fatalf("内存与库条数不一致: mem=%d disk=%d", len(mem), len(disk))
	}
	for i := range mem {
		if mem[i].Role != disk[i].Role || mem[i].Content != disk[i].Content || mem[i].Seq != disk[i].Seq {
			t.Fatalf("第 %d 条内存与库不一致: mem=%+v disk=%+v", i, mem[i], disk[i])
		}
	}
	if len(mem) != 2 || mem[0].Content != "第1句" || mem[1].Content != "第2句" {
		t.Fatalf("应只剩前两句: %+v", mem)
	}
}

// TestRewindClearsPairedRound：撤回锚点是一条 user 消息（轮边界）——它之后的整轮
// （assistant 的 2 个 tool_call + 2 个 tool 结果 + 收尾）必须**全清**，切点处游标为 0
// （配对平衡），历史里不留孤儿 tool 结果。
func TestRewindClearsPairedRound(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	s.append(llm.Message{Role: "user", Content: "帮我读两个文件"})
	s.append(callMsg("c1", "c2"))
	s.append(resultMsg("c1"))
	s.append(resultMsg("c2"))
	s.append(llm.Message{Role: "assistant", Content: "读完了"})

	before := s.History().Messages
	if len(before) != 5 {
		t.Fatalf("前置状态不符: %+v", before)
	}
	// 撤回锚点就是那条 user 消息：它之前的全部 tool_calls 都有配对结果，切点天然平衡
	anchor := before[0].Seq
	res, err := s.Rewind(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 5 {
		t.Fatalf("应删掉全部 5 条，实际 %d", res.Removed)
	}
	mem := s.History().Messages
	if len(mem) != 0 {
		t.Fatalf("撤回那条 user 之后应全清: %+v", mem)
	}
	pairing := AnalyzeToolPairing(mem)
	if !pairing.BalancedBefore(0) {
		t.Fatalf("切点处游标必须为 0（平衡）: %+v", pairing.Cuts)
	}
	if len(pairing.Unpaired) != 0 || len(pairing.Orphans) != 0 {
		t.Fatalf("历史里不许留孤儿: unpaired=%v orphans=%v", pairing.Unpaired, pairing.Orphans)
	}
	disk, err := s.st.Load(s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if len(disk) != 0 {
		t.Fatalf("库里也该清空: %+v", disk)
	}
}

// TestRewindMidHistoryKeepsPairingBalanced：撤回点落在历史中间时，保留下来的前缀仍然是
// 平衡的、没有孤儿——撤回**不会**把某一轮的 tool_calls 与它的结果拆开（锚点是轮边界的 user 消息）。
func TestRewindMidHistoryKeepsPairingBalanced(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	s.append(llm.Message{Role: "user", Content: "第一轮"})
	s.append(callMsg("c1", "c2"))
	s.append(resultMsg("c1"))
	s.append(resultMsg("c2"))
	s.append(llm.Message{Role: "assistant", Content: "第一轮收尾"})
	s.append(llm.Message{Role: "user", Content: "第二轮"})
	s.append(callMsg("c3"))
	s.append(resultMsg("c3"))
	s.append(llm.Message{Role: "assistant", Content: "第二轮收尾"})

	anchor := s.History().Messages[5].Seq // 第二轮那条 user
	if _, err := s.Rewind(anchor); err != nil {
		t.Fatal(err)
	}
	mem := s.History().Messages
	if len(mem) != 5 {
		t.Fatalf("应只剩第一轮（5 条）: %+v", mem)
	}
	pairing := AnalyzeToolPairing(mem)
	if !pairing.BalancedAfter(len(mem) - 1) {
		t.Fatalf("撤回后末尾切点必须平衡: %+v", pairing.Cuts)
	}
	if len(pairing.Unpaired) != 0 || len(pairing.Orphans) != 0 {
		t.Fatalf("历史里不许留孤儿: unpaired=%v orphans=%v", pairing.Unpaired, pairing.Orphans)
	}
}

// TestRewindRejectedWhileBusy：正在跑的一轮不能被抽掉脚下的历史（与 Compact 同款纪律）。
func TestRewindRejectedWhileBusy(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 3)
	anchor := s.History().Messages[1].Seq
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()
	if _, err := s.Rewind(anchor); !errors.Is(err, ErrBusy) {
		t.Fatalf("忙时应拒绝撤回: %v", err)
	}
	if len(s.History().Messages) != 3 {
		t.Fatal("被拒绝时历史不得改动")
	}
}

// TestRewindRecomputesContextUsage：撤回删掉了一段历史，占用测量必须跟着重算——
// 不重算的话指示器停在撤回前的数字（压缩真链路踩过同一个坑，见 AGENTS.md §2.2）。
func TestRewindRecomputesContextUsage(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 5)
	s.mu.Lock()
	// 模拟一次主轮之后的真实锚点：provider 回报了 5000 tokens
	s.context = ContextUsage{Used: 5000, Window: 32768, System: 1000, Messages: 4000}
	s.mu.Unlock()

	before := s.ContextUsage()
	anchor := s.History().Messages[2].Seq
	if _, err := s.Rewind(anchor); err != nil {
		t.Fatal(err)
	}
	after := s.ContextUsage()
	if after.Used == before.Used {
		t.Fatalf("撤回后占用必须重算（不能停在撤回前的数字）: before=%+v after=%+v", before, after)
	}
	if after.Used >= before.Used {
		t.Fatalf("删了历史占用应变小: before=%d after=%d", before.Used, after.Used)
	}
	if after.Window != before.Window {
		t.Fatalf("窗口沿用旧值: %+v", after)
	}
	// 分类之和恒等于 Used（P1 的不变式）
	if sum := after.System + after.ToolResults + after.Messages + after.Reasoning; sum != after.Used {
		t.Fatalf("分类之和应等于 Used: sum=%d used=%d", sum, after.Used)
	}
}

// TestRewindIsIdempotent：重复撤回同一条返回 0 不报错（两个客户端同时点撤回不该变成故障）。
func TestRewindIsIdempotent(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 4)
	anchor := s.History().Messages[1].Seq
	first, err := s.Rewind(anchor)
	if err != nil || first.Removed != 3 {
		t.Fatalf("首次撤回应删 3 条: %+v err=%v", first, err)
	}
	second, err := s.Rewind(anchor)
	if err != nil {
		t.Fatalf("重复撤回不该报错: %v", err)
	}
	if second.Removed != 0 {
		t.Fatalf("重复撤回应返回 0: %+v", second)
	}
	if len(s.History().Messages) != 1 {
		t.Fatalf("空操作不该改动历史: %+v", s.History().Messages)
	}
}

// TestRewindRejectsNonPositiveSeq：0 不是合法序号——没落库的消息在内存里 Seq 也是 0，
// 拿它当锚点会误伤"第一条没落库的消息之后的所有历史"。宁可报错也不猜。
func TestRewindRejectsNonPositiveSeq(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 2)
	if _, err := s.Rewind(0); err == nil {
		t.Fatal("seq=0 应被拒绝")
	}
	if len(s.History().Messages) != 2 {
		t.Fatal("被拒绝时历史不得改动")
	}
}

// TestRewindEmitsEventWithContext：撤回收尾发 RewoundEvent（宿主据此广播 chat.rewound），
// 并带上**重算后**的占用测量——客户端拿它直接刷新指示器，不必等下一轮。
func TestRewindEmitsEventWithContext(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 4)
	s.mu.Lock()
	s.context = ContextUsage{Used: 4000, Window: 32768, System: 1000, Messages: 3000}
	s.mu.Unlock()
	var mu sync.Mutex
	var events []Event
	s.emit = func(ev Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}
	anchor := s.History().Messages[1].Seq
	if _, err := s.Rewind(anchor); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var got *RewoundEvent
	for i := range events {
		if e, ok := events[i].(RewoundEvent); ok {
			got = &e
		}
	}
	if got == nil {
		t.Fatal("撤回应发 RewoundEvent")
	}
	if got.Seq != anchor || got.Removed != 3 {
		t.Fatalf("事件载荷不符: %+v", *got)
	}
	if got.Context.Used <= 0 || got.Context.Used >= 4000 {
		t.Fatalf("事件应带重算后的占用: %+v", got.Context)
	}
}

// TestSendUserMessageCarriesSeq：实时那条 user 消息也必须带序号——只让"刷新后的历史"
// 有序号等于用户当场点撤回时前端手里没有锚点。
func TestSendUserMessageCarriesSeq(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	s.stream = (&fakeStream{script: [][]llm.StreamEvent{textResult("收到")}}).stream
	var mu sync.Mutex
	var got llm.Message
	s.emit = func(ev Event) {
		if e, ok := ev.(UserMsgEvent); ok {
			mu.Lock()
			got = e.Message
			mu.Unlock()
		}
	}
	if err := s.Send("你好"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got.Seq != 0
	})
	mu.Lock()
	defer mu.Unlock()
	if got.Role != "user" || got.Content != "你好" {
		t.Fatalf("用户消息事件载荷不符: %+v", got)
	}
	// 事件里的序号必须与内存/库里的那条一致（前端按它撤回）
	if mem := s.History().Messages[0]; mem.Seq != got.Seq {
		t.Fatalf("事件序号与内存历史不一致: event=%d mem=%d", got.Seq, mem.Seq)
	}
}
