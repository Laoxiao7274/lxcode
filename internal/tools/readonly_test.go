// 只读 bash（bash_readonly）的判据钉子（2026-12 用户反馈：调研 Agent 没有
// 执行面，读不了本地数据——查 SQLite 聚合跑不了 sqlite3、看历史跑不了 git log）。
//
// 这一层是**唯一的安全边界**（只读工具没有确认门可依赖），所以放行的每一条
// 都要钉住，拒绝的每一类也都要钉住——放宽一条 = 调研 Agent 拿到一个写入口。
package tools

import (
	"context"
	"strings"
	"testing"
)

// TestBashReadOnlyDefShape：只读 bash 的工具声明——低危 + 不变更外部世界
// （strict 只读档据此放行）、无确认门（只读命令不该让用户逐条点确认）。
func TestBashReadOnlyDefShape(t *testing.T) {
	r := New()
	d, ok := r.Get(bashReadOnlyName)
	if !ok {
		t.Fatalf("注册表缺 %s（调研 Agent 的执行面）", bashReadOnlyName)
	}
	if d.Risk != RiskLow || d.Mutates {
		t.Fatalf("%s 应为低危 + 不变更外部世界（strict 只读档必须可用）: risk=%v mutates=%v",
			bashReadOnlyName, d.Risk, d.Mutates)
	}
	if p := d.Confirm(context.Background(), []byte(`{"command":"ls"}`)); p != "" {
		t.Fatalf("只读命令不该弹确认门: %q", p)
	}
	// 完整 bash 不受影响：仍是高危 + 变更 + 走确认门
	full, _ := r.Get("bash")
	if full.Risk != RiskHigh || !full.Mutates {
		t.Fatalf("完整 bash 应保持高危 + 变更: risk=%v mutates=%v", full.Risk, full.Mutates)
	}
	if p := full.Confirm(context.Background(), []byte(`{"command":"ls"}`)); !strings.Contains(p, "ls") {
		t.Fatalf("完整 bash 仍应走确认门: %q", p)
	}
}

// TestReadOnlyAllowsReadCommands：只读命令放行（含调研的真实需求：查库/看历史/
// 统计规模/管道过滤）。
func TestReadOnlyAllowsReadCommands(t *testing.T) {
	cases := []string{
		"ls -la",
		"cat README.md",
		"wc -l internal/*.go",
		"grep -rn \"timeout\" internal/",
		"find . -name '*.go'",
		"git log --oneline -5",
		"git diff HEAD~1 -- internal/agent",
		"git status",
		"git show HEAD:AGENTS.md",
		"git branch -a",
		"git tag --list",
		"git config --get user.name",
		"git remote -v",
		"sqlite3 sessions.db 'select count(*) from messages'",
		"sqlite3 -readonly sessions.db 'select role, count(*) from messages group by role'",
		"sqlite3 sessions.db .tables",
		"ls | grep go",                  // 管道：两侧都是只读命令
		"grep -rn 'a|b' .",              // 引号里的 | 是普通字符
		"FOO=1 ls",                      // 前缀环境变量赋值不是命令
		"cat a.txt; cat b.txt",          // 分号分段，两段都只读
		"sort -n counts.txt",            // 不带 -o 的排序只读
		"echo done",                     // 纯输出
		"git log --format='%h %s' -3",   // 引号参数里的 % 不影响判定
		"sqlite3 db 'select 1' .tables", // 查询 + 只读点命令
	}
	for _, c := range cases {
		if v := readOnlyViolation(c); v != "" {
			t.Errorf("只读命令应放行 %q，实际被拒: %s", c, v)
		}
	}
}

