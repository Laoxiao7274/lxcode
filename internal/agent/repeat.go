// 死循环判据：**连续同一批工具调用**（工具名 + 参数逐字相同）。
//
// 为什么不是"轮数上限"（2026-09-29 用户拍板去掉 maxToolRounds=16）：轮数区分不了
// 「卡住」与「任务本来就长」。真库实测：一个通读仓库的 researcher 跑了 16 轮，每轮参数
// 都不同，却被轮数上限误杀在半路——最后一条消息是工具调用（正文为空），没有结论，主
// Agent 于是拿它的会话 id 又派了一遍。真正该盯的是**不进展**：同参数反复调同一个工具。
// 判据与阈值对齐 DSH 的 dsh-repeat-tool-reminder（阈值 3/5/8，先软后硬）。
//
// 为什么参数**逐字**比较、不做 JSON 归一：模型真要重试同一条命令，参数就是一个字都不差
// 的；归一化反而会把"我改了参数再试一次"也算成重复——那是正常调试，不是死循环。
package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
)

// RepeatNoticePrefix 标注「这条消息是重复调用提醒，不是用户说的」——前端据此渲染成提示条
// 而不是用户气泡（与 protocol.JobNoticePrefix 同一套机制，见 notify.go）。
//
// 为什么常量放在 agent 而不是 protocol：分层守卫禁止 agent import protocol（AGENTS.md §4）。
// 前端的同名常量由 frontend/tests 的对照测试钉住逐字一致。
const RepeatNoticePrefix = "[重复调用提醒] "

const (
	repeatGentle = 3 // 第一次提醒（轻）
	repeatFirm   = 5 // 第二次提醒（点名工具/次数/参数）
	repeatStop   = 8 // 停止：两轮升级提醒都没用，再跑就是纯浪费
)

// repeatGuard 记「连续几轮是同一批调用」。作用域 = 一轮用户消息（runTurn 的局部变量）：
// 新的一轮用户消息重新开始计数——用户又说话了，那是新的上下文。
type repeatGuard struct {
	key   string
	count int
}

// observe 记一轮工具调用，返回该在**下一个轮边界**注入的提醒（空 = 不注入）；
// stop = 判定死循环，本轮直接停止。
func (g *repeatGuard) observe(calls []llm.ToolCall) (hint string, stop bool) {
	key := repeatKey(calls)
	if key == "" {
		g.key, g.count = "", 0
		return "", false
	}
	if key == g.key {
		g.count++
	} else {
		g.key, g.count = key, 1
	}
	switch g.count {
	case repeatGentle:
		return RepeatNoticePrefix + "你在重复完全相同的工具调用，参数一个字都没变。" +
			"先仔细看上一次的结果再决定下一步——同样的调用不会给出不同的结果。", false
	case repeatFirm:
		return fmt.Sprintf("%s检测到重复调用：\n- 工具：%s\n- 连续次数：%d\n- 参数：%s\n\n"+
			"这些调用没有产生新进展。换一个思路，或者把已经得到的结论整理出来收尾。",
			RepeatNoticePrefix, repeatNames(calls), g.count, repeatPreview(calls)), false
	case repeatStop:
		return fmt.Sprintf("连续 %d 次完全相同的工具调用，判定为死循环，已停止本轮。"+
			"（这不是轮数上限——正常的长任务不受影响；停止是因为同一批调用一字不差地重复了 %d 次）",
			g.count, g.count), true
	}
	return "", false
}

// 会话级重复防护（P7，2026-12 用户拍板）的阈值与容量。数据依据：真库 6,564 次
// 参数逐字相同的冗余调用，28 组 ≥50 次集中在 6 个会话（4,437 次）；极端样本是
// 同一条 grep 命令 1,206 次、8 条 sed -n 各 160 次。上面的 repeatGuard 只认
// 「连续同一批」（作用域 = 单轮 runTurn 的局部变量，跨轮重置），跨轮重复全放过
// ——这一层是**会话级**的第二层防线。
//
// 阈值取刻意宽松的 10/25：文件刚改完重跑同一条命令是合法流程（「改了再看」），
// 这一层**绝不阻断**（不设置 stop）——stop 只属于上面的连续守卫。10 次起软提醒
// （「结果不会变」），25 次起硬提醒并点名：连续守卫漏掉的（改过参数换轮次绕开的）
// 长尾在这里被抓到，且 25 次时「还在做同一件事」的结论已经足够可靠。
const (
	sessionRepeatSoft = 10
	sessionRepeatHard = 25
	// sessionRepeatLRUCap 是会话级 key 记录的容量上限（内存纪律）：超限逐出
	// 最久未见的 key。一个 key 只花「名字+参数」级别的内存，200 条足够覆盖
	// 一次真实会话的调用面；真跑出 200+ 个不同 key 的会话，第 201 个被逐出的
	// key 重新攒计数也只是提醒晚到几轮——防护本来就是软提醒。
	sessionRepeatLRUCap = 200
)

