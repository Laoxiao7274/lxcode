// 工具调用参数的**读侧兜底**（组装请求时）。
//
// 背景（2026-09-23 线上事故）：历史里可能存着参数不是合法 JSON 的 tool call——
// 模型输出被 max_tokens 截断时，那一版实现把半截 JSON 原样写进了历史。组装请求
// 时硬报错等于**整个会话之后每一次请求都失败**：用户连发三条消息全部无响应，
// 报错是 `工具 agent_dispatch 的 arguments 不是合法 JSON: …`，只能新开会话。
//
// 所以这里一律不报错：先保守修复（internal/jsonrepair），修不动就发空对象——
// 宁可让模型看到一次「参数丢了」，也不让会话变砖。写侧已在源头修（agent 的
// sanitizeToolCallArgs 保证进入历史的参数合法），这里是读侧的最后一道兜底。
package llm

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/moyunteng/lxcode/internal/jsonrepair"
)

// repairToolArgsForWire 把一个工具调用的 arguments 变成"发得出去"的字符串：
// 合法就原样返回（零开销），坏的就保守修复，修不动发空对象。
func repairToolArgsForWire(args string) string {
	if strings.TrimSpace(args) == "" || json.Valid([]byte(args)) {
		return args
	}
	if fixed, ok := jsonrepair.Repair(args); ok {
		log.Printf("历史里的工具调用参数不是合法 JSON，已保守补全后发送（原 %d 字节）", len(args))
		return fixed
	}
	log.Printf("历史里的工具调用参数不是合法 JSON 且修不动，已按空参数发送（原 %d 字节）", len(args))
	return "{}"
}

// sanitizeMessagesForWire 复制出一份可发出的消息：含工具调用的消息逐条复制再修
// 参数，**不就地改写调用方的历史**（内存历史是会话的共享状态，请求组装不该改它）。
func sanitizeMessagesForWire(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if len(out[i].ToolCalls) == 0 {
			continue
		}
		calls := make([]ToolCall, len(out[i].ToolCalls))
		copy(calls, out[i].ToolCalls)
		for j := range calls {
			calls[j].Function.Arguments = repairToolArgsForWire(calls[j].Function.Arguments)
		}
		out[i].ToolCalls = calls
	}
	return out
}
