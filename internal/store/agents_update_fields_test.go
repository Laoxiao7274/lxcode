// UpdateAgentFields（agent_write 的落库路径）的行为测试：真库 + 真种子——
// 共享校验的各条硬校验、字段级保留、user_modified 置位（种子热更新不覆盖
// agent 代用户做的修改）。
package store

import (
	"slices"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func strp(s string) *string { return &s }

func TestUpdateAgentFields(t *testing.T) {
	s := openTestStore(t)

	t.Run("改模板与白名单：落库 + 字段级保留", func(t *testing.T) {
		u := sessiondata.AgentFieldUpdate{
			Prompt: strp("你是测试 Agent（v2）。"),
			Tools:  []string{"read_file", "search", "web_search"},
		}
		if err := s.UpdateAgentFields("tester", u); err != nil {
			t.Fatalf("合法更新失败: %v", err)
		}
		defs, err := s.ListAgents()
		if err != nil {
			t.Fatal(err)
		}
		var def sessiondata.AgentDef
		for _, d := range defs {
			if d.ID == "tester" {
				def = d
			}
		}
		if def.Prompt != "你是测试 Agent（v2）。" {
			t.Fatalf("模板未落库: %q", def.Prompt)
		}
		if !slices.Equal(def.Tools, []string{"read_file", "search", "web_search"}) {
			t.Fatalf("白名单未全量替换: %v", def.Tools)
		}
		// 未提供的字段原样保留
		if def.Desc == "" || def.Approval != "confirm" || !def.Enabled {
			t.Fatalf("未提供的字段被改动了: %+v", def)
		}
		// user_modified=1：种子热更新从此跳过这行（用户侧编辑的语义）
		var modified int
		if err := s.db.QueryRow(`SELECT user_modified FROM agents WHERE id = 'tester'`).Scan(&modified); err != nil {
			t.Fatal(err)
		}
		if modified != 1 {
			t.Fatalf("user_modified 应置 1（种子热更新不覆盖）")
		}
	})

	t.Run("清空技能绑定合法（不绑技能是合法配置）", func(t *testing.T) {
		if err := s.UpdateAgentFields("tester", sessiondata.AgentFieldUpdate{Skills: []string{}}); err != nil {
			t.Fatalf("清空技能绑定失败: %v", err)
		}
	})

	t.Run("硬校验：各拒绝路径", func(t *testing.T) {
		cases := []struct {
			name string
			u    sessiondata.AgentFieldUpdate
			want string
		}{
			{"模板空白", sessiondata.AgentFieldUpdate{Prompt: strp("  ")}, "不能为空"},
			{"白名单清空", sessiondata.AgentFieldUpdate{Tools: []string{}}, "不能清空"},
			{"未知工具名", sessiondata.AgentFieldUpdate{Tools: []string{"read_file", "ghost_tool"}}, "ghost_tool"},
			{"未知技能 id", sessiondata.AgentFieldUpdate{Skills: []string{"ghost_skill"}}, "ghost_skill"},
		}
		for _, tc := range cases {
			err := s.UpdateAgentFields("tester", tc.u)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s: 应报 %q，得 %v", tc.name, tc.want, err)
			}
		}
		// 主 Agent 拒改（它的工具白名单是结构性的）
		err := s.UpdateAgentFields("main", sessiondata.AgentFieldUpdate{Prompt: strp("x")})
		if err == nil || !strings.Contains(err.Error(), "主 Agent") {
			t.Fatalf("主 Agent 应拒改: %v", err)
		}
		// id 不存在
		err = s.UpdateAgentFields("ghost", sessiondata.AgentFieldUpdate{Prompt: strp("x")})
		if err == nil || !strings.Contains(err.Error(), "不存在") {
			t.Fatalf("不存在的 id 应报错: %v", err)
		}
		// 拒绝后历史不被部分写入（fail-closed）：tester 的模板仍是 v2
		defs, _ := s.ListAgents()
		for _, d := range defs {
			if d.ID == "tester" && strings.Contains(d.Prompt, "v3") {
				t.Fatalf("被拒绝的更新不应部分落库: %q", d.Prompt)
			}
		}
	})
}
