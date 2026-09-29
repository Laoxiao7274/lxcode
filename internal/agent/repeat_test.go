// 死循环判据的单测（repeat.go）。用户拍板：去掉轮数上限，改判「同一批调用重复」。
//
// 这里的参数刻意用普通字符串（不是合法 JSON）：判据只做**逐字比较**，与参数是不是
// JSON 无关——用普通串反而让测试意图（比较的是文本）一目了然。
package agent

import (
	"strconv"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

func mkCalls(specs ...string) []llm.ToolCall {
	out := make([]llm.ToolCall, 0, len(specs))
	for i, s := range specs {
		name, args, _ := strings.Cut(s, "|")
		tc := llm.ToolCall{ID: "c" + strconv.Itoa(i)}
		tc.Function.Name = name
		tc.Function.Arguments = args
		out = append(out, tc)
	}
	return out
}

// TestRepeatGuardEscalates：同一批调用连续重复 → 3 轻提醒、5 点名提醒、8 停止；
// 其余次数不打扰（每轮都塞一条提醒本身就是在灌上下文）。
func TestRepeatGuardEscalates(t *testing.T) {
	g := &repeatGuard{}
	same := mkCalls("bash|go test ./...")
	for i := 1; i <= repeatStop; i++ {
		hint, stop := g.observe(same)
		switch i {
		case repeatGentle, repeatFirm:
			if stop {
				t.Fatalf("第 %d 次重复不该停止（先提醒）", i)
			}
			if hint == "" {
				t.Fatalf("第 %d 次重复应给提醒", i)
			}
			if !strings.HasPrefix(hint, RepeatNoticePrefix) {
				t.Fatalf("提醒应带 %q 前缀（前端据此不渲染成用户气泡）: %q", RepeatNoticePrefix, hint)
			}
		case repeatStop:
			if !stop {
				t.Fatalf("第 %d 次重复应判定死循环（提醒两次都没用）", i)
			}
			if !strings.Contains(hint, "死循环") {
				t.Fatalf("停止理由应说明是死循环: %q", hint)
			}
		default:
			if hint != "" || stop {
				t.Fatalf("第 %d 次重复不该触发（阈值 %d/%d/%d）: hint=%q stop=%v",
					i, repeatGentle, repeatFirm, repeatStop, hint, stop)
			}
		}
	}
}

// TestRepeatGuardResetsOnDifferentArgs：参数一变就重新计数——「改了参数再试一次」是正常
// 调试，不是死循环（这正是轮数上限做不到的区分）。
func TestRepeatGuardResetsOnDifferentArgs(t *testing.T) {
	g := &repeatGuard{}
	for i := 0; i < repeatStop+3; i++ {
		hint, stop := g.observe(mkCalls("bash|echo " + strconv.Itoa(i)))
		if hint != "" || stop {
			t.Fatalf("第 %d 轮参数不同，不该判死循环: hint=%q stop=%v", i+1, hint, stop)
		}
	}
	if g.count != 1 {
		t.Fatalf("每轮都不同 → 计数恒为 1，得到 %d", g.count)
	}
}

// TestRepeatGuardTreatsWholeBatchAsUnit：一轮发多个调用时，**整批**相同才算重复；
// 顺序或参数变一个就不算（并行派发两个子 Agent 时这是常态）。
func TestRepeatGuardTreatsWholeBatchAsUnit(t *testing.T) {
	g := &repeatGuard{}
	batch := mkCalls("read_file|a", "read_file|b")
	for i := 0; i < repeatGentle-1; i++ {
		if hint, stop := g.observe(batch); hint != "" || stop {
			t.Fatalf("第 %d 轮不该触发", i+1)
		}
	}
	if hint, stop := g.observe(batch); hint == "" || stop {
		t.Fatalf("第 %d 轮应给轻提醒: hint=%q stop=%v", repeatGentle, hint, stop)
	}
	if hint, stop := g.observe(mkCalls("read_file|b", "read_file|a")); hint != "" || stop {
		t.Fatalf("顺序变了应重新计数: hint=%q stop=%v", hint, stop)
	}
}

// TestRepeatGuardIgnoresEmptyRounds：没有工具调用的轮次不参与判定。
func TestRepeatGuardIgnoresEmptyRounds(t *testing.T) {
	g := &repeatGuard{}
	for i := 0; i < repeatStop+2; i++ {
		if hint, stop := g.observe(nil); hint != "" || stop {
			t.Fatalf("空轮不该触发: %q %v", hint, stop)
		}
	}
}
