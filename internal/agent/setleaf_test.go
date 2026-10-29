// 切叶（setLeaf）在会话层的契约（docs/session-tree.md §B.2 的错误语义表）：
//
//   - 坏号报错（不静默当 0）；
//   - 配对不变量：切在 tool_calls 与它的结果之间 → 拒绝；
//   - 切到废弃分支/被影子行 → 合法（路径随之切换）；
//   - context 必须**重算**（不能沿用落库那份——那是切分支之前那条路径的测量）；
//   - busy 拒绝；无存储拒绝。
package agent

import (
	"errors"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// TestSetLeafSwitchesPath：切到旧节点后历史随之切换，且**一行不删**（切回来就回来）。
func TestSetLeafSwitchesPath(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 5)
	before := s.History().Messages
	if len(before) != 5 {
		t.Fatalf("前置状态不符: %d", len(before))
	}
	res, err := s.SetLeaf(before[1].Seq)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 3 {
		t.Fatalf("离开路径的条数应为 3，实际 %d", res.Removed)
	}
	mem := s.History().Messages
	if len(mem) != 2 {
		t.Fatalf("路径应只剩 2 条，实际 %d", len(mem))
	}
	// 一行不删：切回最新，全部回来
	if _, err := s.SetLeaf(before[4].Seq); err != nil {
		t.Fatal(err)
	}
	back := s.History().Messages
	if len(back) != 5 {
		t.Fatalf("切回最新应看到全部 5 条，实际 %d", len(back))
	}
}

// TestSetLeafRejectsUnknownSeq：坏号报错（**不静默当 0**——静默会把用户送到一个
// 他没点的位置）。
func TestSetLeafRejectsUnknownSeq(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 3)
	if _, err := s.SetLeaf(999); err == nil {
		t.Fatal("不存在的 seq 应报错")
	}
	if _, err := s.SetLeaf(-1); err == nil {
		t.Fatal("负数 seq 应报错")
	}
	// 0 = 空路径，合法
	res, err := s.SetLeaf(0)
	if err != nil {
		t.Fatal(err)
	}
	if res.LeafSeq != 0 {
		t.Fatalf("空路径的 leaf 应为 0，实际 %d", res.LeafSeq)
	}
	if len(s.History().Messages) != 0 {
		t.Fatal("空路径的历史应为空")
	}
}

// TestSetLeafRejectsUnbalancedCut：切在 assistant 的 tool_calls 与它的结果之间 →
// 拒绝（否则下一轮请求直接被端点 400 拒收）。
func TestSetLeafRejectsUnbalancedCut(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	s.append(llm.Message{Role: "user", Content: "读文件"})
	s.append(callMsg("c1"))
	callSeq := s.History().Messages[1].Seq
	s.append(resultMsg("c1"))

	_, err := s.SetLeaf(callSeq)
	if err == nil {
		t.Fatal("切在 tool_calls 与结果之间应被拒绝")
	}
	// 文案要指路
	if !errors.Is(err, err) || err.Error() == "" {
		t.Fatal("拒绝时应有人话原因")
	}
	// 历史没被动过
	if len(s.History().Messages) != 3 {
		t.Fatalf("被拒绝时历史不该改动: %d", len(s.History().Messages))
	}
}

// TestSetLeafRejectsWhileBusy：正在跑的一轮不能被抽掉脚下的历史。
func TestSetLeafRejectsWhileBusy(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 3)
	seq := s.History().Messages[1].Seq
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()
	if _, err := s.SetLeaf(seq); !errors.Is(err, ErrBusy) {
		t.Fatalf("忙时应拒绝切叶: %v", err)
	}
}

// TestSetLeafRejectsWithoutPersistence：纯内存模式没有 leaf——报人话错误
// （前端据此禁用树面板，与 chat.tree 空树同一条：**不编一棵假树**）。
func TestSetLeafRejectsWithoutPersistence(t *testing.T) {
	s, _ := newTestSession(t)
	if _, err := s.SetLeaf(1); err == nil {
		t.Fatal("没有存储时应拒绝切叶")
	}
}

