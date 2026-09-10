// probe.go 实现后端验收探针（--probe）：对运行中的后端做协议级检查，
// 是 install/update 脚本的健康验收面（参考项目的 wsprobe 角色的内置版）。
// 刻意不做 LLM 真实对话——那是冒烟（temp/smoke.mjs）的事，装机的验收
// 只关心"服务活着且协议面完整"，不该花模型 token。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/moyunteng/myt-harness/internal/config"
	"github.com/moyunteng/myt-harness/internal/protocol"
	"github.com/moyunteng/myt-harness/internal/wsclient"
)

// probe 退出码（脚本据此分支）：
const (
	probeOK      = 0 // 全部通过
	probeDead    = 1 // 协议失败——服务死了/没监听（装机脚本应回滚）
	probeUnready = 2 // 服务活着但没配模型（装好了，等用户填配置；热加载会接上）
)

// runProbe 跑全部检查并返回退出码。addr 形如 127.0.0.1:7789。
func runProbe(addr string) int {
	fail := false
	unready := false

	be, err := wsclient.Dial(addr)
	if err != nil {
		fmt.Printf("✗ 连接后端 %s 失败: %v\n", addr, err)
		return probeDead
	}
	defer be.Close()
	fmt.Printf("✓ 连接 %s\n", addr)

	// ready 事件（连接建立即单发）
	if _, ok := waitEvent(be, protocol.EventReady, 3*time.Second); !ok {
		fmt.Println("✗ 未收到 connection.ready")
		return probeDead
	}
	fmt.Println("✓ connection.ready")

	// hello 往返（服务端身份与协议版本）
	var hello protocol.HelloResult
	if err := call(be, protocol.MethodHello, protocol.HelloParams{Client: "probe", Version: protocol.Version}, &hello); err != nil {
		fmt.Printf("✗ hello 失败: %v\n", err)
		return probeDead
	}
	if hello.Server != "myt-harness" || hello.Version != protocol.Version {
		fmt.Printf("✗ 服务端身份不符: %+v\n", hello)
		return probeDead
	}
	fmt.Printf("✓ hello（%s 协议 v%s）\n", hello.Server, hello.Version)

	// model.list
	var ml protocol.ModelListResult
	if err := call(be, protocol.MethodModelList, nil, &ml); err != nil {
		fmt.Printf("✗ model.list 失败: %v\n", err)
		return probeDead
	}
	fmt.Printf("✓ model.list（%d 个模型）\n", len(ml.Models))
	if ml.Roles[config.RoleDefault] == "" {
		fmt.Println("⚠ default 角色未绑定模型——服务活着但还不能对话（编辑配置后热加载生效）")
		unready = true
	}

	// session.list
	var sessions []protocol.SessionMeta
	if err := call(be, protocol.MethodSessionList, nil, &sessions); err != nil {
		fmt.Printf("✗ session.list 失败: %v\n", err)
		return probeDead
	}
	fmt.Printf("✓ session.list（%d 个会话）\n", len(sessions))

	// chat.history
	var hist protocol.ChatHistoryResult
	if err := call(be, protocol.MethodChatHistory, nil, &hist); err != nil {
		fmt.Printf("✗ chat.history 失败: %v\n", err)
		return probeDead
	}
	fmt.Printf("✓ chat.history（%d 条消息，busy=%v）\n", len(hist.Messages), hist.Busy)

	if fail {
		return probeDead
	}
	if unready {
		return probeUnready
	}
	return probeOK
}

// call 是带超时的请求封装。
func call(be wsclient.Backend, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return be.Call(ctx, method, params, result)
}

// waitEvent 等待指定事件名（超时返回 false）。
func waitEvent(be wsclient.Backend, name string, timeout time.Duration) (json.RawMessage, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-be.Events():
			if !ok {
				return nil, false
			}
			if ev.Method == name {
				b, _ := json.Marshal(ev.Params)
				return b, true
			}
		case <-deadline:
			return nil, false
		}
	}
}

// main 的入口包装（退出码直达 os.Exit）。
func probeMain(addr string) {
	os.Exit(runProbe(addr))
}
