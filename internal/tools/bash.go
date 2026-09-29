package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/moyunteng/lxcode/internal/jobs"
)

const (
	bashDefaultTimeout = 60 * time.Second
	bashMaxTimeout     = 300 * time.Second
	bashMaxOutput      = 32 * 1024
	bashMaxStdin       = 64 * 1024 // 脚本体上限：够写几百行，又不至于把上下文当文件系统用
)

// selectShell 按平台选择命令 shell：Unix 用 sh；Windows 优先 Git Bash
// （模型对 bash 语法最熟，训练语料几乎都按 bash 写），找不到才退 cmd.exe。
// 返回（可执行名, 命令标志）：bash/sh 用 -c，cmd 用 /c。
// 构造时调用一次并固化——Exec 里每条命令不再做 PATH 探测。
//
// Windows 陷阱（本机实测踩过）：System32 里的 bash.exe 是 WSL 的启动 stub，
// 没装 WSL 发行版时它不执行命令、只打印 UTF-16 编码的安装提示并退出 1——
// LookPath("bash.exe") 在装了 Git 的机器上也多半先命中它（System32 在 PATH
// 前列）。所以候选顺序是：sh.exe（Git Bash 提供，无 stub 问题）→ 不在系统
// 目录下的 bash.exe → 常见 Git Bash 安装路径 → cmd。
func selectShell() (name, flag string) {
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("sh.exe"); err == nil {
			return p, "-c"
		}
		if p, err := exec.LookPath("bash.exe"); err == nil && !isUnderSystemDir(p) {
			return p, "-c"
		}
		for _, p := range []string{
			`C:\Program Files\Git\bin\bash.exe`,
			`C:\Program Files (x86)\Git\bin\bash.exe`,
		} {
			if _, err := os.Stat(p); err == nil {
				return p, "-c"
			}
		}
		return "cmd", "/c"
	}
	return "sh", "-c"
}

// isUnderSystemDir 判断路径是否位于系统目录（%SystemRoot% 之下）——
// System32\bash.exe 是 WSL stub，不是可用的 bash。
func isUnderSystemDir(p string) bool {
	root := os.Getenv("SystemRoot")
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "..")
}

// shellSyntaxHint 给描述补充所选 shell 的语法差异提示——只对 cmd.exe 需要：
// 模型默认按 bash 写命令（$VAR、$( )、单引号分组），在 cmd 下会原样失败。
func shellSyntaxHint(shellName string) string {
	if strings.Contains(filepath.Base(strings.ToLower(shellName)), "cmd") {
		return "注意：本机 shell 是 cmd.exe——不要用 bash 专属语法（$VAR、$( )、反引号、单引号分组），" +
			"环境变量写 %VAR%，字符串用双引号，&& 与 || 可用。"
	}
	return ""
}

