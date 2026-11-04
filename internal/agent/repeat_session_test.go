// 会话级重复防护（P7）的单测（repeat.go 的 sessionRepeatGuard）。
//
// 与 repeat_test.go（单轮连续守卫）的分工：这里测的是**跨轮**维度——
// 键序无关的归一化、跨轮累计、阈值注入、LRU 容量与逐出。
package agent

import (
	"strings"
	"testing"
)

// TestSessionRepeatCrossTurn：计数跨轮持久——guard 不在轮间重置，同一 callKey
// 累计到阈值就注入（这正是单轮 repeatGuard 跨轮重置所漏掉的形态）。
func TestSessionRepeatCrossTurn(t *testing.T) {
	g := newSessionRepeatGuard()
	// 模拟 9 轮、每轮一次同一调用（参数键序每次抖动——归一化后是同一调用）：
	// 前 9 次不注入（10 次起）。
	for i := 1; i < sessionRepeatSoft; i++ {
		var args string
		if i%2 == 0 {
			args = `{"limit":20,"path":"a.go"}`
		} else {
			args = `{"path":"a.go","limit":20}`
		}
		if hint := g.observe("read_file", args); hint != "" {
			t.Fatalf("第 %d 次不该注入（软阈值 %d 起）: %q", i, sessionRepeatSoft, hint)
		}
	}
	hint := g.observe("read_file", `{"limit":20,"path":"a.go"}`)
	if hint == "" {
		t.Fatalf("第 %d 次应注入软提醒", sessionRepeatSoft)
	}
	if !strings.HasPrefix(hint, RepeatNoticePrefix) {
		t.Fatalf("注入应带前缀（前端据此不渲染成用户气泡）: %q", hint)
	}
	if !strings.Contains(hint, "本会话已执行") {
		t.Fatalf("软提醒应说明会话级累计次数: %q", hint)
	}
	// 之后每次都注入（软提醒持续，不只在阈值点一次——长尾要持续被提醒）。
	if hint := g.observe("read_file", `{"path":"a.go","limit":20}`); hint == "" {
		t.Fatal("软阈值之后每次都应注入")
	}
}

// TestSessionRepeatHardThreshold：硬提醒从 sessionRepeatHard 起，点名次数；
// 且会话级防护**绝不阻断**——observe 没有停止语义（阻断属于单轮守卫）。
func TestSessionRepeatHardThreshold(t *testing.T) {
	g := newSessionRepeatGuard()
	for i := 1; i < sessionRepeatHard; i++ {
		_ = g.observe("bash", `{"command":"go test"}`)
	}
	hint := g.observe("bash", `{"command":"go test"}`)
	if hint == "" {
		t.Fatalf("第 %d 次应注入硬提醒", sessionRepeatHard)
	}
	if !strings.Contains(hint, "25") {
		t.Fatalf("硬提醒应点名次数 25: %q", hint)
	}
	if !strings.Contains(hint, "不要") && !strings.Contains(hint, "停下") {
		t.Fatalf("硬提醒应明确劝止: %q", hint)
	}
}

// TestSessionRepeatKeyOrderAgnostic：参数 JSON 键序无关——同一组参数换键序/
// 加空白算同一个调用（与连续守卫的逐字比较刻意不同，见 normalizeCallKey 注释）。
func TestSessionRepeatKeyOrderAgnostic(t *testing.T) {
	g := newSessionRepeatGuard()
	// 同一调用交替用两种键序：计数必须连续累计到软阈值。
	for i := 1; i < sessionRepeatSoft; i++ {
		a := `{"pattern":"foo","path":"."}`
		b := `{ "path" : "." , "pattern":"foo" }`
		if i%2 == 0 {
			a, b = b, a
		}
		if hint := g.observe("search", a); hint != "" {
			t.Fatalf("第 %d 次不应注入（键序抖动应算同一调用，累计未到阈值）", i)
		}
		_ = b
	}
	if hint := g.observe("search", `{"path":".","pattern":"foo"}`); hint == "" {
		t.Fatal("键序抖动的同一调用应累计触发")
	}
	// 参数不同仍是不同 key：不注入。
	if hint := g.observe("search", `{"path":".","pattern":"bar"}`); hint != "" {
		t.Fatalf("不同参数是新调用，不该注入: %q", hint)
	}
}

// TestSessionRepeatLRUCap：LRU 容量上限——超限逐出最久未见的 key（重新计数），
// 最近使用的 key 不被逐出。
func TestSessionRepeatLRUCap(t *testing.T) {
	g := newSessionRepeatGuard()
	// 灌满容量：victim 先进，随后 200 个新 key 把它挤出去。
	if hint := g.observe("t", `{"k":"victim"}`); hint != "" {
		t.Fatal("首次调用不该注入")
	}
	for i := 0; i < sessionRepeatLRUCap; i++ {
		if hint := g.observe("t", `{"k":"`+itoa(i)+`"}`); hint != "" {
			t.Fatalf("第 %d 个新 key 首次出现不该注入: %q", i, hint)
		}
	}
	if len(g.counts) > sessionRepeatLRUCap {
		t.Fatalf("容量超限: %d", len(g.counts))
	}
	// victim 已被逐出：重新计数（第 1 次不注入）。
	if hint := g.observe("t", `{"k":"victim"}`); hint != "" {
		t.Fatalf("被逐出的 key 应重新计数（首次不注入）: %q", hint)
	}
	// 最近使用的不逐出：再灌 1 个新 key，最老的 survivor（第一次循环的第一个）
	// 被挤走，而 victim 刚被 touch 过应该还在。
	if hint := g.observe("t", `{"k":"fresh"}`); hint != "" {
		t.Fatalf("新 key 首次不该注入: %q", hint)
	}
	// victim 应仍在（上次 observe 触碰过它）：
	if _, ok := g.counts["t\x1e"+normalizeCallKey(`{"k":"victim"}`)]; !ok {
		t.Fatal("最近使用过的 key 不该被逐出")
	}
	// 再灌 1 个新 key：现在被逐出的应是第一次循环里的第一个 key。
	if hint := g.observe("t", `{"k":"fresh2"}`); hint != "" {
		t.Fatalf("新 key 首次不该注入: %q", hint)
	}
	if _, ok := g.counts["t\x1e"+normalizeCallKey(`{"k":"0"}`)]; ok {
		t.Fatal("最久未见的 key 应被逐出")
	}
}

