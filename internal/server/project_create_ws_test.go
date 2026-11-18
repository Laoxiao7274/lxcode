// project_create（助手项目全景三件套之三）的 WS 端到端验收：
//   - 助手会话（默认 auto 档）发 project_create → ConfirmForced 硬闸弹
//     assistant.confirmRequest（auto 档的唯一例外口）→ assistant.confirm 放行
//     → 项目落库 + project.changed 广播（前端零改动重拉列表）；
//   - 非空目录拒绝（不往有内容的目录里 git init + 注册）；
//   - 用户拒绝 → 工具不执行（项目没注册、目录没建）。
package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/tools"
)

// pcRoundStream 与 assistantRoundStream 同构：助手第一轮发 project_create
//（参数里的父目录经 chan 喂入），第二轮转述；其余语境给一句文本。
func pcRoundStream(t *testing.T, parentCh <-chan string) testStream {
	t.Helper()
	var mu sync.Mutex
	round := 0
	return func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if len(msgs) > 0 && strings.Contains(msgs[0].Content, "个人智能体助手") {
			mu.Lock()
			round++
			r := round
			mu.Unlock()
			if r > 1 {
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已创建。"}, FinishReason: llm.FinishStop}}
				}()
				return ch, nil
			}
			tc := llm.ToolCall{ID: "call-pc1"}
			tc.Function.Name = tools.ProjectCreateToolName
			args, _ := json.Marshal(map[string]string{"name": "fresh-proj", "parent": <-parentCh})
			tc.Function.Arguments = string(args)
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "?"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	}
}

// newAssistant 建一个助手会话并返回 id。
func newAssistant(t *testing.T, client *wsTestClient) string {
	t.Helper()
	resp := client.call(protocol.MethodAssistantNew, map[string]any{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("assistant.new: %+v", resp)
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	return created.SessionID
}

// TestProjectCreateWSRound：助手（auto 档默认）发 project_create → ConfirmForced
// 硬闸弹确认 → 放行 → 项目落库 + project.changed 广播 + 历史带结果文本。
func TestProjectCreateWSRound(t *testing.T) {
	parentCh := make(chan string, 1)
	srv, client, _ := newTestServer(t, pcRoundStream(t, parentCh))
	parent := t.TempDir()
	parentCh <- parent

	asst := newAssistant(t, client)
	if resp := client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{
		SessionID: asst, Text: "在这建一个 fresh-proj 项目",
	}); resp == nil || resp.Error != nil {
		t.Fatalf("assistant.send: %+v", resp)
	}

	// auto 档也必须弹（ConfirmForced 硬闸——本工具的唯一例外口）
	confirm := client.waitEvent(protocol.EventAssistantConfirm)
	if confirm == nil {
		t.Fatal("auto 档下 project_create 仍应弹确认门（ConfirmForced 硬闸）")
	}
	var req protocol.ConfirmRequest
	if b, err := json.Marshal(confirm.Params); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	if req.Name != tools.ProjectCreateToolName {
		t.Fatalf("确认请求应归属 project_create: %q", req.Name)
	}
	if !strings.Contains(req.Prompt, "fresh-proj") {
		t.Fatalf("确认文案应带项目名: %q", req.Prompt)
	}
	if resp := client.call(protocol.MethodAssistantConfirm, protocol.ToolConfirmParams{
		SessionID: asst, ID: req.ID, Allow: true,
	}); resp == nil || resp.Error != nil {
		t.Fatalf("assistant.confirm: %+v", resp)
	}
	if err := waitAssistantIdle(srv, asst); err != nil {
		t.Fatal(err)
	}

	// 广播 project.changed（前端零改动自动重拉列表）
	if ev := client.waitEvent(protocol.EventProjectChanged); ev == nil {
		t.Fatal("放行后应广播 project.changed")
	}
	// 项目已在列表里
	metas, err := srv.st.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	var found *string
	for i := range metas {
		if metas[i].Name == "fresh-proj" {
			p := metas[i].Path
			found = &p
		}
	}
	if found == nil {
		t.Fatalf("项目应已注册: %+v", metas)
	}
	if *found != filepath.Join(parent, "fresh-proj") {
		t.Fatalf("项目路径应为父目录下的子目录: %q", *found)
	}
	// 磁盘：目录 + git init
	if _, err := os.Stat(filepath.Join(*found, ".git")); err != nil {
		t.Fatalf("项目目录应完成 git init: %v", err)
	}
	// 历史里工具结果带 project_id
	hresp := client.call(protocol.MethodAssistantHistory, protocol.ChatHistoryParams{SessionID: asst})
	var hist protocol.ChatHistoryResult
	if err := unmarshalResult(hresp, &hist); err != nil {
		t.Fatal(err)
	}
	var hasResult bool
	for _, m := range hist.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "project_id: ") && strings.Contains(m.Content, "fresh-proj") {
			hasResult = true
		}
	}
	if !hasResult {
		t.Fatal("工具结果应带 project_id（run_task 的取值来源）")
	}
}

