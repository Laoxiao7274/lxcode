package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// TodoItem 是任务清单的一项。Status 三态对齐主流 coding agent 的习惯：
// pending（未开始）/ active（进行中，同一时刻应只有一项）/ done（完成）。
type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"` // pending | active | done
}

// validTodoStatus 是合法状态集合。
var validTodoStatus = map[string]bool{"pending": true, "active": true, "done": true}

// TodoWriteFn 是 todo 状态写入的注入接缝（agent.Session 接线）：
// 会话持有清单状态才能在 UI 上渲染（桌面壳的 TodoList 卡片），
// 工具层只做校验与格式化，状态归属不出工具包边界。
type TodoWriteFn func(items []TodoItem)

// TodoSink 的注册与读取（与 sessionSearch 同一注入模式）。
func (r *Registry) SetTodoSink(fn TodoWriteFn) {
	r.todoMu.Lock()
	r.todoSink = fn
	r.todoMu.Unlock()
}

func (r *Registry) getTodoSink() TodoWriteFn {
	r.todoMu.Lock()
	defer r.todoMu.Unlock()
	return r.todoSink
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
		Name: "todo",
		Description: "维护当前任务清单（全量替换式写入）。开始多步骤工作（≥3 步）先建清单，" +
			"每完成一步更新状态：把当前项标 done、下一项标 active。清单帮你自己不漏步骤，" +
			"也让用户看到进度。同一时刻最多一项 active。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Items []TodoItem `json:"items"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			active := 0
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
			}
			if active > 1 {
				return "", fmt.Errorf("active 状态有 %d 项——同一时刻只能有一项进行中，请先完成或改回 pending", active)
			}

			if fn := r.getTodoSink(); fn != nil {
				fn(a.Items)
			}
			return formatTodoResult(a.Items), nil
		},
	}
}

// formatTodoResult 把清单渲染成模型可读的确认文本。
func formatTodoResult(items []TodoItem) string {
	if len(items) == 0 {
		return "清单已清空。"
	}
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
	return fmt.Sprintf("清单已更新（%d 项，done %d）:\n%s",
		len(items), countDone(items), b.String())
}

func countDone(items []TodoItem) int {
	n := 0
	for _, it := range items {
		if it.Status == "done" {
			n++
		}
	}
	return n
}
