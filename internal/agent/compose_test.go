// M2 上下文组装与 Agent 直选的测试：四层组合、白名单过滤、模型绑定、
// 权限取严、Agent 不存在/停用拒绝。
package agent

import (
	"context"
	"fmt"
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
	prompt := ComposeSystemPrompt(s.tools, "/proj/demo", ac, ac.Def.Tools, WorktreeInfo{}, false, ProjectDocs{}, nil)

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
			Tools: []string{"agent_dispatch"}, Delegates: []string{"coder"}},
		Delegates: []sessiondata.AgentDef{
			{ID: "coder", Name: "代码 Agent", Desc: "写代码", Enabled: true},
			{ID: "ops", Name: "运维 Agent", Desc: "跑命令", Enabled: false},
		},
	}
	prompt := ComposeSystemPrompt(s.tools, "", ac, ac.Def.Tools, WorktreeInfo{}, false, ProjectDocs{}, nil)
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

// 可委派注入按 Delegates 维度判定（不按 IsMain）：委派能力与主 Agent 身份
// 解耦的前置重构（assistant Agent 定义是后续功能提交）。
func TestComposeDelegatesInjectionByDelegatesDimension(t *testing.T) {
	s := newAgentSession(t)
	// 正例：IsMain=false 但声明了 Delegates → 注入
	nonMain := &sessiondata.AgentContext{
		Def: sessiondata.AgentDef{ID: "assistant", Name: "助手 Agent", IsMain: false,
			Delegates: []string{"coder"}, Tools: []string{"read_file"}},
		Delegates: []sessiondata.AgentDef{
			{ID: "coder", Name: "代码 Agent", Desc: "写代码", Enabled: true},
		},
	}
	prompt := ComposeSystemPrompt(s.tools, "", nonMain, nonMain.Def.Tools, WorktreeInfo{}, false, ProjectDocs{}, nil)
	if !strings.Contains(prompt, "可委派名单") || !strings.Contains(prompt, "- coder（代码 Agent）：写代码") {
		t.Fatal("IsMain=false 但 Delegates 非空 → 应注入委派名单")
	}
	// 反例：IsMain=true 但 Delegates 空 → 不注入
	mainNoDelegates := &sessiondata.AgentContext{
		Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true,
			Tools: []string{"read_file"}},
	}
	prompt = ComposeSystemPrompt(s.tools, "", mainNoDelegates, mainNoDelegates.Def.Tools, WorktreeInfo{}, false, ProjectDocs{}, nil)
	if strings.Contains(prompt, "可委派名单（agent_dispatch 的 agent 参数填下面的 id）") {
		t.Fatal("IsMain=true 但 Delegates 空 → 不应注入委派名单")
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

// ---- 助手协议（P0 地基）----

func TestDefaultProtocolAssistant(t *testing.T) {
	main := DefaultProtocol(ProtocolRoleMain)
	sub := DefaultProtocol(ProtocolRoleSub)
	assistant := DefaultProtocol(ProtocolRoleAssistant)
	if strings.TrimSpace(assistant) == "" {
		t.Fatal("assistant 协议文本不应为空")
	}
	if assistant == sub {
		t.Fatal("assistant 协议不应与 sub 相同（助理不是纯执行者——小事自己干、项目任务派发）")
	}
	if assistant == main {
		t.Fatal("assistant 协议不应与 main 相同（助手不是纯调度中枢）")
	}
	// 关键词钉子：run_task 是能力桥；派发与先问是纪律锚点；「感知用户当前
	// 活动」教法（2026-12）：当前状态/可用项目段是「我在做什么」的答案权，
	// session_search 是「聊过什么」的答案权，git log 不是；mcp_manage 是
	// MCP 管理面（2026-12）：助手知道自己有这功能、装前复述确认、不派发。
	for _, kw := range []string{"run_task", "派给", "先问", "当前状态", "session_search", "git log", "mcp_manage", "MCP 服务器", "复述 command/url", "自动绑定"} {
		if !strings.Contains(assistant, kw) {
			t.Fatalf("assistant 协议缺关键词 %q", kw)
		}
	}
	// ComposeSystemPrompt 的 role 推导：Space=assistant → 助手协议
	s := newAgentSession(t)
	ac := &sessiondata.AgentContext{
		Def: sessiondata.AgentDef{ID: "assistant", Name: "助手", Space: sessiondata.SpaceAssistant,
			Tools: []string{"run_task", "read_file"}, Delegates: []string{"coder"},
			Approval: "auto"},
		Delegates: []sessiondata.AgentDef{{ID: "coder", Name: "代码", Desc: "写代码", Enabled: true}},
	}
	prompt := ComposeSystemPrompt(s.tools, "", ac, ac.Def.Tools, WorktreeInfo{}, false, ProjectDocs{}, nil)
	if !strings.Contains(prompt, "个人智能体助手") {
		t.Fatal("Space=assistant 的 Agent 应使用助手内置协议")
	}
}

// 助手协议边界句按白名单条件化（2026-12 用户拍板：助手默认工具完全可配，
// 内置写类工具也可勾入）：
//   - 无写类工具（默认态）=「不能改项目里的文件——改动必须经 run_task 派进项目」；
//   - 白名单含 bash/edit/write_file 任一 =「你有写类工具……项目内的代码改动
//     仍优先经 run_task 派给项目里的 Agent」；
//   - 「你的工作目录不是任何项目」句两态恒保留（写类工具不改变助手域归属）；
//   - 其余段落两态同文（run_task 纪律/感知活动/MCP 管理不随边界句变）。
func TestAssistantProtocolBoundaryByAllowTools(t *testing.T) {
	readOnly := DefaultProtocol(ProtocolRoleAssistant)
	if !strings.Contains(readOnly, "不能改项目里的文件——改动必须经 run_task 派进项目") {
		t.Fatalf("默认态边界句应在（只读面）:\n%s", readOnly)
	}
	if strings.Contains(readOnly, "你有写类工具") {
		t.Fatalf("默认态不应出现写类边界句:\n%s", readOnly)
	}
	if !strings.Contains(readOnly, "你的工作目录不是任何项目") {
		t.Fatal("域归属句两态恒保留（只读态）")
	}
	for _, with := range [][]string{
		{"bash"}, {"edit"}, {"write_file"}, {"bash", "read_file", "run_task"},
	} {
		p := DefaultProtocol(ProtocolRoleAssistant, with)
		if !strings.Contains(p, "你有写类工具") || !strings.Contains(p, "项目内的代码改动仍优先经 run_task 派给项目里的 Agent") {
			t.Fatalf("含写类工具 %v 的白名单应换写类边界句:\n%s", with, p)
		}
		if strings.Contains(p, "不能改项目里的文件") {
			t.Fatalf("写类态不应保留只读边界句:\n%s", p)
		}
		if !strings.Contains(p, "你的工作目录不是任何项目") {
			t.Fatal("域归属句两态恒保留（写类态）")
		}
		// 其余段落与只读态同文：掐掉边界句后逐字一致。
		if trimBoundary(p) != trimBoundary(readOnly) {
			t.Fatalf("非边界段两态应同文:\n---只读---\n%s\n---写类---\n%s", readOnly, p)
		}
	}
	// ComposeSystemPrompt 端到端：allowTools 决定两态。
	s := newAgentSession(t)
	mkAC := func(tools []string) *sessiondata.AgentContext {
		return &sessiondata.AgentContext{
			Def: sessiondata.AgentDef{ID: "assistant", Name: "助手", Space: sessiondata.SpaceAssistant, Tools: tools},
		}
	}
	prompt := ComposeSystemPrompt(s.tools, "", mkAC([]string{"read_file"}), []string{"read_file"},
		WorktreeInfo{}, false, ProjectDocs{}, nil)
	if !strings.Contains(prompt, "不能改项目里的文件") {
		t.Fatalf("只读白名单的组装提示词应带只读边界句:\n%s", prompt)
	}
	prompt = ComposeSystemPrompt(s.tools, "", mkAC([]string{"bash"}), []string{"bash"},
		WorktreeInfo{}, false, ProjectDocs{}, nil)
	if !strings.Contains(prompt, "你有写类工具") {
		t.Fatalf("含 bash 的白名单组装提示词应带写类边界句:\n%s", prompt)
	}
}

// trimBoundary 掐掉两态边界句（按行掐，剩余文本用于同文比较）。
func trimBoundary(protocol string) string {
	var out []string
	for _, line := range strings.Split(protocol, "\n") {
		if strings.Contains(line, "不能改项目里的文件") || strings.Contains(line, "你有写类工具") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// 项目全景注入段（2026-12 助手项目全景三件套之三）：
//   - 助手语境注入（id 必须在场——run_task 的 project_id 是不可读随机串）；
//   - 上限 20 截断 + 尾注（详查走 project_list）；
//   - 非助手语境不注入（主域项目会话不吃这段上下文）；
//   - 空清单整段缺席（注入一个空段是噪声）。
func TestComposeProjectPanorama(t *testing.T) {
	s := newAgentSession(t)
	one := func(id, name string) sessiondata.ProjectStats {
		return sessiondata.ProjectStats{
			ProjectMeta: sessiondata.ProjectMeta{ID: id, Name: name, Path: "/repo/" + name},
			Sessions:    2, UpdatedAt: "2026-12-20 10:30",
		}
	}
	assistantAC := &sessiondata.AgentContext{
		Def: sessiondata.AgentDef{ID: "assistant", Name: "助手", Space: sessiondata.SpaceAssistant,
			Tools: []string{"run_task", "read_file"}},
	}
	// 助手语境：注入，id 在场
	prompt := ComposeSystemPrompt(s.tools, "", assistantAC, assistantAC.Def.Tools, WorktreeInfo{}, false,
		ProjectDocs{}, []sessiondata.ProjectStats{one("p-1", "lxcode")})
	for _, want := range []string{
		projectPanoramaHeader,
		"- p-1（lxcode）：/repo/lxcode，2 个会话，最近活动 2026-12-20 10:30",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("助手提示词缺 %q:\n%s", want, prompt)
		}
	}
	// 上限：21 个项目 → 前 20 行 + 尾注
	many := make([]sessiondata.ProjectStats, 0, 21)
	for i := 0; i < 21; i++ {
		many = append(many, one(fmt.Sprintf("p-%02d", i), fmt.Sprintf("proj%02d", i)))
	}
	prompt = ComposeSystemPrompt(s.tools, "", assistantAC, assistantAC.Def.Tools, WorktreeInfo{}, false, ProjectDocs{}, many)
	if got := strings.Count(prompt, "- p-"); got != 20 {
		t.Fatalf("注入应截到 20 行, got %d", got)
	}
	if !strings.Contains(prompt, "另有 1 个项目，用 project_list 详查") {
		t.Fatal("截断应带尾注（模型知道还有更多，详查走工具）")
	}
	// 非助手语境：同数据不注入（主域项目会话已有归属与 AGENTS.md 注入）
	mainAC := &sessiondata.AgentContext{
		Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true,
			Tools: []string{"agent_dispatch"}},
	}
	prompt = ComposeSystemPrompt(s.tools, "", mainAC, mainAC.Def.Tools, WorktreeInfo{}, false,
		ProjectDocs{}, []sessiondata.ProjectStats{one("p-1", "lxcode")})
	if strings.Contains(prompt, projectPanoramaHeader) {
		t.Fatalf("非助手语境不应注入项目全景:\n%s", prompt)
	}
	// 空清单：整段缺席（助手语境也没有空段噪声）
	prompt = ComposeSystemPrompt(s.tools, "", assistantAC, assistantAC.Def.Tools, WorktreeInfo{}, false, ProjectDocs{}, nil)
	if strings.Contains(prompt, projectPanoramaHeader) {
		t.Fatal("空清单不应注入空段")
	}
}

// 「当前状态」注入段（2026-12 感知用户当前活动三）：
//   - 助手语境注入（最近会话 + 此刻运行中，条目是 server 侧格式化好的文本）；
//   - 最近会话上限 3 条截断；
//   - 运行中的会话单列段首（与「最近」区分时态）；
//   - 非助手语境不注入；空快照整段缺席（与全景段同一纪律）。
func TestComposeCurrentActivity(t *testing.T) {
	s := newAgentSession(t)
	assistantAC := &sessiondata.AgentContext{
		Def: sessiondata.AgentDef{ID: "assistant", Name: "助手", Space: sessiondata.SpaceAssistant,
			Tools: []string{"run_task", "read_file"}},
	}
	snap := ActivitySnapshot{
		Running: []string{`「重构登录」（lxcode，2026-12-20 11:00）`},
		Recent: []string{
			`「重构登录」（lxcode，2026-12-20 11:00）`,
			`「修打包脚本」（site，2026-12-19 18:20）`,
			`「写周报」（主域 · 未分组，2026-12-19 09:00）`,
			`「更早的会话」（site，2026-12-18 09:00）`,
		},
	}
	prompt := ComposeSystemPrompt(s.tools, "", assistantAC, assistantAC.Def.Tools, WorktreeInfo{}, false,
		ProjectDocs{}, nil)
	if strings.Contains(prompt, CurrentActivityHeader) {
		t.Fatal("零值快照不应注入当前状态段")
	}
	// 有活动：注入——运行中在段首、最近条目在场
	prompt = composeSystemPrompt(s.tools, "", assistantAC, assistantAC.Def.Tools, WorktreeInfo{}, false,
		ProjectDocs{}, nil, snap)
	if !strings.Contains(prompt, CurrentActivityHeader) {
		t.Fatalf("助手提示词应注入当前状态段:\n%s", prompt)
	}
	if !strings.Contains(prompt, "- 正在运行：「重构登录」（lxcode，2026-12-20 11:00）") {
		t.Fatalf("运行中的会话应在段首:\n%s", prompt)
	}
	for _, want := range []string{
		"- 最近：「重构登录」（lxcode，2026-12-20 11:00）",
		"- 最近：「修打包脚本」（site，2026-12-19 18:20）",
		"- 最近：「写周报」（主域 · 未分组，2026-12-19 09:00）",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("最近会话条目缺 %q:\n%s", want, prompt)
		}
	}
	// 上限：最近会话最多 3 条（第 4 条不注入）
	if strings.Contains(prompt, "更早的会话") {
		t.Fatalf("最近会话应截到 3 条:\n%s", prompt)
	}
	// 非助手语境：同快照不注入
	mainAC := &sessiondata.AgentContext{
		Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true,
			Tools: []string{"agent_dispatch"}},
	}
	prompt = composeSystemPrompt(s.tools, "", mainAC, mainAC.Def.Tools, WorktreeInfo{}, false,
		ProjectDocs{}, nil, snap)
	if strings.Contains(prompt, CurrentActivityHeader) {
		t.Fatalf("非助手语境不应注入当前状态段:\n%s", prompt)
	}
	// 只有运行中、无最近（如全新库）：运行中条目在场
	runningOnly := ActivitySnapshot{Running: []string{`「跑测试」（lxcode）`}}
	prompt = composeSystemPrompt(s.tools, "", assistantAC, assistantAC.Def.Tools, WorktreeInfo{}, false,
		ProjectDocs{}, nil, runningOnly)
	if !strings.Contains(prompt, "- 正在运行：「跑测试」（lxcode）") {
		t.Fatalf("只有运行中会话时也应注入:\n%s", prompt)
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

// ---- 助手首次初始化（persona 为空时注入主动询问指令）----

func TestComposeAssistantOnboardingEmptyPersona(t *testing.T) {
	s := newAgentSession(t)
	assistantAC := &sessiondata.AgentContext{
		Def: sessiondata.AgentDef{ID: "assistant", Name: "助手", Space: sessiondata.SpaceAssistant,
			Tools: []string{"run_task", "read_file", "update_assistant_config"}},
	}
	prompt := ComposeSystemPrompt(s.tools, "", assistantAC, assistantAC.Def.Tools, WorktreeInfo{}, false,
		ProjectDocs{}, nil)
	if !strings.Contains(prompt, assistantOnboardingSection) {
		t.Fatalf("persona 为空的助手提示词应注入首次初始化指令:\n%s", prompt)
	}
	if !strings.Contains(prompt, "update_assistant_config") {
		t.Fatal("指令应指明保存通道（update_assistant_config）")
	}
}

func TestComposeAssistantOnboardingPersonaSet(t *testing.T) {
	s := newAgentSession(t)
	assistantAC := &sessiondata.AgentContext{
		Def: sessiondata.AgentDef{ID: "assistant", Name: "小灵", Space: sessiondata.SpaceAssistant,
			Prompt: "说话轻松幽默，回答简洁",
			Tools:  []string{"run_task", "read_file", "update_assistant_config"}},
	}
	prompt := ComposeSystemPrompt(s.tools, "", assistantAC, assistantAC.Def.Tools, WorktreeInfo{}, false,
		ProjectDocs{}, nil)
	if strings.Contains(prompt, assistantOnboardingSection) {
		t.Fatalf("persona 非空时不应再注入初始化指令:\n%s", prompt)
	}
	if !strings.Contains(prompt, "说话轻松幽默") {
		t.Fatalf("persona 应在自定义段出现:\n%s", prompt)
	}
}

func TestComposeOnboardingMainAgentNotInjected(t *testing.T) {
	s := newAgentSession(t)
	mainAC := &sessiondata.AgentContext{
		Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true,
			Tools: []string{"agent_dispatch"}},
	}
	prompt := ComposeSystemPrompt(s.tools, "", mainAC, mainAC.Def.Tools, WorktreeInfo{}, false,
		ProjectDocs{}, nil)
	if strings.Contains(prompt, assistantOnboardingSection) {
		t.Fatalf("主域 Agent 不该吃初始化指令:\n%s", prompt)
	}
}
