// 跨会话归属与续跑的测试（docs/session-tree.md §C.2.1 / §C.5.1）。
//
// 三条硬要求：
//   - 父会话回滚 = 父对话 leaf 移动 + **父子全部文件改动一起还原** + 子会话对话不动；
//   - 回滚点之后新建的文件（子会话新建的）→ 删掉；
//   - 续跑同一子会话时 owner_seq 按**本次**派发重写（边界①）：回滚到第二轮之前
//     只还原第二轮改的文件。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// TestParentRewindRestoresChildFiles：父会话派子 Agent 改文件 → 父会话回滚 →
// 文件还原；**子会话的对话历史一条不少、leaf 未变**（拍板项：只回文件、不动子对话）。
func TestParentRewindRestoresChildFiles(t *testing.T) {
	srv, client, _ := newTestServer(t, dispatchEditStream("c1", "shared.txt", "base", "child改的"))
	id := createTestSession(t, client, "")
	workDir := mustWorkDir(t, srv, client, id)
	target := filepath.Join(workDir, "shared.txt")
	if err := os.WriteFile(target, []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	anchor := sendTurnSeq(t, srv, client, id, "派个子 Agent 改文件")
	if got := mustRead(t, target); got != "child改的" {
		t.Fatalf("子 Agent 应改掉文件，实际 %q", got)
	}

	// 记下子会话的历史与 leaf（回滚后必须完全不变）
	children, err := srv.st.ChildrenOf(id)
	if err != nil || len(children) == 0 {
		t.Fatalf("应有子会话: %v", err)
	}
	childID := children[0].ID
	childBefore, err := srv.st.Load(childID)
	if err != nil {
		t.Fatal(err)
	}
	childLeafBefore, err := srv.st.LeafSeq(childID)
	if err != nil {
		t.Fatal(err)
	}

	var res protocol.ChatRewindResult
	callResult(t, client, protocol.MethodChatRewind,
		protocol.ChatRewindParams{SessionID: id, Seq: anchor, Files: true}, &res)
	if res.Files == nil || len(res.Files.Conflicts) != 0 {
		t.Fatalf("不该有冲突: %+v", res.Files)
	}
	if len(res.Files.Restored) != 1 || res.Files.Restored[0].SessionID != childID {
		t.Fatalf("还原项应标明是子会话改的: %+v", res.Files.Restored)
	}
	if len(res.Files.ChildSessions) != 1 || res.Files.ChildSessions[0] != childID {
		t.Fatalf("child_sessions 应列出子会话: %+v", res.Files.ChildSessions)
	}
	if got := mustRead(t, target); got != "base" {
		t.Fatalf("文件应还原为 base，实际 %q", got)
	}

	// 子会话的对话历史与 leaf **完全不动**
	childAfter, err := srv.st.Load(childID)
	if err != nil {
		t.Fatal(err)
	}
	if len(childAfter) != len(childBefore) {
		t.Fatalf("子会话历史不该被回滚: before=%d after=%d", len(childBefore), len(childAfter))
	}
	childLeafAfter, err := srv.st.LeafSeq(childID)
	if err != nil {
		t.Fatal(err)
	}
	if childLeafAfter != childLeafBefore {
		t.Fatalf("子会话 leaf 不该动: before=%d after=%d", childLeafBefore, childLeafAfter)
	}
}

// TestChildCreatedFileDeletedOnRewind：回滚点之后**新建**的文件 → 删掉
// （它在这一段里是 agent 新建的，回滚 = 让它消失）。
func TestChildCreatedFileDeletedOnRewind(t *testing.T) {
	srv, client, _ := newTestServer(t, dispatchCreateStream("fresh.txt", "内容"))
	id := createTestSession(t, client, "")
	workDir := mustWorkDir(t, srv, client, id)
	target := filepath.Join(workDir, "fresh.txt")
	anchor := sendTurnSeq(t, srv, client, id, "让子 Agent 新建一个文件")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("文件应已新建: %v", err)
	}

	var res protocol.ChatRewindResult
	callResult(t, client, protocol.MethodChatRewind,
		protocol.ChatRewindParams{SessionID: id, Seq: anchor, Files: true}, &res)
	if len(res.Files.Deleted) != 1 || res.Files.Deleted[0].Path != "fresh.txt" {
		t.Fatalf("新建的文件应被删掉: %+v", res.Files)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("文件应已被删除，err=%v", err)
	}
}