// repeatHintFor 按会话级累计次数生成**前缀注入**文案（注入点 = 工具结果前缀，
// 见 runTools）。
//
// 阈值语义是「第 N 次起」：n >= 阈值硬提醒（点名次数，模型必须正视）；n >= 软阈值
// 软提醒；之间不打扰——每次结果都带一句"重复了"本身就是在灌上下文。
//
// sleep 类调用收紧（2026-12，真库取证驱动）：sleep 命令全库 5,349 次/89.4h，
// 其中验收子会话 sleep 变体（`sleep 280; echo t12` → `t13`）连续 53.5 分钟——
// 参数里的 echo 尾巴每次都在变，逐字归一后是**不同调用**，通用阈值（10/25）
// 根本抓不到。sleep 类收紧到 3/8：sleep 本身不产生信息，第 3 次就该提醒
// 「这不是等待」，第 8 次点名劝止。isSleep 由 observe 按 key 判定——
// 收紧阈值只作用于 sleep 类调用，普通调用照走 10/25。
func repeatHintFor(n int, isSleep bool) string {
	if isSleep {
		if n >= sessionSleepHard {
			return fmt.Sprintf("%s该 sleep 类调用（含变体）本会话已执行 %d 次。sleep 不检查任何状态、纯烧轮次，不是等待——"+
				"等后台任务用 job_output(wait=true)（上限 600s），等外部状态用 run_in_background 起轮询循环。停下来换正确的等待方式。",
				RepeatNoticePrefix, n)
		}
		if n >= sessionSleepSoft {
			return fmt.Sprintf("%s该 sleep 类调用（含变体）本会话已执行 %d 次。sleep 不会带来新信息——"+
				"等后台任务用 job_output(wait=true)，等外部状态用 run_in_background 起轮询循环（until cond; do sleep 30; done）。",
				RepeatNoticePrefix, n)
		}
		return ""
	}
	switch {
	case n >= sessionRepeatHard:
		return fmt.Sprintf("%s该调用本会话已执行 %d 次（工具与参数完全相同）。你很可能在原地打转："+
			"若相关文件没有变化，结果不会变。停下来整理已得到的结论，或换一个能推进任务的明确动作——不要再发起这个调用。",
			RepeatNoticePrefix, n)
	case n >= sessionRepeatSoft:
		return fmt.Sprintf("%s该调用本会话已执行 %d 次，若相关文件未变化，结果不会变。"+
			"确认你在利用之前的结果，而不是机械重跑。",
			RepeatNoticePrefix, n)
	}
	return ""
}

// sleep 类调用的收紧阈值（3 软 / 8 硬）：sleep 不产生信息，比通用阈值严得多。
// 判定与键归一见 sleepCallKey。
const (
	sessionSleepSoft = 3
	sessionSleepHard = 8
)

// sessionRepeatGuard 记「本会话每个 callKey 出现过多少次」。callKey =
// 工具名 + **归一化参数**（键序无关，见 normalizeCallKey——与连续守卫的
// 「逐字比较」刻意不同：连续守卫要区分「改了参数再试」，而会话级层抓的是
// 「同一个调用反复重跑」，键序抖动的同一参数理应算同一个调用）。
//
// 为什么挂在 Session 上（不是 runTurn 局部变量）：跨轮持久——runTurn 每轮
// 新建 repeatGuard，跨轮重复正好从它的缝里漏掉。并发：同一会话的工具调用
// 串行执行（runTools 阶段一顺序），并行 dispatch 不经过本守卫（子会话是
// 独立会话，各有自己的 guard），mu 只是防御性的。
//
// 内存：LRU 容量 sessionRepeatLRUCap，超限逐出最久未见的 key。
type sessionRepeatGuard struct {
	mu      sync.Mutex
	cap     int
	counts  map[string]int
	order   []string // 按 key 首次出现后的**最近访问**排序（尾部 = 最近）
	inOrder map[string]int
}

func newSessionRepeatGuard() *sessionRepeatGuard {
	return &sessionRepeatGuard{
		cap:     sessionRepeatLRUCap,
		counts:  map[string]int{},
		inOrder: map[string]int{},
	}
}