// bashDef：bash，风险等级 高危——重启主机、删数据、改配置都能经它发生，
// 所以一律人工确认。超时默认 60s（模型可用 timeout_sec 调整，上限 300s），
// 输出上限 32KB。
//
// 为什么加 cwd / stdin 两个参数：
//   - 没有 cwd，模型每写一条命令都得塞绝对路径或 `cd x && ...`，路径稍长就挤占
//     上下文，还容易写错；
//   - 没有 stdin，多行脚本只能挤进一个字符串参数、层层转义（嵌套 shell 时
//     反复出错）。stdin 走 cmd.Stdin，是真正的 stdin 而不是 `<<<`（bash 扩展）。
//
// 平台差异（selectShell）：Windows 优先 Git Bash（bash.exe），退 cmd.exe——
// 描述与语法提示按实际选中的 shell 生成，模型不会拿着 bash 语法去撞 cmd。
//
// run_in_background=true 时走后台任务（docs/jobs.md §3）：起进程后**立刻返回**
// job id，输出接进 jobs.Manager，收尾由 producer settle。确认门与前台完全一致
// ——它由 agent 的权限门在 Exec 之前调用，批准了才走到这里；跑起来之后不再弹
// 确认（后台任务没有"当前轮"可挂，这是 auto 档的语义边界）。
func bashDef(r *Registry) *Def {
	shellName, shellFlag := selectShell()
	shellDisp := filepath.Base(shellName)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {"type": "string", "description": "要执行的 shell 命令；多行脚本建议用 stdin 传入"},
			"cwd": {"type": "string", "description": "工作目录（绝对或相对会话工作目录，可选）；默认会话工作目录（无则为进程当前目录）"},
			"stdin": {"type": "string", "description": "喂给命令的标准输入（可选，上限 64KB）：可写多行脚本或 heredoc 体，避免引号转义"},
			"timeout_sec": {"type": "integer", "description": "超时秒数，默认 60，上限 300"},
			"run_in_background": {"type": "boolean", "description": "true 时立刻返回 job id（长命令：构建、测试、dev server）"}
		},
		"required": ["command"]
	}`)
	return &Def{
		Name: "bash",
		Description: "在本机执行 shell 命令（" + shellDisp + "），返回合并的 stdout/stderr 与退出码。" +
			"适合查看状态、文件操作、构建测试、系统管理。多行脚本请用 stdin 传（别在 command 里堆引号）。" +
			shellSyntaxHint(shellDisp),
		Parameters: schema,
		Risk:       RiskHigh,
		Mutates:    true, // 执行命令即变更外部世界（strict 只读模式拒绝）
		Confirm: func(ctx context.Context, args json.RawMessage) string {
			var a struct {
				Command         string `json:"command"`
				Cwd             string `json:"cwd"`
				Stdin           string `json:"stdin"`
				RunInBackground bool   `json:"run_in_background"`
			}
			if err := json.Unmarshal(args, &a); err != nil || a.Command == "" {
				return "执行 shell 命令（参数不完整，建议拒绝）"
			}
			prompt := "将执行命令: " + a.Command
			if a.Cwd != "" {
				// 确认卡展示解析后的真实目录（相对路径按会话工作目录）
				prompt += "\n工作目录: " + resolveToolPath(ctx, a.Cwd)
			}
			if a.Stdin != "" {
				prompt += fmt.Sprintf("\n标准输入: %d 字节\n%s", len(a.Stdin), clip(a.Stdin, 300))
			}
			if a.RunInBackground {
				prompt += "\n（后台运行：批准后立即返回任务 id，不再有后续确认）"
			}
			return prompt
		},
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Command         string `json:"command"`
				Cwd             string `json:"cwd"`
				Stdin           string `json:"stdin"`
				TimeoutSec      int    `json:"timeout_sec"`
				RunInBackground bool   `json:"run_in_background"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			if a.Command == "" {
				return "", fmt.Errorf("command 不能为空")
			}
			// 系统层硬校验：cwd 必须解析为存在的目录（相对路径按会话工作
			// 目录解析），别让模型靠 sh 报错猜
			if a.Cwd != "" {
				cwd := resolveToolPath(ctx, a.Cwd)
				if !filepath.IsAbs(cwd) {
					var err error
					cwd, err = filepath.Abs(cwd)
					if err != nil {
						return "", fmt.Errorf("cwd 无法解析为绝对路径: %s (%v)", a.Cwd, err)
					}
				}
				st, err := os.Stat(cwd)
				if err != nil {
					return "", fmt.Errorf("cwd 不存在或不可访问: %s (%v)", cwd, err)
				}
				if !st.IsDir() {
					return "", fmt.Errorf("cwd 不是目录: %s", cwd)
				}
				a.Cwd = cwd
			} else if wd := WorkDir(ctx); wd != "" {
				// 模型未指定目录：默认会话工作目录（项目会话 = 项目根），
				// 免得每条命令都塞绝对路径或 cd 前缀
				a.Cwd = wd
			}
			if len(a.Stdin) > bashMaxStdin {
				return "", fmt.Errorf("stdin 超过 %dKB 上限（当前 %d 字节）；大文件请分块或用 read_file/write_file",
					bashMaxStdin/1024, len(a.Stdin))
			}
			// 后台任务走独立路径：不等它、不跟本轮 ctx 的生命周期（后台任务
			// 的意义就是活过这一轮）
			if a.RunInBackground {
				return startBackground(r, ctx, shellName, shellFlag, a.Command, a.Cwd, a.Stdin, a.TimeoutSec)
			}
			timeout := bashDefaultTimeout
			if a.TimeoutSec > 0 {
				timeout = time.Duration(min(a.TimeoutSec, int(bashMaxTimeout/time.Second))) * time.Second
			}
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			cmd := exec.CommandContext(cctx, shellName, shellFlag, a.Command)
			// 取消 = 杀**整棵进程树**：默认的 Cancel 只 Process.Kill 直接子进程，
			// 而 sh -c 拉起的孙进程会活下来（见 proctree_*.go 的实测记录）
			configureProcTree(cmd)
			cmd.Cancel = func() error { return killProcessTree(cmd.Process) }
			cmd.WaitDelay = procKillGrace
			if a.Cwd != "" {
				cmd.Dir = a.Cwd
			}
			if a.Stdin != "" {
				cmd.Stdin = strings.NewReader(a.Stdin)
			}
			out, err := cmd.CombinedOutput()
			result := string(out)
			if len(result) > bashMaxOutput {
				result = result[:bashMaxOutput] + "\n…（输出超 32KB 已截断）"
			}
			// 非零退出码与超时是"结果"不是执行错误——回填文本让模型自行判断
			note := execNote(cctx, err, timeout)
			if result == "" && note == "" {
				return "（无输出，退出码 0）", nil
			}
			return result + note, nil
		},
	}
}