// TestProjectCreateWSNonEmptyDir：目标目录非空 → 拒绝（不注册）。
func TestProjectCreateWSNonEmptyDir(t *testing.T) {
	parent := t.TempDir()
	occupied := filepath.Join(parent, "taken")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 直接经注册表执行工具（接线闭包的校验面；门控与事件归约在上面两条测试已覆盖）
	srv, _, _ := newTestServer(t, nil)
	args, _ := json.Marshal(map[string]string{"name": "taken", "parent": parent})
	got := srv.treg.Execute(context.Background(), pcCall(string(args)))
	if !strings.Contains(got, "非空") {
		t.Fatalf("非空目录应拒绝并说明原因: %q", got)
	}
	metas, _ := srv.st.ListProjects()
	for _, m := range metas {
		if m.Name == "taken" {
			t.Fatal("非空目录不应注册")
		}
	}
	// 已存在文件（非目录）同样拒绝
	asFile := filepath.Join(parent, "file.txt")
	if err := os.WriteFile(asFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ = json.Marshal(map[string]string{"name": "file.txt", "parent": parent})
	got = srv.treg.Execute(context.Background(), pcCall(string(args)))
	if !strings.Contains(got, "不是文件夹") {
		t.Fatalf("目标是文件应拒绝: %q", got)
	}
}

// pcCall 经注册表调 project_create（与模型走的同一条路径）。
func pcCall(args string) llm.ToolCall {
	return llm.ToolCall{ID: "call-pc", Function: struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}{Name: tools.ProjectCreateToolName, Arguments: args}}
}

// TestProjectCreateWSDenied：用户拒绝 → 不执行（目录没建、项目没注册）。
func TestProjectCreateWSDenied(t *testing.T) {
	parentCh := make(chan string, 1)
	srv, client, _ := newTestServer(t, pcRoundStream(t, parentCh))
	parent := t.TempDir()
	parentCh <- parent

	asst := newAssistant(t, client)
	if resp := client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{
		SessionID: asst, Text: "在这建一个 fresh-proj 项目",
	}); resp == nil || resp.Error != nil {
		t.Fatalf("assistant.send: %+v", resp)
	}
	confirm := client.waitEvent(protocol.EventAssistantConfirm)
	if confirm == nil {
		t.Fatal("auto 档下 project_create 仍应弹确认门")
	}
	var req protocol.ConfirmRequest
	if b, err := json.Marshal(confirm.Params); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	if resp := client.call(protocol.MethodAssistantConfirm, protocol.ToolConfirmParams{
		SessionID: asst, ID: req.ID, Allow: false,
	}); resp == nil || resp.Error != nil {
		t.Fatalf("assistant.confirm(拒绝): %+v", resp)
	}
	if err := waitAssistantIdle(srv, asst); err != nil {
		t.Fatal(err)
	}
	// 目录没建、项目没注册
	if _, err := os.Stat(filepath.Join(parent, "fresh-proj")); !os.IsNotExist(err) {
		t.Fatalf("被拒绝的创建不应碰磁盘: %v", err)
	}
	metas, _ := srv.st.ListProjects()
	for _, m := range metas {
		if m.Name == "fresh-proj" {
			t.Fatal("被拒绝的创建不应注册项目")
		}
	}
}
