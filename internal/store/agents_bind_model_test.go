// BindAgentModel（子会话页切模型的 Agent 绑定更新路径）单测：
// 单列窄写不动其他字段、user_modified 置位（种子热更新跳过）、行不存在显式报错。
package store

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func TestBindAgentModelUpdatesOnlyModel(t *testing.T) {
	s := openAgentStore(t)
	agents, err := s.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	var coder *sessiondata.AgentDef = nil
	for i := range agents {
		if agents[i].ID == "coder" {
			coder = &agents[i]
			break
		}
	}
	if coder == nil {
		t.Fatal("种子名单里应有 coder")
	}
	before := *coder

	if err := s.BindAgentModel("coder", "m2"); err != nil {
		t.Fatalf("BindAgentModel 失败: %v", err)
	}
	after, err := s.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	var got *sessiondata.AgentDef = nil
	for i := range after {
		if after[i].ID == "coder" {
			got = &after[i]
			break
		}
	}
	if got == nil {
		t.Fatal("coder 消失了")
	}
	if got.Model != "m2" {
		t.Fatalf("model 应更新为 m2: %q", got.Model)
	}
	// 其他字段一个不碰
	if got.Name != before.Name || got.Desc != before.Desc || got.Prompt != before.Prompt ||
		got.Tools == nil || len(got.Tools) != len(before.Tools) ||
		got.Enabled != before.Enabled || got.IsMain != before.IsMain || got.Custom != before.Custom {
		t.Fatalf("BindAgentModel 只该动 model 列: before=%+v after=%+v", before, got)
	}
	// user_modified 置 1（种子热更新不覆盖用户改动——与 UpdateAgent 同一条纪律）
	var modified int
	if err := s.db.QueryRow(`SELECT user_modified FROM agents WHERE id = 'coder'`).Scan(&modified); err != nil {
		t.Fatal(err)
	}
	if modified != 1 {
		t.Fatalf("user_modified 应置 1: %d", modified)
	}
}

func TestBindAgentModelMissingAgentErrors(t *testing.T) {
	s := openAgentStore(t)
	if err := s.BindAgentModel("没有这个 Agent", "m2"); err == nil {
		t.Fatal("绑定行不存在应报错（不静默）")
	}
}
