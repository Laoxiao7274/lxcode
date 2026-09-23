// M2 上下文组装与 Agent 直选的测试：四层组合、白名单过滤、模型绑定、
// 权限取严、Agent 不存在/停用拒绝。
package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// stubResolver 是 AgentResolver 的测试桩（名单在 map 里）。
type stubResolver struct {
	entries map[string]*sessiondata.AgentContext
}

func (r *stubResolver) Resolve(id string) (*sessiondata.AgentContext, bool) {
	ac, ok := r.entries[id]
	return ac, ok
}

func (r *stubResolver) ResolveByName(name string) (*sessiondata.AgentContext, bool) {
	for _, ac := range r.entries {
		if ac.Def.Name == name {
			return ac, true
		}
	}
	return nil, false
}

func newAgentSession(t *testing.T) *Session {
	t.Helper()
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(config.ModelConfig{
		ID: "m1", BaseURL: "http://127.0.0.1:1/v1", Model: "m1", Enabled: true,
		Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	s := New(reg, tools.New(), func(Event) {})
	t.Cleanup(s.Close)
	return s
}

func TestComposeSystemPromptFourLayers(t *testing.T) {
	s := newAgentSession(t)
	ac := &sessiondata.AgentContext{
		Def: sessiondata.AgentDef{
			ID: "coder", Name: "代码 Agent", IsMain: false,
			Tools: []string{"read_file", "edit", "read_skill"}, Approval: "strict", Prompt: "你是代码 Agent。改动前先读。",
			Protocol: "定制协议：只写 Go。",
		},
		Workflow: &sessiondata.ModuleSpec{ID: "mc", Kind: "process", Desc: "最小改动", Body: "# 最小改动\n\n只动必需的部分。"},
		Skills:   []sessiondata.ModuleSpec{{ID: "gsap", Kind: "skill", Desc: "GSAP 动效", Body: "# GSAP\n\n入场收尾 clearProps。"}},
	}
	prompt := ComposeSystemPrompt(s.tools, "/proj/demo", ac, ac.Def.Tools, ProjectDocs{})

	// ① 协议层（定制替换内置默认）
	if !strings.Contains(prompt, "定制协议：只写 Go。") {
		t.Fatal("定制协议未生效（第一层）")
	}
	if strings.Contains(prompt, "你是执行 Agent") {
		t.Fatal("定制协议应整段替换内置默认")
	}
	// ② 流程模块全文注入（原子单元）
	if !strings.Contains(prompt, "只动必需的部分") {
		t.Fatal("流程模块正文未注入（第二层）")
	}
	// ② 技能 = 渐进披露：索引（id+摘要）进提示词，正文不进
	if !strings.Contains(prompt, "- gsap：GSAP 动效") {
		t.Fatal("技能索引（id+摘要）应出现在提示词")
	}
	if strings.Contains(prompt, "clearProps") {
		t.Fatal("技能正文不应注入提示词（渐进披露——read_skill 按需取）")
	}
	if !strings.Contains(prompt, "read_skill 取完整内容") {
		t.Fatal("应告知获取方式（read_skill）")
	}
	// ③ 自定义段
	if !strings.Contains(prompt, "改动前先读") {
		t.Fatal("自定义段未注入（第三层）")
	}
	// ④ 工作目录 + 白名单过滤的工具清单
	if !strings.Contains(prompt, "/proj/demo") {
		t.Fatal("工作目录说明缺失")
	}
	if !strings.Contains(prompt, "read_file") {
		t.Fatal("白名单内工具应出现")
	}
	if strings.Contains(prompt, "bash：") {
		t.Fatal("白名单外的 bash 不应出现在工具清单")
	}
	// 守则还在
	if !strings.Contains(prompt, "工作守则") {
		t.Fatal("工作守则丢失")
	}
}

func TestComposeMainAgentDelegatesInjection(t *testing.T) {
	s := newAgentSession(t)
	ac := &sessiondata.AgentContext{
		Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true,
			Tools: []string{"agent_dispatch"}},
		Delegates: []sessiondata.AgentDef{
			{ID: "coder", Name: "代码 Agent", Desc: "写代码", Enabled: true},
			{ID: "ops", Name: "运维 Agent", Desc: "跑命令", Enabled: false},
		},
	}
	prompt := ComposeSystemPrompt(s.tools, "", ac, ac.Def.Tools, ProjectDocs{})
	if !strings.Contains(prompt, "可委派名单") || !strings.Contains(prompt, "- coder（代码 Agent）：写代码") {
		t.Fatal("委派名单未注入（第四层动态注入——id（名字）：描述）")
	}
	if !strings.Contains(prompt, "已停用——不可分派") {
		t.Fatal("停用的子 Agent 应标注不可分派")
	}
	// 主 Agent 内置协议（未定制 → 默认调度协议）
	if !strings.Contains(prompt, "你是主 Agent（调度中枢）") {
		t.Fatal("主 Agent 默认协议缺失")
	}
}

func TestSendWithAgentWhitelistAndModel(t *testing.T) {
	s := newAgentSession(t)
	// 绑定不同模型验证绑定优先
	if err := s.reg.Add(config.ModelConfig{
		ID: "m2", BaseURL: "http://127.0.0.1:2/v1", Model: "m2", Enabled: true,
		Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	var gotModel config.ModelConfig
	var gotPrompt string
	s.SetStream(func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		gotModel = m
		gotPrompt = msgs[0].Content
		ch := make(chan llm.StreamEvent, 1)
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "ok"}}}
		close(ch)
		return ch, nil
	})
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"coder": {Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Model: "m2", Enabled: true,
			Tools: []string{"read_file"}, Approval: "auto"}},
	}})
	if err := s.Send("干活", WithAgent("coder")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, s)
	if gotModel.ID != "m2" {
		t.Fatalf("Agent 绑定模型应优先: %+v", gotModel)
	}
	if !strings.Contains(gotPrompt, "你是执行 Agent") {
		t.Fatal("提示词应含子 Agent 语境（默认执行协议）")
	}
	// 白名单过滤：read_file 在清单，bash 不在（提示词的工具清单是过滤后的）
	if !strings.Contains(gotPrompt, "read_file") {
		t.Fatal("白名单内工具应出现在提示词清单")
	}
	if strings.Contains(gotPrompt, "bash：") {
		t.Fatal("白名单外的 bash 不应出现在提示词清单")
	}
}