// observe 记一次调用并返回注入前缀（空 = 不注入）。
//
// sleep 类调用（bash 命令实质是 sleep，含尾随 echo 变体）走收紧阈值与
// 变体归一键（sleepRepeatKey）：`sleep 280; echo t12` 与 `sleep 280; echo t13`
// 是同一个调用——参数尾巴每次在变只是「换个序号继续盲等」，逐字归一会把它
// 当成新调用放过去（真库实锤：验收子会话 sleep 变体连续 53.5 分钟，通用
// 阈值根本抓不到）。
func (g *sessionRepeatGuard) observe(name, args string) string {
	if g == nil {
		return ""
	}
	key := name + "\x1e" + normalizeCallKey(args)
	// sleep 类调用共用一个变体 key（echo 尾巴不参与计数）+ 收紧阈值判定。
	// bash_readonly 与 bash 共用执行核，同受特例约束。
	isSleep := false
	if name == "bash" || name == "bash_readonly" {
		if sleepVariantKey(normalizeCallKey(args)) != "" {
			key = "bash\x1esleep-variant"
			isSleep = true
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if n, ok := g.counts[key]; ok {
		g.counts[key] = n + 1
		g.touchLocked(key)
		return repeatHintFor(n+1, isSleep)
	}
	// 新 key：先逐出再插入（容量纪律）。
	for len(g.counts) >= g.cap && len(g.order) > 0 {
		evict := g.order[0]
		g.order = g.order[1:]
		delete(g.counts, evict)
		delete(g.inOrder, evict)
	}
	g.counts[key] = 1
	g.order = append(g.order, key)
	g.inOrder[key] = len(g.order) - 1
	return ""
}

// touchLocked 把 key 挪到 order 尾部（LRU 的「最近使用」）。已在尾部时不动
//（常见路径，避免每步都 O(n) 重排——尾部命中直接返回）。
func (g *sessionRepeatGuard) touchLocked(key string) {
	i, ok := g.inOrder[key]
	if !ok {
		return
	}
	if i == len(g.order)-1 {
		return
	}
	g.order = append(g.order[:i], g.order[i+1:]...)
	g.order = append(g.order, key)
	g.inOrder[key] = len(g.order) - 1
}

// normalizeCallKey 把参数 JSON 规范化成键序无关的签名。
//
// 为什么会话级层做归一而连续守卫不做：两层回答不同的问题——连续守卫回答
// 「是不是同一批调用**一字不差**地又来了一遍」（模型改参数再试是调试，要重新
// 计数）；会话级层回答「**语义上同一个**调用是不是跑了太多次」（6,564 次冗余
// 调用里相当一部分只是 JSON 键序或空白抖动）。参数不是合法 JSON 时退回逐字
// 比较（trim 后）——没法归一就别硬归一，宁可漏判不可错判。
//
// 太长的参数截断到 512 字节（key 内存纪律；长参数（write_file 的 content）
// 截断后仍能区分绝大多数调用——前 512 字节相同而语义不同的调用极其罕见，
// 而 write_file 反复全量重写正是要抓的形态）。
func normalizeCallKey(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return ""
	}
	if !json.Valid([]byte(args)) {
		if len(args) > callKeyArgCap {
			return args[:callKeyArgCap]
		}
		return args
	}
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return args
	}
	norm, err := json.Marshal(v)
	if err != nil {
		return args
	}
	s := string(norm)
	if len(s) > callKeyArgCap {
		s = s[:callKeyArgCap]
	}
	return s
}

const callKeyArgCap = 512

// sleepVariantKey 判断归一化后的 bash 参数是否是「sleep 类变体调用」：
// 命令实质是 sleep（可带任意 echo 尾巴——尾巴在归一键里就是变体）→ 返回
// 非空标记（observe 里所有 sleep 变体共用同一个 key）；否则返回空串。
//
// 判定复用 tools 包的硬拦核心（纯 sleep 判据 + 去尾随 echo），两处共用同一份
// 语义：被硬拦拒掉的形态、放行前的盲等形态、echo 变体形态，在这里都算同一类。
//
// 参数形状：normalizeCallKey 已归一成紧凑 JSON（`{"command":"sleep 280; echo t13"}`）。
// 不是合法 JSON / 没有 command 字段 → 空串（按普通调用走通用阈值）。
func sleepVariantKey(args string) string {
	var a struct {
		Command         string `json:"command"`
		RunInBackground bool   `json:"run_in_background"`
	}
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return ""
	}
	if a.Command == "" || a.RunInBackground {
		return "" // 后台 sleep 是轮询循环的合法组件，不算盲等
	}
	// 去掉尾随 echo 后核心是纯 sleep → 是变体调用。纯 sleep 不带 echo 的
	// 形态逐字相同、本来就会被通用守卫抓到，但归进同一个 key 语义更一致：
	// 3 次就提醒比等 10 次合理（sleep 不产生信息）。
	if core := strings.TrimSpace(tools.StripTrailingEcho(strings.TrimSpace(a.Command))); core != "" && tools.IsPureSleep(core) {
		return "sleep-variant"
	}
	return ""
}

// repeatKey 把一轮的调用拼成签名：全部工具名 + 参数（逐字）。空轮 = 空串（不参与判定）。
func repeatKey(calls []llm.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	var b strings.Builder
	for i, c := range calls {
		if i > 0 {
			b.WriteByte('\x1f')
		}
		b.WriteString(c.Function.Name)
		b.WriteByte('\x1e')
		b.WriteString(c.Function.Arguments)
	}
	return b.String()
}

func repeatNames(calls []llm.ToolCall) string {
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, c.Function.Name)
	}
	return strings.Join(names, ", ")
}

// repeatPreview 截断参数（提醒里带全文可能很长，而这条提醒本身也会进上下文）。
func repeatPreview(calls []llm.ToolCall) string {
	var parts []string
	for _, c := range calls {
		a := c.Function.Arguments
		if len(a) > 300 {
			a = a[:300] + "…（已截断）"
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " | ")
}
