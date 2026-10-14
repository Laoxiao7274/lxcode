// Go/TS 协议一致性黄金测试（Go 侧）。
//
// golden.json 是协议 wire 形状的**快照事实源**，前端测试
// （frontend/tests/protocol-golden.test.mjs）读同一份文件做 TS 侧校验。
// 两层防护：
//
//  1. TestGoldenRoundTrip：样本序列化结果与 testdata/golden.json 逐字节一致
//     ——wire 形状变化（字段名/tag/新样本）没有重生成 golden 就会爆；
//  2. TestGoldenCoversAllWireFields：对样本里出现过的每个结构体类型，反射出
//     它的全部 json 字段，要求**并集覆盖**——给 wire 类型加字段而没在任何
//     样本里设值，直接失败（强制开发者同时更新 golden 与 types.ts）。
//     反向也查：样本里出现的键必须是声明的 wire 字段（tag 拼错早爆）。
//
// 重新生成：go test ./internal/protocol -run TestGolden -update
// （或环境变量 LXCODE_UPDATE_GOLDEN=1）。
//
// 样本纪律：
//   - 「全量样本」把该类型的每个 wire 字段都设成非零值（omitempty 字段才会
//     出现在 wire 上，覆盖检查才有依据）；
//   - 「最小样本」演示**缺席 vs 显式零值**语义（context 整键缺席 = 未知、
//     busy:false 是显式零值、data:"" 是 base64 字段无 omitempty 的显式空）；
//   - _type 是该 wire 形状对应的 **types.ts 接口名**（前端测试据此做键声明
//     检查）；没有 1:1 对应接口的（适配层做了映射的，如 ChatHistoryResult →
//     HistorySnapshot 是 camelCase 重映射）留空，由前端测试的抽检表兜底。
package protocol

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
	"github.com/moyunteng/lxcode/internal/websearch"
)

var updateGolden = flag.Bool("update", false, "重写 internal/protocol/testdata/golden.json")

// goldenSample 是 golden.json 里的一个条目：Type 是对应的 types.ts 接口名
// （空 = 无 1:1 接口，前端测试只做往返与抽检），Value 是 wire 形状样本。
type goldenSample struct {
	Type  string `json:"_type"`
	Value any    `json:"value"`
}

const goldenPath = "testdata" + string(filepath.Separator) + "golden.json"

