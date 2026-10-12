// e2e 临时探针（不入库——验收后删除）：审批档持久化/隔离/默认档四项验收。
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/wsclient"
)

func main() {
	b, err := wsclient.Dial("127.0.0.1:7790")
	if err != nil {
		panic(err)
	}
	defer b.Close()
	ctx := context.Background()
	var hello protocol.HelloResult
	if err := b.Call(ctx, protocol.MethodHello, protocol.HelloParams{Client: "e2e-probe", Version: protocol.Version}, &hello); err != nil {
		panic(err)
	}
	fmt.Println("hello:", hello.Server, hello.Version)

	switch os.Args[1] {
	case "init": // 建两个会话，各设不同档；打印 id
		var a, b2 protocol.SessionResult
		if err := b.Call(ctx, protocol.MethodSessionNew, protocol.SessionNewParams{}, &a); err != nil {
			panic(err)
		}
		if err := b.Call(ctx, protocol.MethodSessionNew, protocol.SessionNewParams{}, &b2); err != nil {
			panic(err)
		}
		var ca, cb protocol.ChatApprovalResult
		if err := b.Call(ctx, protocol.MethodChatApproval, protocol.ChatApprovalParams{SessionID: a.SessionID, Approval: "strict"}, &ca); err != nil {
			panic(err)
		}
		if err := b.Call(ctx, protocol.MethodChatApproval, protocol.ChatApprovalParams{SessionID: b2.SessionID, Approval: "auto"}, &cb); err != nil {
			panic(err)
		}
		fmt.Println("A", a.SessionID, "approval:", ca.Approval)
		fmt.Println("B", b2.SessionID, "approval:", cb.Approval)
	case "verify": // 重启后恢复会话，读 chat.history 的 approval
		for _, id := range os.Args[2:] {
			if err := b.Call(ctx, protocol.MethodSessionResume, protocol.SessionResumeParams{ID: id}, nil); err != nil {
				panic(err)
			}
			var h protocol.ChatHistoryResult
			if err := b.Call(ctx, protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: id}, &h); err != nil {
				panic(err)
			}
			fmt.Println("history", id, "approval:", h.Approval, "msgs:", len(h.Messages))
		}
	case "cross": // 不切换会话，直接按 id 读两边的 history（隔离口径）
		for _, id := range os.Args[2:] {
			var h protocol.ChatHistoryResult
			if err := b.Call(ctx, protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: id}, &h); err != nil {
				panic(err)
			}
			fmt.Println("history", id, "approval:", h.Approval)
		}
	case "default": // 改默认档 → 新会话初始档；最后恢复默认
		mode := os.Args[2]
		if err := b.Call(ctx, protocol.MethodDefaultApprovalSet, protocol.DefaultApprovalSetParams{Approval: mode}, nil); err != nil {
			panic(err)
		}
		var g protocol.DefaultApprovalResult
		if err := b.Call(ctx, protocol.MethodDefaultApprovalGet, map[string]any{}, &g); err != nil {
			panic(err)
		}
		var n protocol.SessionResult
		if err := b.Call(ctx, protocol.MethodSessionNew, protocol.SessionNewParams{}, &n); err != nil {
			panic(err)
		}
		var h protocol.ChatHistoryResult
		if err := b.Call(ctx, protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: n.SessionID}, &h); err != nil {
			panic(err)
		}
		fmt.Println("default:", g.Approval, "new-session:", n.SessionID, "initial-approval:", h.Approval)
	case "reset-default": // 恢复默认档 confirm（清理）
		if err := b.Call(ctx, protocol.MethodDefaultApprovalSet, protocol.DefaultApprovalSetParams{Approval: config.ApprovalConfirm}, nil); err != nil {
			panic(err)
		}
		fmt.Println("default reset to confirm")
	case "send": // 发一条真实消息并等生成结束（轮询 history）
		id, text := os.Args[2], os.Args[3]
		if err := b.Call(ctx, protocol.MethodChatSend, protocol.ChatSendParams{SessionID: id, Text: text}, nil); err != nil {
			panic(err)
		}
		fmt.Println("sent to", id)
	case "wait-done": // 轮询 history 直到 busy=false（生成收尾）
		id := os.Args[2]
		for i := 0; i < 60; i++ {
			var h protocol.ChatHistoryResult
			if err := b.Call(ctx, protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: id}, &h); err != nil {
				panic(err)
			}
			if !h.Busy && len(h.Messages) > 0 {
				fmt.Println("done", id, "msgs:", len(h.Messages), "approval:", h.Approval)
				return
			}
			fmt.Println("waiting...", i, "busy:", h.Busy, "msgs:", len(h.Messages))
			time.Sleep(3 * time.Second)
		}
		fmt.Println("timeout")
	case "set": // 直接改某会话档（e2e 铺数据用）：set <sid> <mode>
		var ca protocol.ChatApprovalResult
		if err := b.Call(ctx, protocol.MethodChatApproval, protocol.ChatApprovalParams{SessionID: os.Args[2], Approval: os.Args[3]}, &ca); err != nil {
			panic(err)
		}
		fmt.Println("set", os.Args[2], "→", ca.Approval)
	case "list": // session.list（排查安卓端列表）
		var ls []protocol.SessionMeta
		if err := b.Call(ctx, protocol.MethodSessionList, nil, &ls); err != nil {
			panic(err)
		}
		fmt.Println("count:", len(ls))
		for _, m := range ls {
			fmt.Printf("%s arch=%v ws=%q title=%s\n", m.ID, m.Archived, m.Workspace, m.Title)
		}
	}
}
// list: 打印 session.list（安卓端列表口径排查用）
