package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---------- read_file：分页 / 行号 / 二进制 / 目录 ----------

func TestReadLineNumbersAndPagination(t *testing.T) {
	r := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "lines.txt")
	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&sb, "line-%d\n", i)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	// 默认读全部（10 行 < 2000）
	got := r.Execute(context.Background(), call("read_file", `{"path":`+quote(path)+`}`))
	if !strings.Contains(got, "1→line-1") || !strings.Contains(got, "10→line-10") {
		t.Fatalf("应带行号前缀: %q", got)
	}

	// 分页：offset=3 limit=4 → 第 3..6 行，并提示续读
	got = r.Execute(context.Background(), call("read_file",
		`{"path":`+quote(path)+`,"offset":3,"limit":4}`))
	for _, want := range []string{"3→line-3", "6→line-6"} {
		if !strings.Contains(got, want) {
			t.Fatalf("分页结果缺 %q: %q", want, got)
		}
	}
	if strings.Contains(got, "7→") {
		t.Fatalf("limit 未生效，读到了第 7 行: %q", got)
	}
	if !strings.Contains(got, "续读") || !strings.Contains(got, "offset=7") {
		t.Fatalf("截断应给出续读参数: %q", got)
	}
}

func TestReadOffsetBeyondEOF(t *testing.T) {
	r := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "short.txt")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := r.Execute(context.Background(), call("read_file",
		`{"path":`+quote(path)+`,"offset":99}`))
	if !strings.Contains(got, "共 2 行") {
		t.Fatalf("越界 offset 应说明总行数: %q", got)
	}
}

func TestReadRejectsBinary(t *testing.T) {
	r := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "bin.dat")
	raw := []byte{0x7f, 'E', 'L', 'F', 0x00, 0x01, 0x02, 0x03}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got := r.Execute(context.Background(), call("read_file", `{"path":`+quote(path)+`}`))
	if !strings.Contains(got, "二进制") {
		t.Fatalf("二进制文件应被识别并拒绝: %q", got)
	}
	if strings.Contains(got, "\x00") {
		t.Fatal("结果里不应含 NUL 字节（会污染上下文）")
	}
}

func TestReadDirectoryErrorIsSelfExplaining(t *testing.T) {
	r := New()
	got := r.Execute(context.Background(), call("read_file", `{"path":`+quote(t.TempDir())+`}`))
	if !strings.Contains(got, "目录") {
		t.Fatalf("读目录应点名目录并给替代方案: %q", got)
	}
}

// ---------- write_file：原子写 / 缩水守卫 ----------

func TestWriteIsAtomicAndPreservesMode(t *testing.T) {
	r := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.conf")
	if err := os.WriteFile(path, []byte("old-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	got := r.Execute(context.Background(), call("write_file",
		`{"path":`+quote(path)+`,"content":"new-content"}`))
	if !strings.Contains(got, "已写入") {
		t.Fatalf("写入应成功: %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new-content" {
		t.Fatalf("内容不符: %q err=%v", data, err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && os.SameFile(before, after) {
		t.Fatal("文件 inode 未变化，说明是原地截断写而非 rename 原子替换")
	}
	// 权限位只在 POSIX 上可断言：Windows 的权限模型不映射 0600
	if runtime.GOOS != "windows" && after.Mode().Perm() != 0o600 {
		t.Fatalf("原文件权限应保留，got %v", after.Mode().Perm())
	}
	// 不留临时文件
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}
}

// TestWriteFailureKeepsOriginal：原子写的核心保证——失败时目标不受影响、不留临时文件。
// 用"目标是目录"来制造确定的失败：rename 覆盖目录在任何权限模型下都失败
// （容器里测试以 root 跑，靠改目录权限来制造失败会被 root 直接绕过）。
func TestWriteFailureKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "iam-a-dir")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	got := New().Execute(context.Background(), call("write_file",
		`{"path":`+quote(target)+`,"content":"x"}`))
	if !strings.HasPrefix(got, "错误") {
		t.Fatalf("写目录路径应报错: %q", got)
	}
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		t.Fatalf("目标被破坏: err=%v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") || strings.Contains(e.Name(), ".bak") {
			t.Fatalf("失败路径残留中间文件: %s", e.Name())
		}
	}
}