func TestSendAgentGuards(t *testing.T) {
	s := newAgentSession(t)
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"off": {Def: sessiondata.AgentDef{ID: "off", Name: "停用", Enabled: false}},
	}})
	if err := s.Send("x", WithAgent("nobody")); err == nil || !strings.Contains(err.Error(), "Agent 不存在") {
		t.Fatalf("不存在应拒绝: %v", err)
	}
	if err := s.Send("x", WithAgent("off")); err == nil || !strings.Contains(err.Error(), "已停用") {
		t.Fatalf("停用应拒绝: %v", err)
	}
}

func TestEffectiveApprovalTakesStricter(t *testing.T) {
	cases := []struct{ req, agent, want string }{
		{"", "", "confirm"},
		{"auto", "", "auto"},
		{"", "strict", "strict"},
		{"confirm", "auto", "confirm"}, // 请求级优先（显式授权面）
		{"", "auto", "auto"},
	}
	for _, c := range cases {
		if got := effectiveApproval(c.req, c.agent); got != c.want {
			t.Fatalf("effectiveApproval(%q,%q) = %q, want %q", c.req, c.agent, got, c.want)
		}
	}
}

func waitIdle(t *testing.T, s *Session) {
	t.Helper()
	for i := 0; i < 50 && s.Busy(); i++ {
		ch := make(chan struct{})
		go func() { time.Sleep(20 * time.Millisecond); close(ch) }()
		<-ch
	}
}
