// bash 工具的 Windows shell 选择与语法提示（任务 C 的取证结论落成断言）。
//
// 真库取证（2026-12，66193 条工具结果）：
//   - 34335 条 bash 调用，1616 条非零退出码（4.7%），其中 1530 条紧接着又发了
//     一条 bash（模型在试错）；
//   - 12 例 `ERROR: Invalid argument/option - 'C:/Program Files/Git/FI'`（MSYS
//     把 `/FI` 当路径改写）；
//   - 37 例 `/usr/bin/bash.ProcessName`（`$_` 被 sh 先展开）；
//   - 519 条命令里显式写了 `powershell`，其中 58 条失败、19 条输出乱码。
//
// 这里钉的是「我们选的是哪个 shell」与「描述里有没有把这些坑说清楚」——
// 两件事都必须是**按实际选中的 shell** 生成，而不是写死一份。
package tools

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSelectShellNeverPicksWSLStub：Windows 上绝不选系统目录里的 bash.exe
// （那是 WSL 的启动 stub：没装发行版时只打 UTF-16 安装提示并退出 1）。
func TestSelectShellNeverPicksWSLStub(t *testing.T) {
	name, flag := selectShell()
	if runtime.GOOS != "windows" {
		if flag != "-c" {
			t.Fatalf("Unix 应走 -c，实际 %q", flag)
		}
		return
	}
	base := strings.ToLower(filepath.Base(name))
	if base == "cmd" || base == "cmd.exe" {
		if flag != "/c" {
			t.Fatalf("cmd 应走 /c，实际 %q", flag)
		}
		return
	}
	if flag != "-c" {
		t.Fatalf("bash/sh 应走 -c，实际 %q", flag)
	}
	if isUnderSystemDir(name) {
		t.Fatalf("选到了系统目录里的 shell（WSL stub 风险）: %s", name)
	}
}

// TestShellSyntaxHintForGitBash 钉住真库取证的两类高频失败都被写进描述：
// MSYS 路径改写与 `$_` 展开。模型每次请求都读得到，比试错便宜。
func TestShellSyntaxHintForGitBash(t *testing.T) {
	hint := shellSyntaxHint(`C:\Program Files\Git\usr\bin\sh.exe`)
	if runtime.GOOS != "windows" {
		if hint != "" {
			t.Fatalf("非 Windows 不该有 Windows shell 提示: %q", hint)
		}
		return
	}
	for _, want := range []string{"Git Bash", "MSYS_NO_PATHCONV", "$_", "//FI"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("Git Bash 提示缺少 %q: %s", want, hint)
		}
	}
	// 不能把 cmd 的语法说明混进来（两套规则互斥，混着说模型会两边都试）
	if strings.Contains(hint, "%VAR%") {
		t.Fatalf("Git Bash 提示不该混入 cmd 的 %%VAR%% 写法: %s", hint)
	}
}

// TestShellSyntaxHintForCmd 钉住 cmd 分支没被 Git Bash 分支吃掉。
func TestShellSyntaxHintForCmd(t *testing.T) {
	hint := shellSyntaxHint(`C:\Windows\System32\cmd.exe`)
	for _, want := range []string{"cmd.exe", "%VAR%"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("cmd 提示缺少 %q: %s", want, hint)
		}
	}
	if strings.Contains(hint, "MSYS_NO_PATHCONV") {
		t.Fatalf("cmd 提示不该混入 MSYS 说明: %s", hint)
	}
}

// TestBashDescriptionMatchesSelectedShell：描述里的 shell 名与提示必须来自
// 实际选中的那个（写死一份会在换机器/换 PATH 时静默说错）。
func TestBashDescriptionMatchesSelectedShell(t *testing.T) {
	d := bashDef(New())
	name, _ := selectShell()
	disp := filepath.Base(name)
	if !strings.Contains(d.Description, disp) {
		t.Fatalf("描述应点名实际 shell %q: %s", disp, d.Description)
	}
}