func goldenSamples() map[string]goldenSample {
	return map[string]goldenSample{
		// ---- chat 域：发送 / 事件 / 确认 / todo ----

		// 全量样本：chat.send 的每个 wire 字段都在（SendOptions 镜像其请求级子集）。
		"chatSendParamsFull": {"SendOptions", ChatSendParams{
			SessionID: "s1",
			Text:      "帮我看看这个报错",
			Effort:    EffortHigh,
			Approval:  ApprovalAuto,
			Agent:     "reviewer",
			Images:    []ChatSendImage{{Mime: "image/png", Data: "aGVsbG8=", Name: "截图.png"}},
			Files:     []ChatSendFile{{Name: "日志.txt", Data: "MjAyNi0xMC0xMw=="}},
		}},
		// 最小样本：effort/approval/agent/images/files **整键缺席**（不是零值）。
		"chatSendParamsMinimal": {"SendOptions", ChatSendParams{SessionID: "s1", Text: "在吗"}},
		// 图片的两种传输形态：path 形态 data 为**显式空串**（无 omitempty），
		// base64 形态 path **整键缺席**。
		"chatSendImagePathForm": {"ChatSendImage", ChatSendImage{
			Mime: "image/png", Data: "", Name: "截图.png", Path: `C:\Users\xzy\Pictures\shot.png`,
		}},
		"chatSendFileBase64":   {"ChatSendFile", ChatSendFile{Name: "报告.pdf", Data: "JVBERi0xLjQ="}},
		"chatSendFilePathForm": {"ChatSendFile", ChatSendFile{Name: "报告.pdf", Data: "", Path: `C:\Users\xzy\Docs\报告.pdf`}},
		// 文件夹相对路径形态（拖文件夹改造，2026-12）：rel_path 只做展示，
		// 落盘名恒为随机 hex（展示名/相对路径与磁盘名解耦）。
		"chatSendFileRelPath": {"ChatSendFile", ChatSendFile{Name: "auth.md", Data: "IyBBUEk=", RelPath: "docs/api/auth.md"}},

		// chat.done 全量：context/stats/每轮计时/模型都在（带 _type: AgentEvent——
		// done 成员的 wire 载荷）。
		"chatDoneFull": {"AgentEvent", DoneParams{
			SessionID: "s1",
			Message: llm.Message{
				Role: "assistant", Content: "改好了，构建通过。", Seq: 7,
				FirstTokenMs: 420, DurationMs: 5230, Model: "deepseek-v3", UsageTokens: 512,
			},
			UsageTokens:  512,
			FinishReason: "stop",
			Context: &ContextUsage{Used: 48210, Window: 128000, System: 3120, Tools: 8400,
				ToolResults: 12200, Messages: 18100, Reasoning: 5590, Estimated: false},
			Stats: &SessionStats{Turns: 3, Steps: 9, LLMMs: 14200, ToolMs: 8600,
				TTFTMs: 390, TTFTSteps: 9, DecodeMs: 9800, DecodeTokens: 4096,
				InputTokens: 120000, CacheReadTokens: 98000, CacheWriteTokens: 4000,
				OutputTokens: 8200, LegacyTokens: 1500},
			FirstTokenMs: 420, DurationMs: 5230, Model: "deepseek-v3",
		}},
		// 子会话的 done：dispatch_id 在，context/stats/计时/模型**整键缺席**
		//（子轮的占用与统计不进主指示器；没有首字就是没有，不发 0 冒充）。
		"chatDoneDispatch": {"AgentEvent", DoneParams{
			SessionID: "child-1", DispatchID: "d1",
			Message:     llm.Message{Role: "assistant", Content: "子任务完成。"},
			UsageTokens: 210, FinishReason: "stop",
		}},

		// 工具结果事件带 attachments（share_file）与 dispatch_id。
		"toolResultWithAttachments": {"AgentEvent", ToolResultParams{
			SessionID: "s1", ID: "call_1", Name: "share_file",
			Content: "已共享到对话", IsError: false, DispatchID: "d1",
			Attachments: []llm.AttachmentRef{
				{Path: "s1/ab12cd.png", Name: "截图.png", Size: 20480, Mime: "image/png",
					Kind: llm.AttachmentKindImage, Caption: "报错截图"},
				{Path: "s1/ef34gh.log", Name: "日志.txt", Size: 512, Kind: llm.AttachmentKindFile},
			},
		}},

		// ask 形态的确认门：kind/options/dispatch_id 全量。
		"confirmRequestAsk": {"ConfirmRequest", ConfirmRequest{
			SessionID: "s1", ID: "ask_1", Name: "ask_user",
			Arguments: `{"question":"用哪种方案？"}`, Prompt: "用哪种方案？",
			DispatchID: "d1", Kind: "ask", Options: []string{"方案 A", "方案 B"},
		}},

		"todoItem":          {"TodoItem", tools.TodoItem{Content: "拆分 protocol.go", Status: "done"}},
		"todoUpdatedParams": {"", TodoUpdatedParams{SessionID: "s1", Items: []tools.TodoItem{{Content: "拆分 protocol.go", Status: "done"}, {Content: "golden 往返测试", Status: "active"}}}},
		// 带归属（子会话上抛）：dispatch_id 是可选键——缺席 = 主会话自己的清单
		"todoUpdatedParamsDispatched": {"", TodoUpdatedParams{SessionID: "s1", DispatchID: "d1", Items: []tools.TodoItem{{Content: "定位泄漏点", Status: "active"}}}},

		// ---- chat 域：历史回放 ----

		// 全量历史：检查点/统计/上下文/权限档/待确认全在。message 数组覆盖
		// llm.Message 的全部 wire 字段（images/attachments/tool_calls/tool_call_id/
		// reasoning_content/每轮簿记）。_type 留空：前端 HistorySnapshot 是
		// camelCase 重映射后的形态，不是逐键镜像（映射在适配层）。
		"chatHistoryResultFull": {"", ChatHistoryResult{
			Messages: []llm.Message{
				{Role: "user", Content: "看看这个报错", Seq: 1,
					Images:      []llm.ImageRef{{Path: "s1/ab12cd.png", Mime: "image/png"}},
					Attachments: []llm.AttachmentRef{{Path: "s1/ab12cd.png", Name: "截图.png", Size: 20480, Mime: "image/png", Kind: llm.AttachmentKindImage}}},
				{Role: "assistant", Content: "", ReasoningContent: "先看调用栈……", Seq: 2,
					ToolCalls: []llm.ToolCall{{ID: "call_1", Type: "function", Index: 1,
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{Name: "read_file", Arguments: `{"path":"main.go"}`}}}},
				{Role: "tool", Content: "…文件内容…", Seq: 3, ToolCallID: "call_1", DurationMs: 120},
				{Role: "assistant", Content: "改好了。", Seq: 4, FirstTokenMs: 420, DurationMs: 5230, Model: "deepseek-v3", UsageTokens: 512},
			},
			Busy:        true,
			Pending:     &ConfirmRequest{SessionID: "s1", ID: "call_2", Name: "bash", Arguments: `{"command":"go test ./..."}`, Prompt: "执行 go test？"},
			SessionID:   "s1",
			Todos:       []tools.TodoItem{{Content: "修 bug", Status: "active"}},
			Context:     &ContextUsage{Used: 48210, Window: 128000, System: 3120, Tools: 8400, ToolResults: 12200, Messages: 18100, Reasoning: 5590},
			Stats:       &SessionStats{Turns: 3, Steps: 9, LLMMs: 14200, ToolMs: 8600, TTFTMs: 390, TTFTSteps: 9, DecodeMs: 9800, DecodeTokens: 4096, InputTokens: 120000, CacheReadTokens: 98000, CacheWriteTokens: 4000, OutputTokens: 8200},
			Checkpoints: []int{0},
			Model:       "deepseek-v3",
			Approval:    ApprovalConfirm,
		}},
		// 最小历史：context/stats/pending/todos/checkpoints/model/approval 全部
		// **整键缺席**（未知 = 缺席，不编数）；busy 是显式 false（无 omitempty）。
		"chatHistoryResultMinimal": {"", ChatHistoryResult{
			Messages: []llm.Message{{Role: "user", Content: "在吗"}},
			Busy:     false,
		}},

		// ---- chat 域：上下文占用（缺席 vs 估算语义）----

		// 全量分类 + 估算位（estimated=true = 按密度折算，不是 provider 真实用量）。
		"contextUsageEstimated": {"ContextUsage", ContextUsage{
			Used: 18200, Window: 65536, System: 2100, Tools: 4400,
			ToolResults: 5100, Messages: 5300, Reasoning: 1300, Estimated: true,
		}},
		// 未知/冷态：只有 used，window 与分类**整键缺席**（前端显示中性态）。
		"contextUsageUnknown": {"ContextUsage", ContextUsage{Used: 512}},

		// ---- chat 域：压缩 / 撤回 ----

		// 整段会话统计的独立样本（wire 键全集；legacy_tokens 是早期口径，单独给出）。
		"sessionStatsFull": {"SessionStats", SessionStats{
			Turns: 12, Steps: 34, LLMMs: 58200, ToolMs: 21400,
			TTFTMs: 380, TTFTSteps: 34, DecodeMs: 41200, DecodeTokens: 18200,
			InputTokens: 486000, CacheReadTokens: 402000, CacheWriteTokens: 18000,
			OutputTokens: 26400, LegacyTokens: 3200,
		}},
		"compactResult":    {"CompactOutcome", CompactResult{Compacted: false, Reason: "历史太短，摘要没有收益", Before: 9100, After: 9100, Shadowed: 3}},
		"chatRewindResult": {"RewindOutcome", ChatRewindResult{Removed: 0}}, // 幂等空操作：显式 0，不是缺席

		// ---- 会话管理域 ----

		"sessionMeta":          {"", SessionMeta{ID: "s1", Title: "修登录 bug", UpdatedAt: "2026-10-13T10:30:00+08:00", Messages: 24, Archived: true, Workspace: "lxcode"}},
		"sessionArchiveResult": {"", SessionArchiveResult{Archived: true, ReleasedWorktree: false, ReleaseError: "工作区有未提交改动，未释放"}},
		"projectInstructionsResult": {"ProjectInstructions", ProjectInstructionsResult{
			Path: `C:\Projects\lxcode\AGENTS.md`, Content: "# 项目守则……", Exists: true, Note: "（示例）上次读取时文件过大被截断",
		}},

		// ---- 目录域 ----

		"agentEntry": {"AgentAdminEntry", AgentEntry{
			ID: "reviewer", Name: "评审员", Desc: "代码评审", Color: "#3b82f6",
			Model: "deepseek-v3", Tools: []string{"read_file", "search"}, Workflow: "评审工作流",
			Skills: []string{"code-review"}, Delegates: []string{}, Approval: ApprovalConfirm,
			Enabled: true, IsMain: true, Prompt: "你是评审 Agent……", Protocol: "主 Agent 协议", Custom: false,
		}},
		// 两个条目覆盖 stdio/sse 两种 transport 与 connected/error 两种运行态。
		"mcServerEntry": {"AgentAdminMcServer", McServerEntry{
			ID: "fs", Desc: "文件系统", Transport: "stdio", Command: "npx", Args: []string{"-y", "@mcp/fs"},
			Env: map[string]string{"FS_ROOT": "/tmp"}, URL: "", Enabled: true, Custom: true,
			Status: "connected", ToolCount: 11, LastError: "", Stderr: "[warn] experimental",
		}},
		"mcServerEntrySse": {"AgentAdminMcServer", McServerEntry{
			ID: "remote", Desc: "远程 MCP", Transport: "sse", URL: "https://mcp.example.com/sse",
			Enabled: true, Custom: false, Status: "error", LastError: "initialize 失败: context deadline exceeded",
		}},
		// 形状样本：一个条目覆盖全部 wire 键（risk/source/command 的取值组合在
		// 真实数据里按 source 互斥，这里不为语义自洽服务）。
		"toolEntry": {"AgentAdminTool", ToolEntry{
			ID: "read_file", Desc: "读文件", Risk: "low", Source: "binary",
			Params: []ToolParamEntry{{Name: "path", Type: "string", Required: true, Desc: "文件路径"}},
			Doc:    "按行读取", Server: "smoke-mcp", Command: "rg {pattern} {path}",
			Example: `{"path":"main.go"}`, PackageFile: "pack.yaml", Custom: false,
		}},
		// 模型注册表快照（ModelEntry = config.ModelConfig 的 wire 形态，含嵌套
		// capabilities 与 updated_at；_type 留空——顶层是注册表不是单条目）。
		"modelListResult": {"", ModelListResult{
			Models: []config.ModelConfig{{
				ID: "deepseek-v3", DisplayName: "DeepSeek V3", BaseURL: "https://api.deepseek.com/v1",
				APIKey: "sk-**", Format: "openai", Model: "deepseek-chat",
				ContextWindow: 128000, MaxOutputTokens: 8192,
				Capabilities: config.Capabilities{Tools: true, Vision: false, JSONOut: true, Reasoning: true, StreamTools: true},
				Enabled:      true, UpdatedAt: time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC),
			}},
			Roles: map[string]string{"default": "deepseek-v3"},
		}},

		// ---- git 域 ----

		"gitOverviewResult": {"GitOverview", GitOverviewResult{
			Path: `C:\Users\xzy\Desktop\my\lxcode`, Branch: "master",
			Dirty: []GitChange{{Path: "internal/protocol/protocol.go", Kind: "modified"}},
			Branches: []GitBranchInfo{
				{Name: "master", Current: true, Ahead: 2, Behind: 0},
				{Name: "lxcode/session-s1", Current: false, Ahead: 1, Behind: 3,
					SessionID: "s1", SessionTitle: "已归档会话", Archived: true,
					HasWorktree: true, WorktreePath: `C:\Sessions\worktrees\p1\s1`, DirtyCount: 2, Merged: true},
			},
			Commits: []GitCommitInfo{{Hash: "abc1234", Message: "重构 protocol 包", Author: "xzy", When: "2026-10-13T10:00:00+08:00"}},
		}},
		// 主检出分支：会话字段**整键缺席**。
		"gitBranchInfoMain": {"GitBranchInfo", GitBranchInfo{Name: "master", Current: true, Ahead: 0, Behind: 0}},

		// ---- settings 域（搜索渠道 + 行为开关）----

		"searchChannelsResult": {"SearchChannelsSnapshot", SearchChannelsResult{
			Channels: []websearch.Channel{
				{ID: "tavily", Label: "Tavily", Desc: "聚合搜索", DocURL: "https://tavily.com", EnvVar: "TAVILY_API_KEY",
					NeedsKey: true, NeedsURL: false, AcceptsKey: true, OptIn: false, DefaultReady: false,
					Category: "general", CategoryLabel: "通用",
					APIKey: "tvly-***", BaseURL: "https://api.tavily.com",
					OptionSpecs: []websearch.OptionSpec{{Key: "search_depth", Label: "搜索深度", Placeholder: "basic", Hint: "basic|advanced", EnvVar: "TAVILY_DEPTH", Required: true, Default: "basic", Choices: []string{"basic", "advanced"}}},
					Options:     map[string]string{"search_depth": "basic"},
					Stored:      true, Enabled: true, Primary: true, Configured: true},
				{ID: "exa-free", Label: "Exa 免配置通道", Desc: "零配置直连", NeedsKey: false, NeedsURL: false,
					AcceptsKey: false, DefaultReady: true, Category: "free", CategoryLabel: "免费",
					Stored: false, Enabled: true, Primary: false, Configured: true},
				{ID: "ddg", Label: "DuckDuckGo", Desc: "零配置抓取", NeedsKey: false, NeedsURL: false,
					AcceptsKey: false, OptIn: true, Category: "free", CategoryLabel: "免费",
					Stored: false, Enabled: false, Primary: false, Configured: false},
			},
			Primary: "tavily", Ready: true,
		}},
		"searchTestResult": {"SearchTestResult", SearchTestResult{
			Provider: "tavily", Answer: "Go 1.22 于 2024 年发布",
			Results:   []websearch.Result{{Title: "Go release notes", URL: "https://go.dev/doc/devel/release", Snippet: "…"}},
			ElapsedMS: 742,
		}},
		"todoPromptResult": {"", TodoPromptResult{TodoPrompt: "strict"}},

		// ---- jobs 域 ----

		"jobInfo": {"JobInfo", JobInfo{
			ID: "job1", Kind: "bash", Label: "npm run build", Status: "completed", EndedBy: "user",
			Detail: "退出码 0", SessionID: "s1", OwnerSessionID: "s1",
			StartedAt: "2026-10-13T10:00:00+08:00", FinishedAt: "2026-10-13T10:01:12+08:00",
			OutputTail: "vite build 完成", OutputPath: `C:\ProgramData\lxcode\logs\job1.log`,
		}},
		"jobLogResult": {"JobLogResult", JobLogResult{Data: "…全量日志…", Truncated: true}},

		// ---- 子会话派发（chat 域）----

		"dispatchStartParams": {"", DispatchStartParams{
			OwnerSessionID: "s1", DispatchID: "d1", SessionID: "child-1",
			AgentID: "reviewer", AgentName: "评审员", AgentColor: "#3b82f6", Task: "评审这次的改动",
			Todos: []tools.TodoItem{{Content: "读改动", Status: "done"}, {Content: "写意见", Status: "active"}},
		}},
	}
}

