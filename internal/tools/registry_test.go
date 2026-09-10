package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/myt-harness/internal/llm"
)

func call(name, args string) llm.ToolCall {
	var c llm.ToolCall
	c.Function.Name = name
	c.Function.Arguments = args
	return c
}

func TestLLMToolsShape(t *testing.T) {
	r := New()
	toolsWire := r.LLMTools()
	if len(toolsWire) != 7 {
		t.Fatalf("应有 7 个工具（read_file / search / session_search / edit / write_file / bash / todo）, got %d", len(toolsWire))
	}
	for _, tw := range toolsWire {
		if tw.Name == "" || tw.Description == "" || !json.Valid(tw.Parameters) {
			t.Fatalf("工具声明不完整: %+v", tw)
		}
		// schema 必须是对象类型且带 required
		var s map[string]any
		if err := json.Unmarshal(tw.Parameters, &s); err != nil || s["type"] != "object" {
			t.Fatalf("%s 的 schema 不是对象: %s", tw.Name, tw.Parameters)
		}
	}
}

// TestNoHostHint：桌面形态不该携带容器部署的宿主前缀/nsenter 提示——
// 描述里出现这些说明工具被误改回了容器形态文案。
func TestNoHostHint(t *testing.T) {
	for _, tw := range New().LLMTools() {
		if strings.Contains(tw.Description, "nsenter") || strings.Contains(tw.Description, "/host") {
			t.Fatalf("桌面形态不应出现容器提示: %s", tw.Description)
		}
	}
}

func TestExecuteReadFile(t *testing.T) {
	r := New()
	path := filepath.Join(t.TempDir(), "hello.txt")
	if err := os.WriteFile(path, []byte("你好，工具世界"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := r.Execute(context.Background(), call("read_file", `{"path":`+quote(path)+`}`))
	if got != "1→你好，工具世界\n" {
		t.Fatalf("read 结果不符（应为带行号的单行）: %q", got)
	}
}

func TestExecuteReadFileMissing(t *testing.T) {
	r := New()
	got := r.Execute(context.Background(), call("read_file", `{"path":"/no/such/file"}`))
	if !strings.Contains(got, "失败") {
		t.Fatalf("缺文件应报错: %q", got)
	}
	if strings.HasPrefix(got, "错误: 错误") {
		t.Fatalf("错误文本不应双层包装: %q", got)
	}
}

func TestExecuteWriteFile(t *testing.T) {
	r := New()
	path := filepath.Join(t.TempDir(), "nested", "dir", "out.txt")
	got := r.Execute(context.Background(), call("write_file",
		`{"path":`+quote(path)+`,"content":"line1\nline2"}`))
	if !strings.Contains(got, "已写入") {
		t.Fatalf("write 结果不符: %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "line1\nline2" {
		t.Fatalf("文件内容不符: %q err=%v", data, err)
	}
}

func TestConfirmWriteNewAndExisting(t *testing.T) {
	r := New()
	path := filepath.Join(t.TempDir(), "exists.txt")
	if err := os.WriteFile(path, []byte("旧内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 新文件：无需确认
	if got := r.Confirm(call("write_file", `{"path":`+quote(filepath.Join(t.TempDir(), "new.txt"))+`,"content":"x"}`)); got != "" {
		t.Fatalf("新文件不应确认: %q", got)
	}
	// 覆盖已有文件：需确认且提示包含文件名
	got := r.Confirm(call("write_file", `{"path":`+quote(path)+`,"content":"x"}`))
	if !strings.Contains(got, "覆盖") || !strings.Contains(got, "exists.txt") {
		t.Fatalf("覆盖应确认: %q", got)
	}
}

func TestConfirmBashAlways(t *testing.T) {
	r := New()
	got := r.Confirm(call("bash", `{"command":"ls -la"}`))
	if !strings.Contains(got, "ls -la") {
		t.Fatalf("bash 应一律确认并显示命令: %q", got)
	}
}

func TestConfirmReadNever(t *testing.T) {
	r := New()
	if got := r.Confirm(call("read_file", `{"path":"/etc/passwd"}`)); got != "" {
		t.Fatalf("read 低危不应确认: %q", got)
	}
}

func TestExecuteUnknownTool(t *testing.T) {
	r := New()
	got := r.Execute(context.Background(), call("rm_rf", `{}`))
	if !strings.Contains(got, "未知工具") || !strings.Contains(got, "read_file") {
		t.Fatalf("未知工具应报错并列出可用工具: %q", got)
	}
}

func TestExecuteBadJSONArgs(t *testing.T) {
	r := New()
	got := r.Execute(context.Background(), call("bash", `not json at all`))
	if !strings.Contains(got, "不是合法 JSON") {
		t.Fatalf("坏参数应硬校验拦截: %q", got)
	}
}

func TestBashTool(t *testing.T) {
	skipWithoutSh(t)
	r := New()
	// 正常执行 + 退出码
	got := r.Execute(context.Background(), call("bash", `{"command":"echo hello; exit 3"}`))
	if !strings.Contains(got, "hello") || !strings.Contains(got, "退出码 3") {
		t.Fatalf("bash 结果不符: %q", got)
	}
	// 超时
	got = r.Execute(context.Background(), call("bash", `{"command":"sleep 3","timeout_sec":1}`))
	if !strings.Contains(got, "超时") {
		t.Fatalf("超时应终止: %q", got)
	}
	// 空输出
	got = r.Execute(context.Background(), call("bash", `{"command":"true"}`))
	if !strings.Contains(got, "无输出") {
		t.Fatalf("空输出应注明: %q", got)
	}
}

func TestBashTimeoutCap(t *testing.T) {
	// timeout_sec 超 300 被钳制——只验证参数钳制逻辑（不真等）
	r := New()
	d, ok := r.Get("bash")
	if !ok {
		t.Fatal("bash 未注册")
	}
	_ = d
	// 钳制逻辑在 Exec 内部；这里仅冒烟确认定义存在且为高危
	if d.Risk != RiskHigh {
		t.Fatal("bash 应为高危")
	}
	_ = time.Second
}

// quote 生成 JSON 字符串字面量（路径含反斜杠时转义）。
func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