// execNote 把执行错误翻译成回填模型的说明文本（bash 与自定义工具共用，
// 两者的失败语义必须一致——否则模型对同一个退出码要学两套说法）。
//
// 非零退出码与超时是"结果"不是执行错误：回填文本让模型自行判断。
// 启动失败（可执行文件不存在/无权限）才是真错误，同样以文本回填——
// 模型能据此换方法，比抛错中断整轮更有用。
func execNote(cctx context.Context, err error, timeout time.Duration) string {
	switch {
	case err == nil:
		return ""
	case cctx.Err() == context.DeadlineExceeded:
		// 取消走的是整棵进程树（见 proctree_*.go），所以不必再提示
		// 「后台子进程可能还在跑」——消灭那个情形正是这次修复的目的
		return fmt.Sprintf("\n[超时：命令超过 %s 被终止（含它拉起的子进程）]", timeout)
	case errors.Is(err, exec.ErrWaitDelay):
		// 进程已退出，但某个逃逸的孙进程还攥着输出管道：WaitDelay 兜底收尾。
		// 这不是命令失败，如实说明比报「启动失败」有用
		return "\n[输出管道被残留子进程占用，已停止读取]"
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Sprintf("\n[退出码 %d]", exitErr.ExitCode())
		}
		return "\n[启动失败: " + err.Error() + "]"
	}
}

