// read_skill 工具（渐进披露——M2 上下文组装的配套）：提示词只注入
// 技能索引（id + 摘要），模型需要完整内容时按 id 读取。没注入的技能
// 它当没有（提示词里看不到 = 不存在），挂再多技能也不撑上下文。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// SkillEntry 是注入给工具层的技能目录条目（id + 摘要 + 正文——
// 正文只在 read_skill 调用时返回给模型）。
type SkillEntry struct {
	ID   string
	Desc string
	Body string
}

// SkillSourceFn 是技能目录的实现约定：返回当前 Agent 白名单内的技能
// （server 装配时按会话选用的 Agent 解析注入）。
type SkillSourceFn func(ctx context.Context) []SkillEntry

// skillMu/skillSource 同 sessionSearch 的注入模式：工具无状态，
// 目录数据经接线注入（谁在跑这轮，谁白名单里的技能可读）。
var skillMu sync.Mutex
var skillSource SkillSourceFn

// SetSkillSource 注入技能目录实现（server/内核装配时调用）。返回先前
// 的实现（dispatch 子语境临时切换后恢复主语境目录用）。
func (r *Registry) SetSkillSource(fn SkillSourceFn) SkillSourceFn {
	skillMu.Lock()
	prev := skillSource
	skillSource = fn
	skillMu.Unlock()
	return prev
}

// GetSkillSource 读当前目录（快照/恢复用）。
func GetSkillSource() SkillSourceFn {
	skillMu.Lock()
	defer skillMu.Unlock()
	return skillSource
}

// readSkillDef 返回 read_skill 的注册声明。
func readSkillDef(r *Registry) *Def {
	return &Def{
		Name:        "read_skill",
		Description: "读取一个技能的完整内容（markdown）。提示词里只列了技能索引（名称与摘要）——需要某项技能的完整方法论时用本工具按 id 获取。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"id": { "type": "string", "description": "技能 id（提示词「可用技能」清单里列出的名称）" }
			},
			"required": ["id"]
		}`),
		Risk: RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			src := GetSkillSource()
			if src == nil {
				return "错误: 当前会话没有技能目录（Agent 未配置技能）。", nil
			}
			skills := src(ctx)
			for _, s := range skills {
				if s.ID == p.ID {
					if s.Body == "" {
						return fmt.Sprintf("技能 %s 没有正文（空内容）。", s.ID), nil
					}
					return s.Body, nil
				}
			}
			// 没找到：列出可用 id（自解释——模型可以立刻纠正）
			ids := make([]string, 0, len(skills))
			for _, s := range skills {
				ids = append(ids, s.ID)
			}
			return fmt.Sprintf("错误: 技能 %q 不在可用列表里（可用: %v）。提示词「可用技能」清单里列出的才能读。", p.ID, ids), nil
		},
	}
}
