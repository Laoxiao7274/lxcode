package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// setupTree 造一棵小搜索树：
//
//	root/visible.conf        含 "timeout=30"
//	root/other.txt           含 "timeout=60" 与 "timeout=90"
//	root/sub/deep.log        含 "ERROR boom"
//	root/.hidden/secret.conf 含 "timeout=999"（默认应被隐藏规则跳过）
//	root/.dotfile            含 "timeout=1"
//	root/blob.bin            含 NUL 与 "timeout"（默认应被二进制规则跳过）
//	root/big.log             5MB（默认应被大小规则跳过）
func setupTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("visible.conf", "timeout=30\n")
	write("other.txt", "timeout=60\ntimeout=90\n")
	write("sub/deep.log", "ERROR boom\n")
	write(".hidden/secret.conf", "timeout=999\n")
	write(".dotfile", "timeout=1\n")
	write("blob.bin", "timeout\x00binary\n")
	big := strings.Repeat(strings.Repeat("x", 1000)+"\n", 5*1024) // ≈5MB
	write("big.log", big+"timeout=big\n")
	return root
}

func searchArgs(pattern, path, mode string, all bool, max int) string {
	args := fmt.Sprintf(`{"pattern":%q,"path":%q,"mode":%q`, pattern, path, mode)
	if all {
		args += `,"all":true`
	}
	if max > 0 {
		args += fmt.Sprintf(`,"max":%d`, max)
	}
	return args + "}"
}

func TestSearchFilesModeListsPaths(t *testing.T) {
	root := setupTree(t)
	r := New()
	// files 模式 + 文件名通配
	got := r.Execute(context.Background(), call("search", searchArgs("*.conf", root, "files", false, 0)))
	if !strings.Contains(got, "visible.conf") {
		t.Fatalf("应列出匹配文件: %q", got)
	}
	if strings.Contains(got, "secret.conf") {
		t.Fatalf("隐藏目录里的文件默认不该出现: %q", got)
	}
	if strings.Contains(got, "timeout") {
		t.Fatalf("files 模式不该回填文件内容: %q", got)
	}
}

func TestSearchContentModeHasLineNumbers(t *testing.T) {
	root := setupTree(t)
	r := New()
	got := r.Execute(context.Background(), call("search", searchArgs("timeout=60", root, "content", false, 0)))
	if !strings.Contains(got, "other.txt:1:timeout=60") {
		t.Fatalf("content 模式应给 文件:行号:内容: %q", got)
	}
	// 行号必须能直接喂给 read_file（offset 语义一致）
	read := r.Execute(context.Background(), call("read_file",
		`{"path":`+quote(filepath.Join(root, "other.txt"))+`,"offset":1,"limit":1}`))
	if !strings.Contains(read, "1→timeout=60") {
		t.Fatalf("search 给的行号应与 read_file 对齐: %q", read)
	}
}

func TestSearchDefaultSkipsHiddenBinaryAndBig(t *testing.T) {
	root := setupTree(t)
	r := New()
	got := r.Execute(context.Background(), call("search", searchArgs("timeout", root, "content", false, 100)))
	if strings.Contains(got, "secret.conf") {
		t.Errorf("隐藏目录里的命中不该出现: %q", got)
	}
	if strings.Contains(got, ".dotfile") {
		t.Errorf("隐藏文件的命中不该出现: %q", got)
	}
	if strings.Contains(got, "blob.bin") {
		t.Errorf("二进制文件的命中不该出现: %q", got)
	}
	if strings.Contains(got, "big.log") {
		t.Errorf("超过 4MB 的文件不该被扫描: %q", got)
	}
	// 过滤情况必须告知模型，否则"搜不到"被误解成"不存在"
	if !strings.Contains(got, "默认过滤") {
		t.Errorf("应说明过滤情况: %q", got)
	}
	// 但可见文件必须都命中
	for _, want := range []string{"visible.conf", "other.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("漏了可见文件 %s: %q", want, got)
		}
	}
}

func TestSearchAllFlagDisablesFilters(t *testing.T) {
	root := setupTree(t)
	r := New()
	got := r.Execute(context.Background(), call("search", searchArgs("timeout", root, "content", true, 100)))
	for _, want := range []string{"secret.conf", ".dotfile"} {
		if !strings.Contains(got, want) {
			t.Errorf("all=true 时 %s 应被搜到: %q", want, got)
		}
	}
	if strings.Contains(got, "默认过滤") {
		t.Errorf("all=true 时不该再提默认过滤: %q", got)
	}
}

func TestSearchCountMode(t *testing.T) {
	root := setupTree(t)
	r := New()
	got := r.Execute(context.Background(), call("search", searchArgs("timeout=", root, "count", false, 0)))
	if !strings.Contains(got, "other.txt: 2") {
		t.Fatalf("count 模式应给出每个文件的命中数: %q", got)
	}
	if !strings.Contains(got, "合计命中") {
		t.Fatalf("count 模式应给合计: %q", got)
	}
}

