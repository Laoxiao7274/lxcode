// 空消息清洗与配对兜底的测试（emptywire.go + openai.go/anthropic.go 的清洗）。
// 两条 wire 路径的空值语义对称，各有测试钉住；占位串注入的根因（LiteLLM 对
// 空 content 消息的清洗）以「wire 上不出现空 assistant/tool content」为准绳。
package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// buildReqBody 把请求体 JSON 解出来便于断言（messages 的 content 是 any）。
type reqBody struct {
	Messages []struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
	} `json:"messages"`
}

func marshalOpenAI(t *testing.T, msgs []Message) reqBody {
	t.Helper()
	b, err := json.Marshal(buildOpenAIRequest("m", msgs, requestOpts{}, false))
	if err != nil {
		t.Fatal(err)
	}
	var body reqBody
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// TestOpenAIAssistantToolCallsEmptyContentIsNull：① assistant 带 tool_calls 且
// content 为空 → wire 上是 content: null（不是 ""——空串会被 LiteLLM 清洗成
// 占位串混进上下文；null 是 OpenAI 规范允许的形态，且带 tool_calls 时非必填）。
func TestOpenAIAssistantToolCallsEmptyContentIsNull(t *testing.T) {
	tc := ToolCall{ID: "c1", Type: "function"}
	tc.Function.Name = "bash"
	tc.Function.Arguments = `{"command":"ls"}`
	msgs := []Message{
		{Role: "user", Content: "列一下文件"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{tc}},
		{Role: "tool", ToolCallID: "c1", Content: "a.txt"},
	}
	body := marshalOpenAI(t, msgs)
	if len(body.Messages) != 3 {
		t.Fatalf("消息数不符: %+v", body.Messages)
	}
	asst := body.Messages[1]
	if asst.Role != "assistant" {
		t.Fatalf("第二条应是 assistant: %+v", asst)
	}
	if string(asst.Content) != "null" {
		t.Fatalf("空 content + tool_calls 应发 null，得到: %s", asst.Content)
	}
}

// TestOpenAIPureEmptyAssistantSkipped：② 纯空 assistant（无 content 无
// tool_calls 无图）整条跳过不进 wire——空消息无信息量，发出去只会触发清洗。
func TestOpenAIPureEmptyAssistantSkipped(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: ""},
		{Role: "assistant", Content: "回答"},
	}
	body := marshalOpenAI(t, msgs)
	if len(body.Messages) != 2 {
		t.Fatalf("纯空 assistant 应被跳过: %+v", body.Messages)
	}
	if string(body.Messages[1].Content) != `"回答"` {
		t.Fatalf("后一条应原样保留: %+v", body.Messages[1])
	}
}

// TestOpenAIOnlyReasoningAssistantKept：只有思考链没有正文的 assistant 不是
// 空消息（ReasoningContent 是模型产出，有信息量），不跳过、content 发 null。
func TestOpenAIOnlyReasoningAssistantKept(t *testing.T) {
	msgs := []Message{{Role: "assistant", ReasoningContent: "思考中"}}
	body := marshalOpenAI(t, msgs)
	if len(body.Messages) != 1 {
		t.Fatalf("只含思考链的 assistant 不该被跳过: %+v", body.Messages)
	}
	if string(body.Messages[0].Content) != "null" {
		t.Fatalf("空正文应发 null（reasoning_content 另有键）: %s", body.Messages[0].Content)
	}
}

// TestOpenAIEmptyToolResultSentinel：③ 空 tool 结果发单空格哨兵
//（wireEmptyToolResult）——空串会被 LiteLLM 清洗；null 对 tool 消息不合法
//（OpenAI 官方 schema：tool 消息 content 是 Required[str]）。
func TestOpenAIEmptyToolResultSentinel(t *testing.T) {
	tc := ToolCall{ID: "c1", Type: "function"}
	tc.Function.Name = "bash"
	tc.Function.Arguments = "{}"
	msgs := []Message{
		{Role: "assistant", Content: "跑一下", ToolCalls: []ToolCall{tc}},
		{Role: "tool", ToolCallID: "c1", Content: ""}, // 模型只调工具没输出
	}
	body := marshalOpenAI(t, msgs)
	if len(body.Messages) != 2 {
		t.Fatalf("消息数不符: %+v", body.Messages)
	}
	tool := body.Messages[1]
	if tool.Role != "tool" || tool.ToolCallID != "c1" {
		t.Fatalf("第二条应是 c1 的结果: %+v", tool)
	}
	if string(tool.Content) != `" "` {
		t.Fatalf("空 tool 结果应发单空格哨兵，得到: %s", tool.Content)
	}
}

// TestOpenAIBackfillsMissingToolResult：④ 历史缺部分 tool 结果 → 合成补齐
//（严格端点要求 assistant.tool_calls 的每个 id 都有结果，缺一条 400 拒收整轮）。
func TestOpenAIBackfillsMissingToolResult(t *testing.T) {
	var a, b ToolCall
	a.ID, a.Type, a.Function.Name, a.Function.Arguments = "call-a", "function", "bash", `{}`
	b.ID, b.Type, b.Function.Name, b.Function.Arguments = "call-b", "function", "bash", `{}`
	msgs := []Message{
		{Role: "assistant", Content: "并行两个", ToolCalls: []ToolCall{a, b}},
		{Role: "tool", ToolCallID: "call-a", Content: "报告 A"}, // call-b 的结果丢了
	}
	body := marshalOpenAI(t, msgs)
	if len(body.Messages) != 3 {
		t.Fatalf("缺的结果应被补上（assistant + 2 结果）: %+v", body.Messages)
	}
	last := body.Messages[2]
	if last.Role != "tool" || last.ToolCallID != "call-b" {
		t.Fatalf("补的应是 call-b 的合成结果: %+v", last)
	}
	var text string
	if err := json.Unmarshal(last.Content, &text); err != nil {
		t.Fatalf("合成结果应是 string: %s", last.Content)
	}
	if !strings.Contains(text, "残缺") {
		t.Fatalf("合成结果应自解释: %q", text)
	}
}

