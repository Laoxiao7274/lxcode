// 纯 sleep 硬拦与等待纪律指引（2026-12，真库取证驱动）。
//
// 数据（全库日志扫描）：sleep 命令 5,349 次共 89.4h；纯盲等（只 sleep+echo、
// 不检查任何状态）630 次/23.3h；≥60s 的 1,871 次占 87%。最刺眼的样本：验收
// 子会话起了一个后台任务（15.8s 就跑完）后又 sleep 累计 53.5 分钟——它没有
// 收到任务结束通告（根因①，jobs 通告单投时间线归属），也没有任何等待手段的
// 指引（根因②③），只能 sleep 瞎猜「该等多久」。
//
// 修复分两层：
//   - 硬拦（这里）：命令**实质**是 sleep N（至多带 echo 尾巴）且非后台 → 不
//     执行，返回可执行的等待替代（等已有任务进度用 job_output(wait=true,
//     wait_condition=…) 等待原语；真的无任务句柄才起 run_in_background 轮询
//     循环）。run_in_background=true 的 sleep 放行——它是轮询循环的合法组件；
//   - 提示（也在这里）：组合命令带 sleep（sleep 300 && curl check）不算纯
//     sleep，放行执行，但把等待纪律附在结果前——研究数据里 sleep+ssh/curl
//     占比很高，那是在「勉强检查状态」，指引它换成等待原语或轮询循环。
//
// 为什么拦在工具层而不是靠提示词：提示词每轮都读得到但模型实测仍大量盲睡
//（守则写上等待纪律只是根因③的补充，不构成防线）——系统层硬拦是唯一不可绕
// 过的一层，与 cwd 校验/只读白名单同一档位的纪律。
package tools

import (
	"regexp"
	"strings"
)

// sleepJobWaitHint 是硬拦时给模型的替代路径（可执行，不是空话）。
const sleepJobWaitHint = "等待的正确姿势（一等原语，别再写 sleep）：\n" +
	"等已有后台任务的进度/结束 → job_output(job_id=…, wait=true, wait_condition=…)（阻塞等待原语：exit 等结束 / exit_ok 等成功 / output 等新输出 / idle 等静默 / {\"pattern\": \"…\"} 等输出命中正则——OR 组合谁先到返回谁，上限 600s）；\n" +
	"真的无任务句柄可等（外部状态）→ 才用 run_in_background=true 起轮询循环" +
	"（如 `until curl -s http://… ; do sleep 30; done`），系统会在任务结束时通知你。" +
	"纯 sleep 不检查任何状态、纯烧轮次，一律不执行。"

// sleepComboHint 是组合命令（sleep + 别的真动作）放行时附在结果前的提示。
const sleepComboHint = "[等待纪律] 命令里带 sleep：等已有后台任务用 job_output(wait=true, wait_condition=…)（等待原语：exit/exit_ok/output/idle/{pattern}，上限 600s）；" +
	"真的无任务句柄才用 run_in_background=true 起轮询循环（until cond; do sleep 30; done）——" +
	"不要用 sleep 预估等待时长。"

// pureSleepCommand 判定「命令实质是纯 sleep」：
//
// 返回 (hint, blocked)：
//   - blocked=true：纯 sleep（可带 `; echo …` 尾巴）→ 硬拦，hint 是替代指引；
//   - blocked=false 且 hint != ""：组合命令带 sleep → 放行，hint 是附加提示；
//   - blocked=false 且 hint == ""：与 sleep 无关，原样放行。
//
// 判定规则（宁可漏判不可错拦——sleep 30 是合法的采样间隔）：
//   - 去掉尾随 `; echo <任意>`（可多条、可分号或 && 连接）后，剩余部分
//     必须整体匹配 `sleep [选项] N [单位]`（N 可多个——`sleep 1 2 3` 是
//     sleep 的合法累计写法，同样算）；
//   - 多条命令、管道、`sleep x && y`、`sleep x; y`（y 非 echo）都不算纯 sleep
//     （它至少干了点别的），走组合提示或原样放行。
func pureSleepCommand(command string) (hint string, blocked bool) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return "", false
	}
	// 尾随 echo 尾巴：`; echo t12` / `&& echo done`（可重复多条）——去掉后看核心。
	core := strings.TrimSpace(StripTrailingEcho(cmd))
	if core == "" {
		return "", false
	}
	if IsPureSleep(core) {
		return sleepJobWaitHint, true
	}
	// 非 sleep 开头：与 sleep 无关（提示只针对「命令里有 sleep」的形态）。
	// 轮询循环本体（`until cond; do sleep 30; done`）不算——sleep 是它的
	// 循环组件而非「用 sleep 预估等待」，那是等待纪律推荐的做法，不该被提示。
	if !containsSleepToken(core) || pollLoopRe.MatchString(core) {
		return "", false
	}
	// 命令里有 sleep 但还有别的动作：放行 + 附加提示。
	return sleepComboHint, false
}

// StripTrailingEcho 去掉尾随的 echo 段（`; echo …` / `&& echo …`，可重复），
// 返回剩余核心。echo 的参数不参与判定——`sleep 280; echo t12` 与
// `sleep 280; echo done` 在这里等价（repeat 守卫的同款归一在 agent 层）。
// 导出给 agent 的 repeat 守卫用（callKey 对 bash 命令做同款归一）。
func StripTrailingEcho(cmd string) string {
	for {
		i := strings.LastIndexAny(cmd, ";&")
		if i < 0 {
			return cmd
		}
		tail := strings.TrimSpace(cmd[i+1:])
		if tail == "" {
			// 末尾是悬空的分隔符（`sleep 60;`）：剥掉再看
			cmd = strings.TrimRight(cmd[:i], ";& \t\n")
			continue
		}
		if tail == "echo" || strings.HasPrefix(tail, "echo ") || strings.HasPrefix(tail, "echo\t") {
			cmd = cmd[:i]
			continue
		}
		return cmd
	}
}

var (
	sleepCmdRe = regexp.MustCompile(`^(?:env\s+\S+=\S+\s+)*sleep(?:\s+-\S+)*(?:\s+(\d+(?:\.\d+)?)([a-z]?))+$`)
	sleepToken = regexp.MustCompile(`(?:^|[;&|\s(])sleep(?:$|[;&|\s)])`)
)

// IsPureSleep 判断核心命令是否就是一条 sleep（可带 env VAR=x 前缀与数字
// 后缀单位；`sleep 60` / `sleep 1.5m` / `env SLEEP=1 sleep 60` 都算）。
// 导出给 agent 的 repeat 守卫（sleep 变体判定与硬拦共用同一份语义）。
func IsPureSleep(core string) bool {
	return sleepCmdRe.MatchString(core)
}

// containsSleepToken 判断命令里是否出现独立的 sleep 词（子串如 sleepy 不算）。
func containsSleepToken(core string) bool {
	return sleepToken.MatchString(core)
}

// pollLoopRe 识别「轮询循环」形态：while/until + do … done（sleep 是循环体
// 的组件）。命令里有 sleep 但长这样 → 放行不提示（这正是等待纪律推荐的做法）。
var pollLoopRe = regexp.MustCompile(`\b(?:while|until)\b[\s\S]*\bdo\b[\s\S]*\bdone\b`)