// TestGoldenRoundTrip 把样本序列化结果与 golden.json 逐字节比对。
// wire 形状变了（或样本改了）而没重新生成，这里第一时间爆。
func TestGoldenRoundTrip(t *testing.T) {
	doc := goldenSamples()
	want, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("序列化 golden 样本: %v", err)
	}
	want = append(want, '\n')

	if *updateGolden || os.Getenv("LXCODE_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("创建 testdata 目录: %v", err)
		}
		if err := os.WriteFile(goldenPath, want, 0o644); err != nil {
			t.Fatalf("写 golden.json: %v", err)
		}
		t.Logf("golden.json 已重新生成（%d 个样本）", len(doc))
	}

	got, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("读 %s: %v（首次生成：go test ./internal/protocol -run TestGolden -update）", goldenPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden.json 与内存样本不一致——wire 形状变了？确认 types.ts 同步后重新生成：\n"+
			"  go test ./internal/protocol -run TestGolden -update\n--- golden ---\n%s\n--- 内存 ---\n%s",
			got, want)
	}

	// 往返稳定性：golden 反序列化再序列化必须与自身规范化结果一致
	//（golden 是确定性快照的前提；注意 struct 声明序与 map 键序不同，
	// 两侧都规范化到 map 序再比）。
	var round any
	if err := json.Unmarshal(got, &round); err != nil {
		t.Fatalf("golden.json 反序列化: %v", err)
	}
	canonical, err := json.Marshal(round)
	if err != nil {
		t.Fatalf("规范化序列化: %v", err)
	}
	var round2 any
	if err := json.Unmarshal(canonical, &round2); err != nil {
		t.Fatalf("规范化结果反序列化: %v", err)
	}
	again, err := json.Marshal(round2)
	if err != nil {
		t.Fatalf("再序列化: %v", err)
	}
	if !bytes.Equal(again, canonical) {
		t.Fatalf("golden.json 往返不稳定（序列化→反序列化→序列化结果不同）")
	}
}

