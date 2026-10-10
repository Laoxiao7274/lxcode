// ask_user：向用户提问并等待回答（确认门的「提问」形态）。
//
// 为什么是 ctx 注入而不是注册表全局（与 todo sink / 技能目录同一条理由）：
// 「能不能问、答案给谁」是会话级状态——Session 在每轮开始时把实现挂进 ctx
//（WithAsker），工具层只做参数校验与结果措辞；等待与归属不出工具包边界，
// 父会话与子会话（含合并进程拉起的子会话）各问各的、答案各回各的。

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// AskUserToolName 是 ask_user 的注册名（agent 侧组请求时引用，避免第二份字面量）。
const AskUserToolName = "ask_user"

// maxAskOptions 是预设选项的上限（再多就成问卷了——提问要能一眼读完、一眼选完）。
const maxAskOptions = 6

// AskerFn 是提问通道的实现约定（agent.Session 接线）：挂起一个 ask 形态的
// 确认请求等用户回答。ok=true = 用户给了文本回答；false = 用户跳过（不回答）
// 或等待被取消——两种情况模型都该自己拿主意继续干活。
type AskerFn func(ctx context.Context, question string, options []string) (answer string, ok bool)

// askerKey 是提问通道的 ctx 键（空 struct 零值键，避免碰撞）。
type askerKey struct{}

// WithAsker 把提问通道放进 ctx（每轮开始时注入；fn 为 nil 时不注入）。
func WithAsker(ctx context.Context, fn AskerFn) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, askerKey{}, fn)
}

// askerFrom 取 ctx 里的提问通道（nil = 未接线——本会话不支持提问）。
func askerFrom(ctx context.Context) AskerFn {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(askerKey{}).(AskerFn)
	return fn
}

// askUserDef：ask_user，风险等级 低危（只发起提问，不碰文件系统、不执行命令）。
//
// 执行语义：**挂起等待用户回答**（经 AskerFn → Session.askUser → awaitConfirm，
// 与高危确认共用同一个挂起槽位），用户的回答文本作为工具结果回填给模型。
// 用户跳过时回填「用户没有回答…」——不是错误（IsError=false）：跳过是合法的
// 用户决策「你自己拿主意」，模型收到后应自行决策继续，而不是把整轮报失败。
func askUserDef() *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"question": {"type": "string", "description": "要问用户的问题。自包含：说清背景、两边的意图与取舍，给出你的建议与理由"},
			"options": {"type": "array", "items": {"type": "string", "description": "一个预设答案"}, "description": "可选：预设答案（最多 6 个，用户可直接点选）"}
		},
		"required": ["question"]
	}`)
	return &Def{
		Name: AskUserToolName,
		Description: "向用户提问并等待回答（冲突抉择、需要用户决策时用）。问题要自包含：" +
			"说清背景与两边的意图，给出你的建议与理由。用户可能直接回答，也可能跳过让你自行决策——" +
			"跳过不是失败，别反复重问。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Question string   `json:"question"`
				Options  []string `json:"options"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			if strings.TrimSpace(a.Question) == "" {
				return "", fmt.Errorf("question 不能为空——没有问题的提问用户没法回答")
			}
			if len(a.Options) > maxAskOptions {
				return "", fmt.Errorf("options 最多 %d 个（当前 %d 个）——请只保留最关键的几个", maxAskOptions, len(a.Options))
			}
			fn := askerFrom(ctx)
			if fn == nil {
				return "", fmt.Errorf("当前会话没有接线提问通道（ask_user 不可用）——请直接给出你的决策与理由")
			}
			answer, ok := fn(ctx, a.Question, a.Options)
			if !ok {
				return "用户没有回答，请自行决策或换一种做法。", nil
			}
			return "用户回答：" + answer, nil
		},
	}
}
