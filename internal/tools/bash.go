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
func bashDef() *Def {
	shellName, shellFlag := selectShell()
	shellDisp := filepath.Base(shellName)
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {"type": "string", "description": "要执行的 shell 命令；多行脚本建议用 stdin 传入"},
			"cwd": {"type": "string", "description": "工作目录（绝对或相对会话工作目录，可选）；默认会话工作目录（无则为进程当前目录）"},
			"stdin": {"type": "string", "description": "喂给命令的标准输入（可选，上限 64KB）：可写多行脚本或 heredoc 体，避免引号转义"},
			"timeout_sec": {"type": "integer", "description": "超时秒数，默认 60，上限 300"}
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
				Command string `json:"command"`
				Cwd     string `json:"cwd"`
				Stdin   string `json:"stdin"`
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
			return prompt
		},
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Command    string `json:"command"`
				Cwd        string `json:"cwd"`
				Stdin      string `json:"stdin"`
				TimeoutSec int    `json:"timeout_sec"`
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
			timeout := bashDefaultTimeout
			if a.TimeoutSec > 0 {
				timeout = time.Duration(min(a.TimeoutSec, int(bashMaxTimeout/time.Second))) * time.Second
			}
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			cmd := exec.CommandContext(cctx, shellName, shellFlag, a.Command)
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
		// 只杀得掉直接子进程（sh/exe），它拉起的后台子进程可能还在跑——如实告知模型
		return fmt.Sprintf("\n[超时：命令超过 %s 被终止；被它拉起的后台子进程可能仍在运行]", timeout)
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Sprintf("\n[退出码 %d]", exitErr.ExitCode())
		}
		return "\n[启动失败: " + err.Error() + "]"
	}
}

// clip 截断文本用于确认卡展示（保留前若干字节，便于人读）。
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…（已截断显示）"
}
