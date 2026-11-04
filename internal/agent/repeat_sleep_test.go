// 会话级重复防护的 **sleep 特例**（2026-12 修订）：sleep 类调用收紧阈值
//（3 软 / 8 硬）且 echo 变体归一成同一个 key——`sleep 280; echo t12` 与
// `sleep 280; echo t13` 是同一个调用。
//
// 数据依据（真库取证）：验收子会话起后台任务（15.8s 跑完）后又 sleep 累计
// 53.5 分钟，`sleep 280; echo tNN` 变体连续十几轮——参数尾巴每次在变，
// 逐字归一后是「不同调用」，通用阈值（10/25）根本抓不到。sleep 不产生信息，
// 3 次就该提醒、8 次点名劝止。
package agent

import (
	"strings"
	"testing"
)

// TestSessionRepeatSleepVariantsSameKey：echo 尾巴变体算同一个调用——
// 变体交替出现也要累计到收紧阈值。
func TestSessionRepeatSleepVariantsSameKey(t *testing.T) {
	g := newSessionRepeatGuard()
	// 前 2 次不提醒（软阈值 3 起）；t12/t13/t14 是真库里的变体形态。
	variants := []string{
		`{"command":"sleep 280; echo t12"}`,
		`{"command":"sleep 280; echo t13"}`,
		`{"command":"sleep 280; echo t14"}`,
	}
	for i := 0; i < sessionSleepSoft-1; i++ {
		if hint := g.observe("bash", variants[i%len(variants)]); hint != "" {
			t.Fatalf("第 %d 次（变体）不该注入（sleep 软阈值 %d 起）: %q", i+1, sessionSleepSoft, hint)
		}
	}
	hint := g.observe("bash", `{"command":"sleep 280; echo t15"}`)
	if hint == "" {
		t.Fatalf("第 %d 次（变体）应注入软提醒", sessionSleepSoft)
	}
	if !strings.Contains(hint, "job_output") {
		t.Fatalf("sleep 提醒应指到正确的等待姿势: %q", hint)
	}
	if strings.Contains(hint, "25") {
		t.Fatalf("sleep 特例不该走通用阈值文案: %q", hint)
	}
}

// TestSessionRepeatSleepHardThreshold：sleep 硬阈值 8（不是通用的 25），
// 文案点名次数并劝止。
func TestSessionRepeatSleepHardThreshold(t *testing.T) {
	g := newSessionRepeatGuard()
	args := `{"command":"sleep 60"}`
	for i := 1; i < sessionSleepHard; i++ {
		_ = g.observe("bash", args)
	}
	hint := g.observe("bash", args)
	if hint == "" {
		t.Fatalf("第 %d 次 sleep 应注入硬提醒", sessionSleepHard)
	}
	if !strings.Contains(hint, "8") {
		t.Fatalf("硬提醒应点名次数 8（不是 25）: %q", hint)
	}
}

// TestSessionRepeatSleepKeyOrderAgnostic：sleep 参数的键序/空白抖动同样归一
//（normalizeCallKey 先做，sleepVariantKey 再做变体归一）。
func TestSessionRepeatSleepKeyOrderAgnostic(t *testing.T) {
	g := newSessionRepeatGuard()
	for i := 1; i < sessionSleepSoft; i++ {
		args := `{"command":"sleep 30; echo a"}`
		if i%2 == 0 {
			args = `{ "command" : "sleep 30; echo b" }`
		}
		if hint := g.observe("bash", args); hint != "" {
			t.Fatalf("第 %d 次变体不该注入", i)
		}
	}
	// echo 在前、sleep 在后 = 组合命令（不是纯 sleep 变体）——独立计数、
	// 走通用阈值，不进 sleep 特例。
	if hint := g.observe("bash", `{"command":"echo c; sleep 30"}`); hint != "" {
		t.Fatalf("echo 在前的组合命令不该触发 sleep 特例: %q", hint)
	}
}

// TestSessionRepeatBackgroundSleepNotVariant：run_in_background 的 sleep 是
// 轮询循环的合法组件，不进 sleep 特例（按普通调用走通用阈值）。
func TestSessionRepeatBackgroundSleepNotVariant(t *testing.T) {
	g := newSessionRepeatGuard()
	for i := 1; i < sessionSleepSoft; i++ {
		if hint := g.observe("bash", `{"command":"sleep 30","run_in_background":true}`); hint != "" {
			t.Fatalf("后台 sleep 第 %d 次不该按 sleep 特例注入: %q", i, hint)
		}
	}
}

// TestSessionRepeatSleepDoesNotSwallowOtherBash：sleep 变体 key 是独立的——
// 其它 bash 命令的计数不被它吃掉，反之亦然。
func TestSessionRepeatSleepDoesNotSwallowOtherBash(t *testing.T) {
	g := newSessionRepeatGuard()
	if hint := g.observe("bash", `{"command":"go test ./..."}`); hint != "" {
		t.Fatalf("普通命令第一次不该注入: %q", hint)
	}
	// sleep 变体累计若干次，普通命令计数不受影响
	for i := 0; i < sessionSleepSoft; i++ {
		_ = g.observe("bash", `{"command":"sleep 90; echo s"}`)
	}
	if hint := g.observe("bash", `{"command":"go test ./..."}`); hint != "" {
		t.Fatalf("普通命令的计数不该被 sleep 变体污染: %q", hint)
	}
}
