package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// TodoItem 是任务清单的一项（共享形状定义在 sessiondata——落库需要，分层规则
// 禁止 store import tools；这里保持别名，既有调用面零改动）。
type TodoItem = sessiondata.TodoItem

// validTodoStatus 引用共享的合法状态集合（sessiondata.TodoItemStates）。
var validTodoStatus = sessiondata.TodoItemStates

// TodoWriteFn 是 todo 状态写入的注入接缝（agent.Session 接线）：
// 会话持有清单状态才能在 UI 上渲染（桌面壳的 TodoList 卡片），
// 工具层只做校验与格式化，状态归属不出工具包边界。
// 经 ctx 注入（见 sessionstate.go）——父会话与子会话各有自己的清单，
// 注册表级全局会让子 Agent 的清单覆盖主会话的。
type TodoWriteFn func(items []TodoItem)

// todoDescriptionStrict 是 strict 档的工具描述（默认；措辞逐字节保留——
// todo_prompt_test 的 TestTodoDescriptionPinned 钉住）。
const todoDescriptionStrict = "维护当前任务清单（全量替换式写入）。每轮开始先审视清单：新任务先用本工具出" +
	"步骤清单（3~7 条、动词开头、可独立验收）再执行——只有两三步也要先建；已有清单先" +
	"对照现状更新，不要另起炉灶。" +
	"每完成一步立即更新状态：把当前项标 done、下一项标 active。清单帮你自己不漏步骤，" +
	"也让用户看到进度。同一时刻最多一项 active。"

// todoDescriptionOff 是 off 档的工具描述：只留中性能力说明与参数语义（全量替换、
// 空数组 = 清空、status 三态、同时最多一项 active 是工具层硬校验）。
//
// 为什么描述也要跟档位走：工具描述同样是**发给模型的指令面**——用户在设置里选
// 「关闭」后，若描述里还留着「每轮开始先审视清单」「只有两三步也要先建」，
// off 档就名不副实。工具本身仍注册、仍可调用（模型自行决定要不要用清单）。
const todoDescriptionOff = "维护当前任务清单（全量替换式写入）：把完整清单一次写入 items（全量替换，不是增量）；" +
	"空数组 = 清空清单。status 只认 pending/active/done。清单项必须唯一。"

// todoDescriptionFor 按 todo-first 档位取工具描述。档位常量在 internal/config
// （tools 不 import agent——分层守卫；config 只依赖 atomicfile，不会成环）。
func todoDescriptionFor(mode string) string {
	if mode == config.TodoPromptOff {
		return todoDescriptionOff
	}
	return todoDescriptionStrict
}

// todoDef：todo，风险等级 低危（只写会话内的规划状态，不碰文件系统）。
//
// 为什么是全量写入而不是增删改：schema 最简单（一个 items 数组），弱模型
// 不需要维护操作语义（op=update 时要不要带全量？id 从哪来？）；清单天然
// 短（个位数条目），全量重写的 token 开销可忽略。
//
// 约束（工具层硬校验，不依赖模型自觉）：
//   - 空清单（items 为空数组）= 清空，合法（任务全部完成的收敛态）；
//   - status 只认 pending/active/done，别的值报错；
//   - active 多于一项时报错——"同一时刻只有一个进行中"是清单能指导执行的
//     关键约束，模型漂移到"全部 active"会让清单退化成无序集合。
func todoDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"items": {
				"type": "array",
				"description": "完整任务清单（全量替换，不是增量）",
				"items": {
					"type": "object",
					"properties": {
						"content": {"type": "string", "description": "任务描述（一句话，动词开头）"},
						"status": {"type": "string", "enum": ["pending", "active", "done"], "description": "pending=未开始；active=进行中（同时最多一项）；done=完成"}
					},
					"required": ["content", "status"]
				}
			}
		},
		"required": ["items"]
	}`)
	return &Def{
		Name:        "todo",
		Description: todoDescriptionStrict,
		Parameters:  schema,
		Risk:        RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Items []TodoItem `json:"items"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			active := 0
			seen := map[string]bool{}
			for i, it := range a.Items {
				if it.Content == "" {
					return "", fmt.Errorf("第 %d 项的 content 不能为空", i+1)
				}
				if !validTodoStatus[it.Status] {
					return "", fmt.Errorf("第 %d 项的 status %q 不合法（只能是 pending/active/done）", i+1, it.Status)
				}
				if it.Status == "active" {
					active++
				}
				// content 唯一性（对齐 DSH）：全量替换语义下重复项没有任何合法用途，
				// 纯噪声——重复会让「按 content 对照清单」的注入/摘要自相矛盾。
				if seen[it.Content] {
					return "", fmt.Errorf("第 %d 项与前面的项内容重复（%q）——清单项必须唯一，请合并或改写", i+1, it.Content)
				}
				seen[it.Content] = true
			}
			if active > 1 {
				return "", fmt.Errorf("active 状态有 %d 项——同一时刻只能有一项进行中，请先完成或改回 pending", active)
			}

			if fn := todoSinkFrom(ctx); fn != nil {
				fn(a.Items)
			}
			return formatTodoResult(a.Items), nil
		},
	}
}

// formatTodoResult 把清单渲染成模型可读的确认文本。
//
// 回显强化（todo 契约）：模型每次写清单看到的反馈带**结构化三态计数**与当前焦点，
// 全 done 时追加收敛行——「清单已全部完成」是模型侧唯一的收敛信号（没有它，模型
// 会在任务完成后反复改写清单）。计数与 UI 徽标共用 sessiondata.CountTodoStatus 的
// 口径（一份事实源，模型看到的数字与用户看到的一致）。
func formatTodoResult(items []TodoItem) string {
	if len(items) == 0 {
		return "清单已清空。"
	}
	done, act, pend := sessiondata.CountTodoStatus(items)
	// 展示顺序：active 最前（当前焦点），pending 次之，done 最后——
	// 与人读清单的注意力顺序一致，而不是模型写入的随机顺序。
	order := map[string]int{"active": 0, "pending": 1, "done": 2}
	sorted := append([]TodoItem(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return order[sorted[i].Status] < order[sorted[j].Status] })
	var b strings.Builder
	for _, it := range sorted {
		mark := map[string]string{"done": "✓", "active": "▶", "pending": "·"}[it.Status]
		fmt.Fprintf(&b, "%s %s\n", mark, it.Content)
	}
	out := fmt.Sprintf("清单已更新（%d 项：done %d · 进行 %d · 待办 %d）:\n%s",
		len(items), done, act, pend, b.String())
	if act == 0 && pend == 0 {
		out += "\n清单已全部完成。"
	}
	return out
}