// marshalerType 用于跳过自定义序列化的类型（如 time.Time）——它们的 wire 键
// 不来自 struct tag，反射对不上。
var marshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()

// wireFields 反射出类型的全部 wire 键名（json tag；"-" 跳过；匿名内嵌结构体
// 按提升字段递归）。
func wireFields(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			for k := range wireFields(f.Type) {
				out[k] = true
			}
			continue
		}
		tag, ok := f.Tag.Lookup("json")
		if !ok {
			continue
		}
		name := strings.SplitN(tag, ",", 2)[0] // strings import 由调用方保证
		if name == "-" || name == "" {
			continue
		}
		out[name] = true
	}
	return out
}

// coverage 汇总每个结构体类型在样本里实际出现过的 wire 键的并集。
type coverage struct {
	keys  map[string]map[string]bool // 类型名 → 出现过的键并集
	types map[string]reflect.Type    // 类型名 → reflect.Type
}

// collectKeys 递归遍历样本值，把遇到的每个结构体实例的 wire 顶层键记进 cov。
func collectKeys(v reflect.Value, cov *coverage, depth int) {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			collectKeys(v.Elem(), cov, depth)
		}
	case reflect.Struct:
		typ := v.Type()
		name := typ.String()
		if cov.keys[name] == nil {
			cov.keys[name] = map[string]bool{}
			cov.types[name] = typ
		}
		if typ.Implements(marshalerType) {
			return // 自定义序列化（time.Time 等）：键不来自 tag，跳过
		}
		if b, err := json.Marshal(v.Interface()); err == nil {
			var m map[string]json.RawMessage
			if json.Unmarshal(b, &m) == nil {
				for k := range m {
					cov.keys[name][k] = true
				}
			}
		}
		if depth <= 0 {
			return
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.PkgPath != "" {
				continue // 未导出字段
			}
			collectKeys(v.Field(i), cov, depth-1)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			collectKeys(v.Index(i), cov, depth-1)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			collectKeys(v.MapIndex(k), cov, depth-1)
		}
	}
}

