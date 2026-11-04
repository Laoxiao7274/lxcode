// 纯 sleep 硬拦的判定矩阵与执行面单测（sleepguard.go）。
//
// 矩阵覆盖任务的验收口径：
//   - sleep 60 / sleep 60; echo x → 拒（纯盲等，可带 echo 尾巴）；
//   - sleep 60; curl x / sleep 300; ssh host → 放行 + 附加提示（组合命令）；
//   - run_in_background 的 sleep → 放行（轮询循环的合法组件）；
//   - 轮询循环本体（until cond; do sleep 30; done）→ 放行不提示；
//   - 与 sleep 无关的命令 → 原样放行（不添噪声）。
package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestPureSleepCommandMatrix：判定函数的纯矩阵（不执行命令）。
func TestPureSleepCommandMatrix(t *testing.T) {
	cases := []struct {
		cmd     string
		blocked bool
		hint    bool
	}{
		{"sleep 60", true, true},
		{"sleep 60; echo x", true, true},
		{"sleep 60; echo t12", true, true},
		{"sleep 60; echo a; echo b", true, true},
		{"sleep 60;", true, true}, // 尾随悬空分隔符
		{"sleep 1.5m", true, true},
		{"sleep 0.5 && echo done", true, true}, // 去 echo 尾巴后 = 纯 sleep
		{"env SLEEP=1 sleep 60", true, true},
		{"sleep 1 2 3", true, true},
		{"sleepx 60", false, false}, // 子串不是 sleep
		{"echo hi", false, false},   // 与 sleep 无关
		{"ls -la", false, false},
		{"sleep 60 && curl http://x", false, true},           // 组合：放行 + 提示
		{"sleep 300; ssh host", false, true},                 // 组合：放行 + 提示
		{"until curl -s x; do sleep 30; done", false, false}, // 轮询循环本体
		{"while ! nc -z h 1; do sleep 5; done", false, false},
	}
	for _, tc := range cases {
		hint, blocked := pureSleepCommand(tc.cmd)
		if blocked != tc.blocked {
			t.Errorf("%q: blocked=%v 要 %v", tc.cmd, blocked, tc.blocked)
		}
		if blocked {
			if hint == "" {
				t.Errorf("%q: 拦截必须带替代指引", tc.cmd)
			}
			continue
		}
		if (hint != "") != tc.hint {
			t.Errorf("%q: hint=%q 要有=%v", tc.cmd, hint, tc.hint)
		}
	}
}

// TestPureSleepBlockHasExecutableGuidance：拒绝文案必须给出**可执行**的替代
// （job_output(wait=true) / run_in_background 轮询循环）——只说「不要 sleep」
// 不给出路，模型只会换个姿势继续盲等。
func TestPureSleepBlockHasExecutableGuidance(t *testing.T) {
	hint, blocked := pureSleepCommand("sleep 120")
	if !blocked {
		t.Fatal("sleep 120 应被硬拦")
	}
	if !strings.Contains(hint, "job_output") || !strings.Contains(hint, "wait=true") {
		t.Fatalf("指引应包含 job_output(wait=true): %q", hint)
	}
	if !strings.Contains(hint, "run_in_background") {
		t.Fatalf("指引应包含轮询循环路径: %q", hint)
	}
}

// TestBashExecBlocksPureSleep：执行面端到端——纯 sleep 在 Exec 前被拒，
// 不会真的跑起来（跑起来 = 等满整个 sleep，测试就卡死了；拒绝即秒回）。
func TestBashExecBlocksPureSleep(t *testing.T) {
	r := New()
	ctx := context.Background()
	for _, cmd := range []string{"sleep 60", "sleep 60; echo x"} {
		args, _ := json.Marshal(map[string]string{"command": cmd})
		out := r.Execute(ctx, call("bash", string(args)))
		if !strings.Contains(out, "已拒绝执行纯 sleep") {
			t.Fatalf("%q 应被硬拦: %q", cmd, out)
		}
		if !strings.Contains(out, "job_output") {
			t.Fatalf("拒绝文案应给可执行替代: %q", out)
		}
	}
}

// TestBashExecAllowsBackgroundSleep：run_in_background=true 的 sleep 放行
//（轮询循环的合法组件）。真跑会等满 60s——断言它确实被**执行**了（启动了
// 后台任务），随即杀掉。
func TestBashExecAllowsBackgroundSleep(t *testing.T) {
	r, mgr := newJobRegistry(t)
	ctx := context.Background()
	out := r.Execute(ctx, call("bash", `{"command":"sleep 60","run_in_background":true}`))
	if strings.Contains(out, "已拒绝") {
		t.Fatalf("后台 sleep 不该被拦: %q", out)
	}
	if !strings.Contains(out, "后台任务已启动") {
		t.Fatalf("后台 sleep 应正常启动: %q", out)
	}
	for _, s := range mgr.List("") {
		_, _ = mgr.Kill(s.ID, "agent")
	}
}

// TestBashExecComboSleepHint：组合命令（sleep + 真动作）放行执行，
// 结果前带等待纪律提示。
func TestBashExecComboSleepHint(t *testing.T) {
	r := New()
	ctx := context.Background()
	out := r.Execute(ctx, call("bash", `{"command":"echo combo-done; sleep 0.1"}`))
	if strings.Contains(out, "已拒绝") {
		t.Fatalf("组合命令不该被拦: %q", out)
	}
	if !strings.Contains(out, "等待纪律") || !strings.Contains(out, "combo-done") {
		t.Fatalf("组合命令应执行且带提示: %q", out)
	}
}

// TestBashExecPlainCommandNoNoise：普通命令不带任何 sleep 提示（不添噪声）。
func TestBashExecPlainCommandNoNoise(t *testing.T) {
	r := New()
	ctx := context.Background()
	out := r.Execute(ctx, call("bash", `{"command":"echo plain"}`))
	if strings.Contains(out, "等待纪律") {
		t.Fatalf("普通命令不该带提示: %q", out)
	}
}