// TestReadOnlyRejectsWriteCommands：写操作与无法静态判定的结构一律拒绝。
func TestReadOnlyRejectsWriteCommands(t *testing.T) {
	cases := []struct {
		cmd  string
		want string // 拒绝理由里应出现的片段（让模型知道被拒的是哪一类）
	}{
		{"rm -rf build", "删除"},
		{"mv a.txt b.txt", "移动"},
		{"cp a.txt b.txt", "复制"},
		{"mkdir newdir", "创建目录"},
		{"touch a.txt", "创建/修改"},
		{"chmod +x run.sh", "权限"},
		{"echo hi > out.txt", "重定向"},
		{"cat a.txt >> b.txt", "重定向"},
		{"ls; rm -rf x", "删除"},
		{"bash -c 'rm -rf x'", "解释器"},
		{"sh script.sh", "解释器"},
		{"python tools.py", "解释器"},
		{"node build.mjs", "解释器"},
		{"powershell -Command Get-Process", "解释器"},
		{"sudo ls", "提权"},
		{"kill 1234", "终止进程"},
		{"curl https://example.com", "下载"},
		{"wget https://example.com/x", "下载"},
		{"npm install", "包管理"},
		{"go test ./...", "构建"},
		{"make build", "构建"},
		{"find . -delete", "-delete"},
		{"find . -exec rm {} ;", "-exec"},
		{"sort -o out.txt in.txt", "-o"},
		{"sed -i 's/a/b/' f.txt", "sed"},
		{"awk '{print}' f.txt", "awk"},
		{"ls $(rm -rf x)", "命令替换"},
		{"ls `rm -rf x`", "反引号"},
		{"xargs rm", "xargs"},
		{"eval 'rm x'", "动态执行"},
		{"git push", "不是只读子命令"},
		{"git commit -m x", "不是只读子命令"},
		{"git checkout main", "不是只读子命令"},
		{"git branch new-branch", "创建/改写引用"},
		{"git tag v1", "创建/改写引用"},
		{"git remote add origin url", "远端配置"},
		{"git config user.name x", "写配置"},
		{"git -c alias.x='!rm -rf y' x", "全局选项"},
		{"git log --output=out.txt", "--output"},
		{"sqlite3 db 'delete from messages'", "写操作关键字"},
		{"sqlite3 db 'DROP TABLE messages'", "写操作关键字"},
		{"sqlite3 db 'pragma journal_mode=wal'", "写操作关键字"},
		{"sqlite3 db .shell", "点命令"},
		{"sqlite3 db .output /tmp/x", "点命令"},
		{"unknowncmd --flag", "不在只读命令白名单内"},
	}
	for _, c := range cases {
		v := readOnlyViolation(c.cmd)
		if v == "" {
			t.Errorf("写操作/不可判定结构应被拒 %q", c.cmd)
			continue
		}
		if !strings.Contains(v, c.want) {
			t.Errorf("%q 的拒绝理由应含 %q（模型要知道被拒的是哪一类），实际: %s", c.cmd, c.want, v)
		}
	}
}

// TestBashReadOnlyExecutesAndRejects：端到端——只读工具真能跑只读命令，
// 写命令被拒且回填「出路」（告诉模型该派给谁）。
func TestBashReadOnlyExecutesAndRejects(t *testing.T) {
	skipWithoutSh(t)
	r := New()
	ctx := context.Background()

	out := r.Execute(ctx, call(bashReadOnlyName, `{"command":"echo readonly-ok"}`))
	if !strings.Contains(out, "readonly-ok") {
		t.Fatalf("只读 bash 应能执行 echo: %q", out)
	}

	rejected := r.Execute(ctx, call(bashReadOnlyName, `{"command":"echo x > written.txt"}`))
	if !strings.Contains(rejected, "错误") || !strings.Contains(rejected, "只读") {
		t.Fatalf("写命令应被拒绝: %q", rejected)
	}
	if !strings.Contains(rejected, "代码 Agent") {
		t.Fatalf("拒绝理由应给出路（该派给谁）: %q", rejected)
	}

	// 后台任务在只读面上一律拒绝（生命周期与输出无法静态判定）
	bg := r.Execute(ctx, call(bashReadOnlyName, `{"command":"echo x","run_in_background":true}`))
	if !strings.Contains(bg, "错误") || !strings.Contains(bg, "后台") {
		t.Fatalf("只读面不支持后台任务: %q", bg)
	}

	// stdin 是第二条输入通道：`sqlite3 db` + stdin 里的写语句会绕过参数校验
	viaStdin := r.Execute(ctx, call(bashReadOnlyName, `{"command":"cat","stdin":"delete from messages"}`))
	if !strings.Contains(viaStdin, "错误") || !strings.Contains(viaStdin, "stdin") {
		t.Fatalf("只读面不接受 stdin: %q", viaStdin)
	}
}

// TestBashReadOnlyBlocksStdinSmuggling：stdin 走私——`sqlite3 db`（不带 SQL 参数）
// + stdin 里的写语句是绕过参数校验的最短路径，必须被拒。
func TestBashReadOnlyBlocksStdinSmuggling(t *testing.T) {
	r := New()
	// 命令本身通过白名单（sqlite3 只带库名），写操作藏在 stdin 里
	got := r.Execute(context.Background(),
		call(bashReadOnlyName, `{"command":"sqlite3 db.sqlite","stdin":"delete from messages;"}`))
	if !strings.Contains(got, "错误") || !strings.Contains(got, "stdin") {
		t.Fatalf("stdin 走私应被拒: %q", got)
	}
}

// TestFullBashStillUnrestricted：完整 bash 不受只读判据影响（写命令照常执行，
// 确认门由 agent 的权限档负责）——防止把只读校验误挂到共用执行核上。
func TestFullBashStillUnrestricted(t *testing.T) {
	skipWithoutSh(t)
	r := New()
	dir := t.TempDir()
	ctx := WithWorkDir(context.Background(), dir)
	out := r.Execute(ctx, call("bash", `{"command":"echo written > out.txt"}`))
	if strings.Contains(out, "错误") {
		t.Fatalf("完整 bash 的写命令不该被只读判据拦下: %q", out)
	}
}
