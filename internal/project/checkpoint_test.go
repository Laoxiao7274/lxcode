// 检查点提交信息规则的用例：类型/scope/摘要/正文的纯函数判定。
package project

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestCheckpointMessageRules 钉住「类型关键词优先、scope 取改动最多的顶层目录、
// 摘要去寒暄截断、正文带 [checkpoint] 与改动统计」这四条规则。
func TestCheckpointMessageRules(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		files  []FileChange
		typ    string
		scope  string
		substr string
	}{
		{"修复关键词", "修一下设置页的闪退", []FileChange{{Path: "frontend/src/App.tsx", Added: 3, Deleted: 1}}, "fix", "frontend", "修一下设置页的闪退"},
		{"文档关键词", "更新 AGENTS.md 的说明", []FileChange{{Path: "internal/a.go", Added: 1}}, "docs", "internal", "更新 AGENTS.md 的说明"},
		{"重构关键词", "重构一下提交逻辑", []FileChange{{Path: "internal/server/a.go", Added: 1}}, "refactor", "internal", "重构一下提交逻辑"},
		{"纯文档改动", "看看这个", []FileChange{{Path: "docs/a.md", Added: 1}}, "docs", "docs", "看看这个"},
		{"纯测试改动", "补一下", []FileChange{{Path: "internal/server/a_test.go", Added: 1}}, "test", "internal", "补一下"},
		{"默认 feat", "加一个按钮", []FileChange{{Path: "frontend/src/App.tsx", Added: 2}, {Path: "frontend/src/b.ts", Added: 1}, {Path: "internal/x.go", Added: 1}}, "feat", "frontend", "加一个按钮"},
		{"去寒暄前缀", "然后加一个按钮", nil, "feat", "", "加一个按钮"},
		{"无原话回落文件数", "", []FileChange{{Path: "internal/a.go", Added: 1}, {Path: "internal/b.go", Added: 1}}, "feat", "internal", "更新 2 个文件"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			subject, body := CheckpointMessage(7, c.prompt, c.files)
			wantPrefix := c.typ + ":"
			if c.scope != "" {
				wantPrefix = c.typ + "(" + c.scope + "):"
			}
			if !strings.HasPrefix(subject, wantPrefix+" ") {
				t.Fatalf("标题前缀 = %q, want %q", subject, wantPrefix)
			}
			if !strings.Contains(subject, c.substr) {
				t.Fatalf("标题应含 %q: %q", c.substr, subject)
			}
			if !strings.HasPrefix(body, "[checkpoint] 第 7 轮") {
				t.Fatalf("正文首行应为检查点标记: %q", body)
			}
			if c.prompt != "" && !strings.Contains(body, "用户原话：") {
				t.Fatalf("正文应保留用户原话: %q", body)
			}
			if len(c.files) > 0 && !strings.Contains(body, "改动：") {
				t.Fatalf("正文应有改动统计: %q", body)
			}
		})
	}
}

// TestCheckpointMessageTruncates 钉住「标题摘要与正文原话都有上限」。
func TestCheckpointMessageTruncates(t *testing.T) {
	long := strings.Repeat("很长的请求", 40)
	subject, body := CheckpointMessage(1, long, []FileChange{{Path: "internal/a.go", Added: 1}})
	if !strings.HasSuffix(subject, "…") {
		t.Fatalf("超长摘要应截断并加省略号: %q", subject)
	}
	if n := len([]rune(strings.TrimPrefix(subject, "feat(internal): "))); n > checkpointSubjectMax+1 {
		t.Fatalf("标题摘要 %d rune 超过上限 %d", n, checkpointSubjectMax)
	}
	if n := len([]rune(body)); n > checkpointPromptMax+200 {
		t.Fatalf("正文 %d rune 超出预期上限", n)
	}
}

// TestParseNumstatRename 钉住 numstat 的两种重命名写法收敛成目标路径。
func TestParseNumstatRename(t *testing.T) {
	out := "1\t2\tdir/{old.txt => new.txt}\n0\t0\ta.txt\n-\t-\tbin/x.exe\n"
	files := parseNumstat(out)
	if len(files) != 3 {
		t.Fatalf("应解析 3 个文件: %+v", files)
	}
	if files[0].Path != "dir/new.txt" {
		t.Fatalf("重命名目标路径 = %q", files[0].Path)
	}
	if files[2].Added != -1 || files[2].Deleted != -1 {
		t.Fatalf("二进制应记 -1: %+v", files[2])
	}
}

// TestCommitCheckpointWritesMessage 端到端：真实 git 仓库里提交后，标题是
// conventional commits 形态、正文带 [checkpoint] 与文件清单；无改动返回 false。
func TestCommitCheckpointWritesMessage(t *testing.T) {
	repo := t.TempDir()
	gitTest(t, repo, "init")
	gitTest(t, repo, "config", "user.name", "Test")
	gitTest(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.MkdirAll(repo+"/internal", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repo+"/internal/a.go", []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	committed, err := CommitCheckpoint(context.Background(), repo, 3, "修复 internal 的编译错误")
	if err != nil || !committed {
		t.Fatalf("CommitCheckpoint: committed=%v err=%v", committed, err)
	}
	subject := strings.TrimSpace(gitTest(t, repo, "log", "-1", "--pretty=%s"))
	if subject != "fix(internal): 修复 internal 的编译错误" {
		t.Fatalf("标题 = %q", subject)
	}
	body := gitTest(t, repo, "log", "-1", "--pretty=%B")
	for _, want := range []string{"[checkpoint] 第 3 轮", "用户原话：", "internal/a.go"} {
		if !strings.Contains(body, want) {
			t.Fatalf("正文缺少 %q:\n%s", want, body)
		}
	}
	t.Logf("实际提交信息：\n%s\n%s", subject, body)
	again, err := CommitCheckpoint(context.Background(), repo, 4, "再来一次")
	if err != nil || again {
		t.Fatalf("干净工作树不该再提交: committed=%v err=%v", again, err)
	}
}