func TestSearchTruncationTellsTotal(t *testing.T) {
	root := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&sb, "hit-%d\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "many.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New()
	got := r.Execute(context.Background(), call("search", searchArgs("hit-", root, "content", false, 10)))
	lines := strings.Count(got, "many.txt:")
	if lines != 10 {
		t.Fatalf("max=10 应只显示 10 条，got %d: %q", lines, got)
	}
	// 截断必须告知总数——这是模型决定"缩小范围"还是"接受现状"的唯一依据
	if !strings.Contains(got, "共 200 条命中") || !strings.Contains(got, "显示前 10 条") {
		t.Fatalf("截断提示应含总数与显示数: %q", got)
	}
}

func TestSearchByFileNameWildcard(t *testing.T) {
	root := setupTree(t)
	r := New()
	got := r.Execute(context.Background(), call("search", searchArgs("*.log", root, "files", false, 0)))
	if !strings.Contains(got, "sub/deep.log") && !strings.Contains(got, "deep.log") {
		t.Fatalf("通配应能按文件名找到嵌套文件: %q", got)
	}
}

func TestSearchRegexWithAlternation(t *testing.T) {
	root := setupTree(t)
	r := New()
	// 设备上的 BusyBox grep 不支持 -E；我们的 search 用 RE2，必须支持交替
	got := r.Execute(context.Background(), call("search", searchArgs("timeout=(30|60)", root, "content", false, 0)))
	if !strings.Contains(got, "visible.conf") || !strings.Contains(got, "other.txt") {
		t.Fatalf("正则交替应生效（BusyBox grep -E 做不到这一点）: %q", got)
	}
}

func TestSearchSingleFileExplicitBypassesFilters(t *testing.T) {
	root := setupTree(t)
	r := New()
	// 显式指定文件：与 ripgrep 一致，绕过隐藏/二进制过滤
	got := r.Execute(context.Background(), call("search",
		searchArgs("timeout", filepath.Join(root, ".dotfile"), "content", false, 0)))
	if !strings.Contains(got, ".dotfile") {
		t.Fatalf("显式指定的文件应被搜索: %q", got)
	}
}

func TestSearchErrors(t *testing.T) {
	r := New()
	// 空 pattern
	if got := r.Execute(context.Background(), call("search", `{"pattern":""}`)); !strings.Contains(got, "pattern 不能为空") {
		t.Fatalf("空 pattern 应报错: %q", got)
	}
	// 非法 mode
	if got := r.Execute(context.Background(), call("search", `{"pattern":"x","mode":"fuzzy"}`)); !strings.Contains(got, "mode 只能是") {
		t.Fatalf("非法 mode 应报错: %q", got)
	}
	// 非法正则：错误信息要能指导模型怎么改
	got := r.Execute(context.Background(), call("search", `{"pattern":"([","mode":"content"}`))
	if !strings.Contains(got, "正则") {
		t.Fatalf("坏正则应给出可自解释的错误: %q", got)
	}
	// 路径不存在
	if got := r.Execute(context.Background(), call("search", `{"pattern":"x","path":"/no/such/dir"}`)); !strings.Contains(got, "不可访问") {
		t.Fatalf("不存在的路径应报错: %q", got)
	}
}

func TestSearchSkipsProcAndKeepsWithinBounds(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("只有 Linux 有 /proc 这类内核虚拟文件系统（设备端容器内会跑到）")
	}
	r := New()
	// 这是个"绝不能卡死"的用例：搜 /proc 必须快速返回而不是走进内核虚拟文件系统
	done := make(chan string, 1)
	go func() {
		done <- r.Execute(context.Background(), call("search", `{"pattern":"^processor","path":"/proc","mode":"content"}`))
	}()
	select {
	case got := <-done:
		t.Logf("搜 /proc 返回: %.80s", strings.ReplaceAll(got, "\n", " "))
	case <-time.After(10 * time.Second):
		t.Fatal("搜 /proc 超时——默认过滤没挡住内核虚拟文件系统")
	}
}

func TestSearchIsLowRisk(t *testing.T) {
	d, ok := New().Get("search")
	if !ok {
		t.Fatal("search 未注册")
	}
	if d.Risk != RiskLow {
		t.Fatal("search 是只读且不经 shell 的工具，应为低危（这正是它存在的意义：只读检索不占确认门）")
	}
	if d.Confirm != nil {
		t.Fatal("低危工具不该有确认门")
	}
	if len(d.Description) > 400 {
		t.Fatalf("search 描述过长（%d 字符），会占掉本地模型的提示预算", len(d.Description))
	}
}