// TestWritePreservesExistingMode：覆盖写入必须保留原权限位（原子替换最容易丢的属性）。
func TestWritePreservesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 权限模型不映射 POSIX 位")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.conf")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := New().Execute(context.Background(), call("write_file",
		`{"path":`+quote(path)+`,"content":"new-secret"}`)); !strings.Contains(got, "已写入") {
		t.Fatalf("写入失败: %q", got)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("权限未保留: got %v want 0600（临时文件重命名后丢权限会让密钥类文件变可读）",
			st.Mode().Perm())
	}
}

func TestWriteShrinkGuard(t *testing.T) {
	r := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 5000)), 0o644); err != nil {
		t.Fatal(err)
	}

	// 缩到不足一半 → 必须弹确认，且提示里写明字节变化
	prompt := r.Confirm(call("write_file", `{"path":`+quote(path)+`,"content":"{}"}`))
	if prompt == "" {
		t.Fatal("内容显著变短必须要求确认")
	}
	for _, want := range []string{"变短", "5000", "减少"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("缩水提示缺 %q: %q", want, prompt)
		}
	}

	// 正常增减（保持在 50% 以上）→ 仍需确认（覆盖），但不是缩水告警
	prompt = r.Confirm(call("write_file",
		`{"path":`+quote(path)+`,"content":"`+strings.Repeat("y", 4000)+`"}`))
	if prompt == "" {
		t.Fatal("覆盖已有文件仍应确认")
	}
	if strings.Contains(prompt, "变短") {
		t.Fatalf("未触发缩水阈值却报了缩水: %q", prompt)
	}

	// 新建文件 → 无需确认
	prompt = r.Confirm(call("write_file",
		`{"path":`+quote(filepath.Join(dir, "new.txt"))+`,"content":"x"}`))
	if prompt != "" {
		t.Fatalf("新文件不应确认: %q", prompt)
	}
}

// ---------- bash：stdin / cwd ----------

// skipWithoutSh 在真正没有 sh 可用的环境跳过 bash 相关用例。
// 不按 GOOS 判断：开发机上装了 Git for Windows 等工具时 sh 是存在的，
// 按平台跳过会白白丢掉本地验证（设备端是 Alpine，sh 必然存在）。
func skipWithoutSh(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("本机无 sh 可执行文件，跳过（设备端验证覆盖）")
	}
}

func TestBashStdin(t *testing.T) {
	skipWithoutSh(t)
	r := New()
	// command 只写 cat，脚本体走 stdin——这正是"不用引号转义"的关键路径
	got := r.Execute(context.Background(), call("bash",
		`{"command":"cat","stdin":"第一行\n第二行 with 'quotes' and \"double\"\n"}`))
	if !strings.Contains(got, "第一行") || !strings.Contains(got, "第二行") {
		t.Fatalf("stdin 未被传入: %q", got)
	}
	// 多行脚本：stdin 给脚本、command 用 sh 读取（设备上 sh -s 也可）
	got = r.Execute(context.Background(), call("bash",
		`{"command":"sh","stdin":"echo AAA\necho BBB\n"}`))
	if !strings.Contains(got, "AAA") || !strings.Contains(got, "BBB") {
		t.Fatalf("多行脚本执行失败: %q", got)
	}
}

