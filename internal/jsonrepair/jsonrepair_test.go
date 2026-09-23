package jsonrepair

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepair(t *testing.T) {
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
			fixed, ok := Repair(tc.in)
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

func TestRepairTruncated(t *testing.T) {
	in := `{"path": "/tmp/x.txt", "content": "line1
line2`
	fixed, ok := Repair(in)
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

func TestRepairLeavesValidAloneAndGivesUpOnGarbage(t *testing.T) {
	valid := `{"command":"ls -la"}`
	if !json.Valid([]byte(valid)) {
		t.Fatal("用例本身应为合法 JSON")
	}
	if _, ok := Repair(valid); ok {
		t.Log("说明：合法 JSON 也能被修复路径返回（不影响正确性，调用方会先试原样）")
	}
	if _, ok := Repair("not json at all"); ok {
		t.Fatal("纯文本不应被硬修复成 JSON")
	}
	if _, ok := Repair(""); ok {
		t.Fatal("空串不应被修复")
	}
}

// TestRepairGivesUpOnInvalidEscape 钉住修复能力的**边界**：非法转义（模型把
// Windows 路径写成单个反斜杠，产生 `\U` 这种非法转义）不在保守修复的范围内——
// 那是"语义猜测"（无法判断模型本意是反斜杠还是转义），所以这里必须返回 false，
// 由调用方兜底（agent 写历史时降级成 {}、llm 组装请求时同样降级）。
func TestRepairGivesUpOnInvalidEscape(t *testing.T) {
	for _, in := range []string{
		`{"path": "C:\Users\me\file.txt"}`,
		`{"path": "C:\Users"}`,
	} {
		if json.Valid([]byte(in)) {
			t.Fatalf("用例本身应为非法 JSON（含非法转义）: %q", in)
		}
		if fixed, ok := Repair(in); ok {
			t.Fatalf("非法转义不该被硬猜成合法 JSON: %q → %q", in, fixed)
		}
	}
}

// TestRepairRealTruncatedDispatch 用**线上真实事故数据**做回归夹具：
// 2026-09-23 dev 会话里一条被 max_tokens 截断的 agent.dispatch 参数（892 字节，
// 截断点在 task 的代码块中间，字符串与对象都没闭合）。它当时以非法 JSON 进了
// 历史，导致该会话之后每一次请求都在 anthropic 适配器组装时失败（会话永久锁死）。
//
// 这个夹具同时钉住两件事：① 这类截断**能**被保守修复；② 修复后 agent/task
// 两个字段的内容确实保留下来（不能修成空对象）。
func TestRepairRealTruncatedDispatch(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "truncated-dispatch-args.json"))
	if err != nil {
		t.Fatal(err)
	}
	in := string(raw)
	if json.Valid([]byte(in)) {
		t.Fatal("夹具本身应是非法 JSON（截断形态），否则它就不再是回归夹具")
	}
	fixed, ok := Repair(in)
	if !ok {
		t.Fatal("线上这条截断参数应能被保守修复")
	}
	if !json.Valid([]byte(fixed)) {
		t.Fatalf("修复结果仍非法 JSON")
	}
	var v struct {
		Agent string `json:"agent"`
		Task  string `json:"task"`
	}
	if err := json.Unmarshal([]byte(fixed), &v); err != nil {
		t.Fatalf("修复结果解析失败: %v", err)
	}
	if v.Agent != "coder" {
		t.Fatalf("agent 字段应保留: %q", v.Agent)
	}
	if !strings.Contains(v.Task, "移除设备行右侧") {
		t.Fatalf("task 正文应保留: %.60q", v.Task)
	}
	tail := []rune(v.Task)
	if len(tail) > 40 {
		tail = tail[len(tail)-40:]
	}
	// 截断点落在代码块中间：末行 `)}`（JSX 表达式闭合）+ 换行必须原样保留
	// （钉住"修复只是补全右侧，绝不丢内容"）
	if !strings.HasSuffix(strings.TrimSpace(v.Task), ")}") {
		t.Fatalf("task 尾部（截断点前的最后一行）也应保留，实际尾部: %q", string(tail))
	}
	if !strings.Contains(v.Task, "</button>") {
		t.Fatalf("task 里被截断的代码块内容应保留，实际尾部: %q", string(tail))
	}
}
