// behavior.go —— 行为开关配置（config/behavior.json，与 models.json 同目录）。
//
// 与 remote.json 同一条模式：**小而关键的行为开关**，文件缺失/损坏不拒绝启动——
// 行为开关的坏配置不该拖垮对话主功能，回落默认值并在读侧如实生效。
// 热加载不靠 reloadLoop 主动 Reload：服务端每次组装提示词时现读（文件只有
// 几十字节，读一次的开销可以忽略），所以手改 / set 后**下一轮**立即生效；
// 广播由 server 侧负责（set 后与 reloadLoop 的变更检测各一处）。
package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/moyunteng/lxcode/internal/atomicfile"
)

// todo-first 指令注入档位（提示词层的行为开关，见 internal/agent/prompt.go）。
const (
	// TodoPromptStrict 严格（默认）：需要工具的任务必须先建清单（现有文案）。
	TodoPromptStrict = "strict"
	// TodoPromptNormal 常规：按任务复杂度用清单，小任务直接做完汇报。
	TodoPromptNormal = "normal"
	// TodoPromptOff 关闭：不注入任何 todo-first 指令。
	TodoPromptOff = "off"
)

// ValidTodoPromptMode 判断模式值合法（空串不算——空 = 缺省，由读侧补默认）。
func ValidTodoPromptMode(m string) bool {
	switch m {
	case TodoPromptStrict, TodoPromptNormal, TodoPromptOff:
		return true
	}
	return false
}

// Behavior 是 behavior.json 的载荷。
type Behavior struct {
	// TodoPrompt 是 todo-first 提示词的注入档位；空 = strict（读侧补默认）。
	TodoPrompt string `json:"todoPrompt"`
}

// Normalized 返回补齐缺省后的配置（空/非法值 = strict——非法值回落默认而不是
// 报错，与 LoadBehavior 的容错语义一致：行为开关宁可回默认也不让会话发不出）。
func (b Behavior) Normalized() Behavior {
	if !ValidTodoPromptMode(b.TodoPrompt) {
		return Behavior{TodoPrompt: TodoPromptStrict}
	}
	return b
}

// LoadBehavior 读行为配置。文件缺失/损坏/字段非法 = 默认（strict）。
func LoadBehavior(path string) Behavior {
	data, err := os.ReadFile(path)
	if err != nil {
		return Behavior{TodoPrompt: TodoPromptStrict}
	}
	var b Behavior
	if err := json.Unmarshal(data, &b); err != nil {
		return Behavior{TodoPrompt: TodoPromptStrict}
	}
	return b.Normalized()
}

// SaveBehavior 校验并原子写回（与 remote.json 同一份 atomicfile 实现）。
func SaveBehavior(path string, b Behavior) error {
	b = b.Normalized()
	data, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("序列化行为配置失败: %w", err)
	}
	return atomicfile.Write(path, data)
}
