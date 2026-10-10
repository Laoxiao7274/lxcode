package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/wsclient"
)

func main() {
	b, err := wsclient.Dial("127.0.0.1:7790")
	if err != nil {
		panic(err)
	}
	defer b.Close()
	var hello protocol.HelloResult
	if err := b.Call(context.Background(), protocol.MethodHello, protocol.HelloParams{Client: "probe", Version: protocol.Version}, &hello); err != nil {
		panic(err)
	}
	fmt.Println("hello:", hello.Server, hello.Version)
	var hist struct {
		Messages []json.RawMessage `json:"messages"`
		Context  *protocol.ContextUsage `json:"context"`
		Stats    *protocol.SessionStats `json:"stats"`
		Model    string `json:"model"`
		Busy     bool   `json:"busy"`
	}
	if err := b.Call(context.Background(), protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: "20261010-101737-c043"}, &hist); err != nil {
		panic(err)
	}
	cb, _ := json.Marshal(hist.Context)
	sb, _ := json.Marshal(hist.Stats)
	fmt.Println("context:", string(cb))
	fmt.Println("stats:", string(sb))
	fmt.Println("model:", hist.Model, "busy:", hist.Busy, "msgs:", len(hist.Messages))
}
