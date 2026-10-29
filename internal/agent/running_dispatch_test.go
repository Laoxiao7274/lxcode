// 运行中派发的观察快照登记（chat.history 的 running_dispatches 数据源）：
//
// 用户场景（2026-12，安卓端报）：派发由电脑（桌面端）发起，安卓端只是观察者——
// 它错过（或在历史回放里从未收到过）dispatchStart 广播，回放重建的派发卡没有
// 子会话 id → 点不开子会话、todo/子工具事件按卡反查也无处可投（「任务进度消失」）。
// 修法 = Session 登记**正在跑**的派发快照，chat.history 导出 running_dispatches，
// 观察端按 dispatch_id 回填卡。
//
// 生命周期是这条修复的命门：跑完（SendWait 收尾）必须注销——把已结束的派发报成
// 运行中，观察端会把收尾的卡翻回"执行中"。defer unregister 的三条收尾路径
//（正常 / 取消 / panic）由这两条测试钉住。
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
)

// 让子会话的流**等一下再结束**：测试要在子轮跑到一半时读观察快照。
func slowSubStream(delay time.Duration, text string) StreamFn {
	mainRound := 0
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if strings.Contains(msgs[0].Content, "主 Agent（调度中枢）") {
			mainRound++
			if mainRound > 1 {
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已验收。"}, FinishReason: llm.FinishStop}}
				}()
				return ch, nil
			}
			tc := llm.ToolCall{ID: "call-obs"}
			tc.Function.Name = "agent_dispatch"
			tc.Function.Arguments = `{"agent":"coder","task":"跑观察测试"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 子会话语境：先等 delay 再给结论——给测试留观察窗口
		go func() {
			defer close(ch)
			time.Sleep(delay)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: text}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: text}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	}
}

// 派发进行中：观察快照在（子会话 id / Agent 名 / 任务 / 归属键）。
func TestRunningDispatchVisibleWhileRunning(t *testing.T) {
	// 挂存储的环境：子会话是独立的会话行（ChildSessionID 非空——观察端回填卡
	// 靠的就是它；纯内存子会话没有 id，快照上它只能是空）。
	env, _ := newChildSessionEnv(t)
	env.s.SetStream(slowSubStream(300*time.Millisecond, "子结论"))
	if err := env.s.Send("把测试跑了"); err != nil {
		t.Fatal(err)
	}
	// 等到登记出现（Send 立即返回，派发要先开起来）
	var got []RunningDispatch
	for i := 0; i < 200; i++ {
		got = env.s.RunningDispatches()
		if len(got) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(got) != 1 {
		t.Fatalf("派发进行中应登记一条观察快照: %+v", got)
	}
	d := got[0]
	if d.DispatchID != "call-obs" || d.AgentName != "代码 Agent" || !strings.Contains(d.Task, "观察测试") {
		t.Fatalf("观察快照字段不符: %+v", d)
	}
	if d.ChildSessionID == "" {
		t.Fatal("挂了存储的派发应有子会话 id")
	}
	// 跑完：注销（生命周期——把已结束的报成运行中是这条修复的红线）
	waitDispatchIdle(t, env.s)
	if left := env.s.RunningDispatches(); len(left) != 0 {
		t.Fatalf("派发结束后应注销观察快照: %+v", left)
	}
}

// 跑完之后的登记必须消失（正时序的冗余断言，避免 slow 流 timing 抖动让上一条
// 的后半段变成空跑）。
func TestRunningDispatchClearedAfterFinish(t *testing.T) {
	env := newDispatchSession(t, slowSubStream(0, "子结论"))
	if err := env.s.Send("把测试跑了"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)
	if left := env.s.RunningDispatches(); len(left) != 0 {
		t.Fatalf("空闲会话不应再报运行中派发: %+v", left)
	}
}

// 取消路径同样注销（defer 覆盖——观察面不残留僵尸条目）。
func TestRunningDispatchClearedAfterCancel(t *testing.T) {
	env := newDispatchSession(t, slowSubStream(3*time.Second, "不该到"))
	if err := env.s.Send("把测试跑了"); err != nil {
		t.Fatal(err)
	}
	var got []RunningDispatch
	for i := 0; i < 200; i++ {
		got = env.s.RunningDispatches()
		if len(got) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(got) == 0 {
		t.Fatal("取消前应先看到登记（流没起来就取消测的是空转）")
	}
	env.s.Cancel()
	waitDispatchIdle(t, env.s)
	if left := env.s.RunningDispatches(); len(left) != 0 {
		t.Fatalf("取消后应注销观察快照: %+v", left)
	}
}