// TestOpenAIBackfillDoesNotInventOnLooseOrder：assistant 之后一条结果都没有
//（历史顺序乱了）不凭空插消息——对称于 anthropic 侧的同款纪律：凭空插一条
// 消息改变语义比让端点报错更糟，不猜。
func TestOpenAIBackfillDoesNotInventOnLooseOrder(t *testing.T) {
	var a, b ToolCall
	a.ID, a.Type, a.Function.Name, a.Function.Arguments = "call-a", "function", "bash", `{}`
	b.ID, b.Type, b.Function.Name, b.Function.Arguments = "call-b", "function", "bash", `{}`
	msgs := []Message{
		{Role: "assistant", Content: "并行两个", ToolCalls: []ToolCall{a, b}},
		{Role: "user", Content: "接着聊别的"}, // 下一条不是工具结果
	}
	body := marshalOpenAI(t, msgs)
	if len(body.Messages) != 2 {
		t.Fatalf("不该插入新消息: %+v", body.Messages)
	}
	if body.Messages[1].Role != "user" {
		t.Fatalf("不应往用户消息前面塞工具结果: %+v", body.Messages[1])
	}
}

// TestAnthropicEmptyUserTextBlockSkipped：⑤ 空 user 正文（无图）不发空 text
// 块（真 Anthropic 对空 text 块 400：text content blocks must contain
// non-whitespace text）——整条跳过，与 openai 路径同款策略。
func TestAnthropicEmptyUserTextBlockSkipped(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "答"},
		{Role: "user", Content: ""}, // 撤销/占位路径可能产生的空 user 消息
	}
	req, err := convertToAnthropic("m", msgs, requestOpts{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("空 user 消息应被跳过: %+v", req.Messages)
	}
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type == "text" && b.Text == "" {
				t.Fatalf("不该有空 text 块: %+v", m.Content)
			}
		}
	}
}

// TestAnthropicEmptyUserWithImagesOnlyImageBlocks：空正文的带图 user 消息只发
// image 块（text 块省略）——既不 400 也不丢图。
func TestAnthropicEmptyUserWithImagesOnlyImageBlocks(t *testing.T) {
	bindTestLoader(t)
	msgs := []Message{{Role: "user", Content: "",
		Images: []ImageRef{{Path: "s1/a.png", Mime: "image/png"}}}}
	req, err := convertToAnthropic("m", msgs, requestOpts{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("带图消息不该被跳过: %+v", req.Messages)
	}
	blocks := req.Messages[0].Content
	if len(blocks) != 1 || blocks[0].Type != "image" {
		t.Fatalf("空正文带图应只发 image 块: %+v", blocks)
	}
}

// TestOpenAIEmptyUserWithImagesOnlyImagePart：openai 路径对称形态（空正文带图
// 消息不丢图；text 分片为空串是历史形态，端点接受）。
func TestOpenAIEmptyUserWithImagesOnlyImagePart(t *testing.T) {
	bindTestLoader(t)
	msgs := []Message{{Role: "user", Content: "",
		Images: []ImageRef{{Path: "s1/a.png", Mime: "image/png"}}}}
	b, err := json.Marshal(buildOpenAIRequest("m", msgs, requestOpts{}, false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"image_url"`) {
		t.Fatalf("空正文带图不该丢图: %s", b)
	}
	if strings.Contains(string(b), `"text":"看这张图"`) {
		t.Fatalf("不该出现不存在的正文: %s", b)
	}
}

// TestAnthropicPureEmptyAssistantSkipped：anthropic 路径对称清洗——纯空
// assistant 不进 wire（assistantToBlocks 的兜底原本会给它发空 text 块，同样
// 会被真 Anthropic 400 拒收）。
func TestAnthropicPureEmptyAssistantSkipped(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: ""},
	}
	req, err := convertToAnthropic("m", msgs, requestOpts{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Fatalf("纯空 assistant 应被跳过: %+v", req.Messages)
	}
}

// TestOpenAISanitisedWireNeverHasEmptyAssistantContent：总闸测试——含空消息、
// 空工具结果、工具轮的混合历史经清洗后，wire 上不得出现空串 assistant content
//（LiteLLM 清洗的触发形态），也不再出现占位串（占位串一旦进了历史，清洗后的
// 请求里它只是普通文本，出现在 tool 结果或消息正文里不在本测试范围）。
func TestOpenAISanitisedWireNeverHasEmptyAssistantContent(t *testing.T) {
	tc := ToolCall{ID: "c1", Type: "function"}
	tc.Function.Name = "bash"
	tc.Function.Arguments = "{}"
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: ""}, // 纯空
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{tc}}, // 工具轮
		{Role: "tool", ToolCallID: "c1", Content: ""},               // 空结果
		{Role: "assistant", Content: "完成"},
	}
	b, err := json.Marshal(buildOpenAIRequest("m", msgs, requestOpts{}, false))
	if err != nil {
		t.Fatal(err)
	}
	var body reqBody
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	for i, m := range body.Messages {
		if m.Role == "assistant" && string(m.Content) == `""` {
			t.Fatalf("第 %d 条 assistant content 是空串（清洗漏了）: %s", i, b)
		}
	}
	if len(body.Messages) != 4 {
		t.Fatalf("纯空 assistant 应被跳过: %+v", body.Messages)
	}
}