// TestGoldenCoversAllWireFields：样本里出现过的每个结构体类型，其全部 wire
// 字段必须被某个样本覆盖（正向），且样本里出现的键必须都是声明的 wire 字段
// （反向，tag 拼错早爆）。给 wire 类型加字段而不更新 golden，这里失败。
func TestGoldenCoversAllWireFields(t *testing.T) {
	cov := &coverage{keys: map[string]map[string]bool{}, types: map[string]reflect.Type{}}
	for _, s := range goldenSamples() {
		collectKeys(reflect.ValueOf(s.Value), cov, 12)
	}
	if len(cov.keys) == 0 {
		t.Fatal("没有收集到任何结构体样本")
	}
	for name, keys := range cov.keys {
		typ := cov.types[name]
		if typ.Implements(marshalerType) {
			continue
		}
		want := wireFields(typ)
		for f := range want {
			if !keys[f] {
				t.Errorf("类型 %s 的 wire 字段 %q 没有任何 golden 样本覆盖：在 goldenSamples() 里给该字段设非零值，再重新生成 golden.json（-update）", name, f)
			}
		}
		for k := range keys {
			if !want[k] {
				t.Errorf("类型 %s 的样本出现了未声明的 wire 键 %q（tag 拼错或样本写错）", name, k)
			}
		}
	}
}