// TestChildResumeUsesDispatchTurn：续跑同一子会话时 owner_seq 按**本次**派发重写
// （边界①）：回滚到第二轮之前 → 只还原第二轮改的文件，第一轮的不动。
func TestChildResumeUsesDispatchTurn(t *testing.T) {
	srv, client, _ := newTestServer(t, resumeDispatchStream())
	id := createTestSession(t, client, "")
	workDir := mustWorkDir(t, srv, client, id)
	aTarget := filepath.Join(workDir, "a.txt")
	bTarget := filepath.Join(workDir, "b.txt")
	for _, f := range []string{aTarget, bTarget} {
		if err := os.WriteFile(f, []byte("orig"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sendTurnSeq(t, srv, client, id, "第一轮：改 a.txt")
	secondAnchor := sendTurnSeq(t, srv, client, id, "第二轮：改 b.txt")

	if got := mustRead(t, aTarget); got != "a改" {
		t.Fatalf("第一轮应改 a.txt，实际 %q", got)
	}
	if got := mustRead(t, bTarget); got != "b改" {
		t.Fatalf("第二轮应改 b.txt，实际 %q", got)
	}

	// 回滚到第二轮之前：b.txt 还原，a.txt **不动**（它属于第一轮，owner_seq 更小）
	var res protocol.ChatRewindResult
	callResult(t, client, protocol.MethodChatRewind,
		protocol.ChatRewindParams{SessionID: id, Seq: secondAnchor, Files: true}, &res)
	if got := mustRead(t, bTarget); got != "orig" {
		t.Fatalf("第二轮的 b.txt 应被还原，实际 %q", got)
	}
	if got := mustRead(t, aTarget); got != "a改" {
		t.Fatalf("第一轮的 a.txt 不该被还原（owner_seq 更小），实际 %q", got)
	}
}

// ---- 假 LLM ----

// dispatchCreateStream：主 Agent 派子 Agent 用 write_file 新建一个文件。
func dispatchCreateStream(relPath, content string) testStream {
	var mainRounds int
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		isMain := len(msgs) > 0 && strings.Contains(msgs[0].Content, "主 Agent（调度中枢）")
		go func() {
			defer close(ch)
			if !isMain {
				for _, msg := range msgs {
					if msg.Role == "tool" {
						ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
							Message: llm.Message{Role: "assistant", Content: "建好了。"}, FinishReason: llm.FinishStop,
							PromptTokens: 800,
						}}
						return
					}
				}
				args, _ := json.Marshal(map[string]string{"path": relPath, "content": content})
				tc := llm.ToolCall{ID: "sub-call"}
				tc.Function.Name = "write_file"
				tc.Function.Arguments = string(args)
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}},
					FinishReason: llm.FinishToolCalls, PromptTokens: 800,
				}}
				return
			}
			mainRounds++
			for _, msg := range msgs {
				if msg.Role == "tool" {
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已验收。"}, FinishReason: llm.FinishStop,
						PromptTokens: 1000,
					}}
					return
				}
			}
			if mainRounds > 1 {
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: "已验收。"}, FinishReason: llm.FinishStop,
					PromptTokens: 1000,
				}}
				return
			}
			tc := llm.ToolCall{ID: "c1"}
			tc.Function.Name = "agent_dispatch"
			tc.Function.Arguments = `{"agent":"coder","task":"新建一个文件"}`
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}},
				FinishReason: llm.FinishToolCalls, PromptTokens: 1000,
			}}
		}()
		return ch, nil
	}
}

// resumeDispatchStream：两轮都派给**同一个**子会话（续跑），
// 第一轮改 a.txt、第二轮改 b.txt。
func resumeDispatchStream() testStream {
	var mainRounds int
	var childSession string
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		isMain := len(msgs) > 0 && strings.Contains(msgs[0].Content, "主 Agent（调度中枢）")
		go func() {
			defer close(ch)
			if !isMain {
				// 本轮已经跑过 edit（最后一条是它的 tool 结果）→ 收尾。
				// 判定用 tool_call_id 前缀：续跑时历史里还带着上一次派发的 tool 结果，
				// 只看「有没有 tool 消息」会把第二轮误判成已完成。
				if n := len(msgs); n > 0 && msgs[n-1].Role == "tool" && strings.HasPrefix(msgs[n-1].ToolCallID, "sub-call") {
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "改好了。"}, FinishReason: llm.FinishStop,
						PromptTokens: 800,
					}}
					return
				}
				target, newS := "a.txt", "a改"
				for _, msg := range msgs {
					if msg.Role == "user" && strings.Contains(msg.Content, "b.txt") {
						target, newS = "b.txt", "b改"
					}
				}
				args, _ := json.Marshal(map[string]string{"path": target, "old_string": "orig", "new_string": newS})
				tc := llm.ToolCall{ID: "sub-call-" + target}
				tc.Function.Name = "edit"
				tc.Function.Arguments = string(args)
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}},
					FinishReason: llm.FinishToolCalls, PromptTokens: 800,
				}}
				return
			}
			mainRounds++
			// 从上一轮的工具结果里取子会话 id（续跑要显式传 session）
			for _, msg := range msgs {
				if msg.Role != "tool" {
					continue
				}
				if i := strings.Index(msg.Content, "子会话 id: "); i >= 0 {
					rest := msg.Content[i+len("子会话 id: "):]
					if j := strings.IndexAny(rest, " \n"); j > 0 {
						childSession = rest[:j]
					}
				}
			}
			// 这一轮的最后一条消息是 tool 结果 = 本轮的派发已经回填 → 收尾。
			//（不能只看「出现过 tool 结果」：第二轮的历史里也带着第一轮的结果。）
			if len(msgs) > 0 && msgs[len(msgs)-1].Role == "tool" {
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: "已验收。"}, FinishReason: llm.FinishStop,
					PromptTokens: 1000,
				}}
				return
			}
			args := `{"agent":"coder","task":"改 a.txt"}`
			if mainRounds >= 2 && childSession != "" {
				args = fmt.Sprintf(`{"agent":"coder","task":"改 b.txt","session":"%s"}`, childSession)
			}
			tc := llm.ToolCall{ID: fmt.Sprintf("d%d", mainRounds)}
			tc.Function.Name = "agent_dispatch"
			tc.Function.Arguments = args
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}},
				FinishReason: llm.FinishToolCalls, PromptTokens: 1000,
			}}
		}()
		return ch, nil
	}
}