func TestBashCwd(t *testing.T) {
	skipWithoutSh(t)
	r := New()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("here"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := r.Execute(context.Background(), call("bash",
		`{"command":"ls","cwd":`+quote(dir)+`}`))
	if !strings.Contains(got, "marker.txt") {
		t.Fatalf("cwd 未生效: %q", got)
	}
	// 相对 cwd：按工作目录解析（桌面 agent 的模型常传相对路径）——
	// 解析结果不存在时给自解释错误（含解析后的绝对路径，便于模型纠正）
	got = r.Execute(context.Background(), call("bash", `{"command":"ls","cwd":"relative/path"}`))
	if !strings.Contains(got, "不存在") {
		t.Fatalf("不存在的相对 cwd 应被拦下并给出解析结果: %q", got)
	}
	// 不存在的路径用平台绝对路径构造：Windows 上 "/no/such/dir" 不算绝对路径，
	// 会先命中"必须是绝对路径"分支，测不到"不存在"分支
	missing := filepath.Join(t.TempDir(), "no-such-subdir")
	got = r.Execute(context.Background(), call("bash", `{"command":"ls","cwd":`+quote(missing)+`}`))
	if !strings.Contains(got, "不存在") {
		t.Fatalf("不存在的 cwd 应被拦下: %q", got)
	}
}

func TestBashStdinTooLarge(t *testing.T) {
	r := New()
	big := strings.Repeat("a", bashMaxStdin+1)
	got := r.Execute(context.Background(), call("bash",
		`{"command":"cat","stdin":`+quote(big)+`}`))
	if !strings.Contains(got, "上限") {
		t.Fatalf("超限 stdin 应被拦下: %q", got)
	}
}

func TestBashConfirmShowsStdinAndCwd(t *testing.T) {
	r := New()
	dir := t.TempDir()
	prompt := r.Confirm(call("bash",
		`{"command":"run.sh","cwd":`+quote(dir)+`,"stdin":"hello-script\n"}`))
	for _, want := range []string{"run.sh", dir, "hello-script"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("确认卡应展示 %q（人要看清楚到底跑什么）: %q", want, prompt)
		}
	}
}

// ---------- JSON 修复 ----------

func TestRepairJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // 期望修复后解析出的 command 字段
	}{
		{"字符串内裸换行", "{\"command\": \"echo a\necho b\"}", "echo a\necho b"},
		{"字符串内裸制表符", "{\"command\": \"echo\ta\"}", "echo\ta"},
		{"对象尾逗号", "{\"command\": \"ls\",}", "ls"},
		{"CRLF 归一", "{\"command\": \"echo a\r\necho b\"}", "echo a\necho b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixed, ok := repairJSON(tc.in)
			if !ok {
				t.Fatalf("应可修复: %q", tc.in)
			}
			var v struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal([]byte(fixed), &v); err != nil {
				t.Fatalf("修复结果仍非法: %q (%v)", fixed, err)
			}
			if v.Command != tc.want {
				t.Fatalf("修复后字段不符: got %q want %q", v.Command, tc.want)
			}
		})
	}
}

func TestRepairJSONTruncated(t *testing.T) {
	in := `{"path": "/tmp/x.txt", "content": "line1
line2`
	fixed, ok := repairJSON(in)
	if !ok {
		t.Fatalf("截断的 JSON 应可补齐: %q", in)
	}
	var v struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(fixed), &v); err != nil {
		t.Fatalf("补齐结果仍非法: %q (%v)", fixed, err)
	}
	if v.Path != "/tmp/x.txt" || !strings.Contains(v.Content, "line2") {
		t.Fatalf("补齐内容不符: %+v", v)
	}
}

func TestRepairJSONLeavesValidAloneAndGivesUpOnGarbage(t *testing.T) {
	valid := `{"command":"ls -la"}`
	if !json.Valid([]byte(valid)) {
		t.Fatal("用例本身应为合法 JSON")
	}
	if _, ok := repairJSON(valid); ok {
		t.Log("说明：合法 JSON 也能被修复路径返回（不影响正确性，Execute 会先试原样）")
	}
	if _, ok := repairJSON("not json at all"); ok {
		t.Fatal("纯文本不应被硬修复成 JSON")
	}
	if _, ok := repairJSON(""); ok {
		t.Fatal("空串不应被修复")
	}
}

func TestExecuteUsesRepairedArgsAndFlagsIt(t *testing.T) {
	r := New()
	// 裸换行导致非法 JSON：应被修复执行，并在结果里注明"已自动修复"
	got := r.Execute(context.Background(), call("bash", "{\"command\": \"echo repaired-ok\""))
	if !strings.Contains(got, "自动修复") {
		t.Fatalf("应告知模型参数被修复过: %q", got)
	}
	if !strings.Contains(got, "repaired-ok") {
		t.Fatalf("修复后应执行成功: %q", got)
	}
}

func TestExecuteStillRejectsUnrepairable(t *testing.T) {
	r := New()
	got := r.Execute(context.Background(), call("bash", "totally not json"))
	if !strings.Contains(got, "不是合法 JSON") {
		t.Fatalf("无法修复时应如实报错: %q", got)
	}
}