// TestSetLeafRecomputesContextFromPath：切分支后**不能沿用落库那份测量**
// （那是切分支之前那条路径的）——必须重估并标注 Estimated。
func TestSetLeafRecomputesContextFromPath(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 6)
	// 库里故意存一个离谱的旧测量（模拟"上一轮主轮的真实用量"）
	s.mu.Lock()
	s.context = ContextUsage{Used: 999999, Window: 65536, System: 1000, Messages: 998999}
	s.mu.Unlock()
	if err := s.st.SaveContextUsage(s.SessionID(), s.context); err != nil {
		t.Fatal(err)
	}
	// 切到第 2 条（路径很短）
	seq := s.History().Messages[1].Seq
	if _, err := s.SetLeaf(seq); err != nil {
		t.Fatal(err)
	}
	u := s.ContextUsage()
	if u.Used >= 999999 {
		t.Fatalf("切分支后不该沿用旧路径的测量: %+v", u)
	}
	if !u.Estimated {
		t.Fatal("重估的值必须标注 Estimated（如实说明不是真实用量）")
	}
	// 落库的也必须是新值（否则重启又读回旧路径那份）
	if got, ok, err := s.st.ContextUsageOf(s.SessionID()); err != nil {
		t.Fatal(err)
	} else if ok && got.Used >= 999999 {
		t.Fatalf("落库的测量也该更新: %+v", got)
	}
}

// TestRewindKeepsRowsInStore：撤回后库里的行**一条不少**（旧分支完整保留）。
func TestRewindKeepsRowsInStore(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 4)
	anchor := s.History().Messages[2].Seq
	if _, err := s.Rewind(anchor); err != nil {
		t.Fatal(err)
	}
	if len(s.History().Messages) != 2 {
		t.Fatalf("路径应只剩 2 条: %d", len(s.History().Messages))
	}
	// 切回最新：全部回来（证明行还在）
	if _, err := s.SetLeaf(anchor + 1); err != nil {
		t.Fatal(err)
	}
	if len(s.History().Messages) != 4 {
		t.Fatalf("切回最新应看到 4 条（撤回不删行），实际 %d", len(s.History().Messages))
	}
}

// TestRewindEmitsLeafChanged：rewind 发 RewoundEvent + LeafChangedEvent（两个都发）。
func TestRewindEmitsLeafChanged(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 3)
	var rewound, leafChanged bool
	s.emit = func(ev Event) {
		switch ev.(type) {
		case RewoundEvent:
			rewound = true
		case LeafChangedEvent:
			leafChanged = true
		}
	}
	anchor := s.History().Messages[1].Seq
	if _, err := s.Rewind(anchor); err != nil {
		t.Fatal(err)
	}
	if !rewound {
		t.Fatal("rewind 应发 RewoundEvent（老客户端的截断路径）")
	}
	if !leafChanged {
		t.Fatal("rewind 也应发 LeafChangedEvent（新客户端与树面板）")
	}
}

// TestSetLeafEmitsOnlyLeafChanged：setLeaf 只发 LeafChangedEvent（老客户端会按
// chat.rewound 错误截断）。
func TestSetLeafEmitsOnlyLeafChanged(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 3)
	var rewound, leafChanged bool
	s.emit = func(ev Event) {
		switch ev.(type) {
		case RewoundEvent:
			rewound = true
		case LeafChangedEvent:
			leafChanged = true
		}
	}
	if _, err := s.SetLeaf(s.History().Messages[0].Seq); err != nil {
		t.Fatal(err)
	}
	if rewound {
		t.Fatal("setLeaf 不该发 RewoundEvent")
	}
	if !leafChanged {
		t.Fatal("setLeaf 应发 LeafChangedEvent")
	}
}

// TestTreeFromStore：Session.Tree 从库里读骨架（节点数与 leaf 正确）。
func TestTreeFromStore(t *testing.T) {
	s := newPersistSession(t, t.TempDir())
	seedHistory(t, s, 4)
	tree, err := s.Tree(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Nodes) != 4 || tree.Total != 4 {
		t.Fatalf("树应有 4 个节点，实际 %d（total=%d）", len(tree.Nodes), tree.Total)
	}
	if tree.LeafSeq != s.History().Messages[3].Seq {
		t.Fatalf("树的 leaf 不符: %d", tree.LeafSeq)
	}
	for _, n := range tree.Nodes {
		if !n.OnPath {
			t.Fatalf("单链会话的所有节点都应在路径上: %+v", n)
		}
	}
}

// TestTreeEmptyWithoutStore：无存储 → 空树（不编假树）。
func TestTreeEmptyWithoutStore(t *testing.T) {
	s, _ := newTestSession(t)
	tree, err := s.Tree(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Nodes) != 0 {
		t.Fatalf("无存储应返回空树，实际 %d 个节点", len(tree.Nodes))
	}
}