// startBackground 起一个后台任务并**立刻**返回（docs/jobs.md §3）。
//
// 与前台路径的差别只有"不等"：同一个 shell 选择、同样的 cwd 解析、同样的
// stdin 限制。确认门在 Exec 之前已经走完（批准了才走到这里）。
//
// 三条纪律：
//  1. 用 context.Background() 而不是父轮的 ctx——后台任务的意义就是**活过这一轮**，
//     跟着轮次的取消一起死等于白起（用户按停止不该杀掉 dev server）；
//  2. 默认**不设超时**（长构建 / dev server 正是它存在的理由）；模型显式给
//     timeout_sec 时才加限制，超时按"被杀"收尾且 Detail=jobs.TimeoutDetail——
//     唤醒投递据此说「超时」而不是「失败」；
//  3. 归属由 producer 定稿：进程正常退出 = EndedSelf，Kill 已请求过则沿用
//     Manager 记下的 by（user/agent/backend）——这正是契约要修的 DSH 缺陷
//     （谁结束的在数据里必须可区分）。
func startBackground(r *Registry, parent context.Context, shellName, shellFlag, command, cwd, stdin string, timeoutSec int) (string, error) {
	mgr := r.getJobs()
	if mgr == nil {
		return "", fmt.Errorf("后台任务未装配（后端未初始化任务管理器）")
	}
	j, err := mgr.Start(jobs.Spec{
		Kind: "bash", Label: commandLabel(command), SessionID: SessionID(parent),
		// 时间线归属：子 Agent 起的任务挂在父会话上（通告投给父会话才有人能
		// 行动）。OwnerOf 会在为空时回落 SessionID——顶层会话起的任务 owner
		// 就是它自己，行为与以前逐字节一致。
		OwnerSessionID: OwnerSessionID(parent),
	})
	if err != nil {
		return "", fmt.Errorf("启动后台任务失败: %w", err)
	}
	base := context.Background()
	cctx, cancel := context.WithCancel(base)
	if timeoutSec > 0 {
		cctx, cancel = context.WithTimeout(base,
			time.Duration(min(timeoutSec, int(bashMaxTimeout/time.Second)))*time.Second)
	}
	cmd := exec.CommandContext(cctx, shellName, shellFlag, command)
	// 与前台同款：取消要杀掉整棵树，且给 I/O 一个兜底期限
	configureProcTree(cmd)
	cmd.Cancel = func() error { return killProcessTree(cmd.Process) }
	cmd.WaitDelay = procKillGrace
	if cwd != "" {
		cmd.Dir = cwd
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	// stdout 与 stderr 都进同一个任务句柄：合并输出与前台 bash 的
	// CombinedOutput 语义一致（模型读到的顺序就是它真实发生的顺序）。
	cmd.Stdout, cmd.Stderr = j, j
	if cr, ok := j.(jobs.CancelRegistrar); ok {
		cr.SetCancel(cancel)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		j.Settle(jobs.StatusFailed, jobs.EndedSelf, "启动失败: "+err.Error())
		return "", fmt.Errorf("后台任务启动失败: %w", err)
	}
	go func() {
		werr := cmd.Wait()
		// 先取 ctx.Err 再 cancel：cancel 之后它恒为 Canceled，
		// 「超时」与「被 kill」就分不出来了
		ctxErr := cctx.Err()
		cancel()
		settleBackground(j, ctxErr, normalizeWaitErr(cmd, werr))
	}()
	return backgroundStartedText(j.ID(), command), nil
}

// settleBackground 给后台任务定稿（producer 是唯一知道退出码的人）。
func settleBackground(j jobs.Job, ctxErr, waitErr error) {
	by := j.Snapshot().EndedBy
	switch {
	case ctxErr == context.DeadlineExceeded:
		j.Settle(jobs.StatusKilled, jobs.EndedSelf, jobs.TimeoutDetail)
	case by != "":
		// Kill 已被请求（用户/agent/后端）：归属**保持原样**，只把状态定稿。
		// 这里若写 EndedSelf 就把"谁结束的"抹平了——那正是本功能要修的缺陷。
		j.Settle(jobs.StatusKilled, by, "已取消")
	case waitErr == nil:
		j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")
	default:
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			j.Settle(jobs.StatusFailed, jobs.EndedSelf, fmt.Sprintf("退出码 %d", exitErr.ExitCode()))
		} else {
			j.Settle(jobs.StatusFailed, jobs.EndedSelf, "启动失败: "+waitErr.Error())
		}
	}
}

// normalizeWaitErr 把 exec.ErrWaitDelay 还原成进程的真实退出状态。
//
// ErrWaitDelay 说的是「I/O 管道没在期限内关掉」，**不是**「进程失败了」：一个
// 成功退出、但留下了攥着管道的孙进程的命令也会拿到它。不还原的话，一个正常结束
// 的后台任务会被记成 StatusFailed（真实退出码丢失），结束通告的措辞跟着错。
func normalizeWaitErr(cmd *exec.Cmd, err error) error {
	if !errors.Is(err, exec.ErrWaitDelay) {
		return err
	}
	ps := cmd.ProcessState
	switch {
	case ps == nil:
		return err
	case ps.Success():
		return nil
	default:
		return &exec.ExitError{ProcessState: ps}
	}
}

// backgroundStartedText 是立刻返回给模型的文本：job id + 怎么读输出。
// 必须显式说「不要在这里等」——否则模型会立刻 job_output(wait=true)，
// 把后台任务当同步命令用（那就白起了）。
func backgroundStartedText(id, command string) string {
	return fmt.Sprintf("后台任务已启动: %s\n命令: %s\n"+
		"它是异步的——现在不要等它，可以继续做别的事；结束后系统会通知你结果。\n"+
		"  job_output(job_id=%q)             读增量输出（无新输出回 (no new output)）\n"+
		"  job_output(job_id=%q, wait=true)  阻塞等它结束（默认 30s，上限 600s）\n"+
		"  job_list                         看全部任务；job_kill(job_id=%q) 停止它",
		id, commandLabel(command), id, id, id)
}

// commandLabel 生成任务的一行摘要（UI 卡片与唤醒通告都用它）。
func commandLabel(command string) string {
	s := strings.TrimSpace(command)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i]) + " …"
	}
	if s == "" {
		return "(空命令)"
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// clip 截断文本用于确认卡展示（保留前若干字节，便于人读）。
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…（已截断显示）"
}