// TestSessionRepeatLongArgsTruncated：超长参数截断进 key（内存纪律）；
// 前 512 字节相同的两个调用算同一个（这正是 write_file 反复全量重写要抓的形态）。
func TestSessionRepeatLongArgsTruncated(t *testing.T) {
	g := newSessionRepeatGuard()
	long := strings.Repeat("x", 3000)
	for i := 1; i < sessionRepeatSoft; i++ {
		if hint := g.observe("write_file", `{"path":"a.txt","content":"`+long+`"}`); hint != "" {
			t.Fatalf("第 %d 次不应注入: %q", i, hint)
		}
	}
	if hint := g.observe("write_file", `{"content":"`+long+`","path":"a.txt"}`); hint == "" {
		t.Fatal("超长参数截断后同一调用应累计触发（键序无关）")
	}
}

// TestSessionRepeatInvalidArgsVerbatim：非法 JSON 参数退回逐字比较——
// 没法归一就别硬归一，宁可漏判不可错判。
func TestSessionRepeatInvalidArgsVerbatim(t *testing.T) {
	g := newSessionRepeatGuard()
	if normalizeCallKey(`bash -c "echo hi"`) != `bash -c "echo hi"` {
		t.Fatalf("非 JSON 参数应逐字保留: %q", normalizeCallKey(`bash -c "echo hi"`))
	}
	for i := 1; i < sessionRepeatSoft; i++ {
		if hint := g.observe("bash", `git log --oneline -5`); hint != "" {
			t.Fatalf("第 %d 次不应注入: %q", i, hint)
		}
	}
	if hint := g.observe("bash", `git log --oneline -5`); hint == "" {
		t.Fatal("逐字相同的非 JSON 参数应累计触发")
	}
	// 尾部空白不影响（trim 后逐字）。
	if hint := g.observe("bash", `git log --oneline -5  `); hint == "" {
		t.Fatal("仅尾随空白的非 JSON 参数应算同一调用")
	}
}

// TestSessionRepeatWiredIntoRunTools：接线验证——runTools 在执行位把前缀提醒
// 注进工具结果。用最小 Session（nil 注册表不行，用真注册表 + 单轮守卫不触发
// 的参数），靠直接断言 guard 计数与注入文本的组合覆盖；端到端循环行为由
// runTurn 的既有测试面兜底。
func TestSessionRepeatWiredIntoRunTools(t *testing.T) {
	s := New(nil, nil, nil)
	if s.sessionRepeat == nil {
		t.Fatal("New 必须初始化会话级重复防护")
	}
	// 模拟接线处的注入逻辑（与 runTools 同一行代码的语义）：
	var out string
	for i := 1; i <= sessionRepeatSoft; i++ {
		out = "ok"
		if hint := s.sessionRepeat.observe("read_file", `{"path":"a"}`); hint != "" {
			out = hint + "\n" + out
		}
	}
	if !strings.HasPrefix(out, RepeatNoticePrefix) {
		t.Fatalf("第 %d 次的结果应以提醒前缀开头: %q", sessionRepeatSoft, out)
	}
}

// itoa 是测试内的小助手（避免为 fmt 只写一个调用）。
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// mkSessionRepeatCalls 复用 mkCalls 的形状说明会话级层不看整批——它的观测
// 粒度是单个调用（一批里每个调用各记各的）。
func TestSessionRepeatPerCallNotPerBatch(t *testing.T) {
	g := newSessionRepeatGuard()
	// 一批两个不同调用，各记各的：任何一个都不该单独累计到阈值。
	batch := mkCalls("read_file|{\"path\":\"a\"}", "read_file|{\"path\":\"b\"}")
	for i := 0; i < sessionRepeatSoft-1; i++ {
		for _, tc := range batch {
			if hint := g.observe(tc.Function.Name, tc.Function.Arguments); hint != "" {
				t.Fatalf("第 %d 轮不应注入（各调用独立计数，未到阈值）", i+1)
			}
		}
	}
	// 现在 a 与 b 各 9 次；再各跑一次都到 10：都注入（粒度是单个调用，
	// 不是整批——但同一调用跨批的计数是连续的）。换个新参数确认独立 key 不注入。
	if hint := g.observe("read_file", `{"path":"a"}`); hint == "" {
		t.Fatal("a 到 10 次应注入")
	}
	if hint := g.observe("read_file", `{"path":"b"}`); hint == "" {
		t.Fatal("b 也到 10 次应注入（各调用独立累计）")
	}
	if hint := g.observe("read_file", `{"path":"c"}`); hint != "" {
		t.Fatalf("新调用 c 首次出现，不该注入: %q", hint)
	}
}
