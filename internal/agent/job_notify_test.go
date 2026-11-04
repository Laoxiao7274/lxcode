// 执行会话通告口（2026-12 修订，根因①的工具面）：后台任务结束通告按时间线
// 归属投给父会话，子会话自己收不到——子 Agent 只能 sleep 盲等（真库实锤：
// 验收子会话 sleep 累计 53.5 分钟、job_output 调用 0 次）。
//
// 这里的钉子是**真实工具链路**：带 jobNotify 注入的会话，模型调
// job_output(wait=true) 等一个任务，settle 的瞬间通告经注入回调转进本会话——
// 不依赖 server 在场（server 集成面的双投断言在 internal/server/jobs_test.go）。
package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
)

// TestJobNotifyDeliveredOnWaitSettle：子会话 job_output(wait=true) 等任务，
// settle 即触发执行会话通告（经 SetJobNotify 注入的回调）。
func TestJobNotifyDeliveredOnWaitSettle(t *testing.T) {
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(config.ModelConfig{
		ID: "m1", BaseURL: "http://127.0.0.1:1/v1", Model: "m1", Enabled: true,
		Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}

	// 假流：第一轮发 job_output(wait=true)，看到 tool 结果后收尾。
	round := 0
	jobID := "job-notify-1"
	s := New(reg, tools.New(), func(Event) {})
	defer s.Close()
	stream := func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		go func() {
			defer close(ch)
			round++
			if round > 1 {
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: "任务结束了。"}, FinishReason: llm.FinishStop,
				}}
				return
			}
			tc := llm.ToolCall{ID: "c1"}
			tc.Function.Name = "job_output"
			args, _ := json.Marshal(map[string]any{"job_id": jobID, "wait": true, "timeout_ms": 10000})
			tc.Function.Arguments = string(args)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}},
				FinishReason: llm.FinishToolCalls,
			}}
		}()
		return ch, nil
	}
	s.SetStream(stream)

	// 执行会话通告口：记录通告（生产是 server 的 Notify——唤醒/轮边界注入）
	noticed := make(chan string, 4)
	s.SetJobNotify(func(text string) { noticed <- text })

	// 装配任务管理器（工具面）+ 起一个不结束的任务
	mgr := jobs.NewManager(t.TempDir())
	defer mgr.Shutdown()
	s.tools.SetJobs(mgr)
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "长任务", SessionID: "mem-x"})
	if err != nil {
		t.Fatal(err)
	}
	jobID = j.ID()

	// 跑一轮（工具循环会 job_output(wait=true) 等住）；
	// 稍等订阅挂上，再 settle——settle 应触发通告转投。
	if err := s.Send("等长任务结束"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")

	// 等本轮收尾（tool 结果回来 + 第二轮完成）
	waitIdle(t, s)
	select {
	case text := <-noticed:
		if !strings.Contains(text, "长任务") || !strings.Contains(text, "已结束") {
			t.Fatalf("通告文本不符: %q", text)
		}
		if !strings.Contains(text, "job_output") {
			t.Fatalf("通告应指到读输出的路径: %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("settle 后执行会话通告口没有被调用（子会话收不到任务结束——盲等复现）")
	}
	// 工具结果也应等到了终态（wait=true 的语义）
	for _, m := range s.History().Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "[status: completed]") {
			return
		}
	}
	t.Fatal("job_output(wait=true) 的结果应是任务终态")
}

// TestJobNotifyNilIsNoop：未注入通告口（纯内核调用方）时 wait 照常工作，
// 不 panic 不报错——行为与改动前一致。
func TestJobNotifyNilIsNoop(t *testing.T) {
	r := tools.New()
	mgr := jobs.NewManager(t.TempDir())
	defer mgr.Shutdown()
	r.SetJobs(mgr)
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "t", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args, _ := json.Marshal(map[string]any{"job_id": j.ID(), "wait": true, "timeout_ms": 5000})
	tc := llm.ToolCall{ID: "tc1"}
	tc.Function.Name = "job_output"
	tc.Function.Arguments = string(args)
	out := r.Execute(tools.WithSessionID(ctx, "s"), tc)
	if !strings.Contains(out, "[status: completed]") {
		t.Fatalf("wait 应等到终态: %q", out)
	}
}
