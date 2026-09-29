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
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/llm"
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
