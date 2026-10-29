package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exa 的**免配置 MCP 通道**测试（没有 API key 时的路径）。
//
// 夹具按真机实测的响应形态构造（temp/probe-exa-mcp.mjs 的产物）：
//   - basic 工具（web_search_exa）返回**格式化文本块**（Title/URL/Published/Author/Highlights）；
//   - advanced 工具（web_search_advanced_exa）返回**原始搜索 JSON** 字符串；
//   - HTTP 层是 SSE（`event: message` + `data: {...}` 行）。
//
// 端点走 mcp_url 设置项（有默认值），所以测试能把请求指向 httptest——
// 这也是把它做成声明式设置项而不是包级常量的原因之一。

// exaMCPAdvancedJSON 是 advanced 工具返回的原始搜索 JSON（实测形态）。
const exaMCPAdvancedJSON = `{"requestId":"abc123","resolvedSearchType":"","results":[` +
	`{"id":"https://go.dev/doc/tutorial/generics","url":"https://go.dev/doc/tutorial/generics","text":"Tutorial: Getting started with generics\n\nThis tutorial introduces the basics of generics in Go."},` +
	`{"id":"https://go.dev/blog/go1.26","url":"https://go.dev/blog/go1.26","title":"Go 1.26 is released","text":"Go 1.26 is released"}` +
	`]}`

// exaMCPBasicText 是 basic 工具返回的格式化文本块（实测形态）。
const exaMCPBasicText = `Title: Go 1.26 Release Notes
URL: https://go.dev/doc/go1.26
Published: N/A
Author: N/A
Highlights:
Go 1.26 Release Notes - The Go Programming Language

---

Title: Go 1.26 is released
URL: https://go.dev/blog/go1.26
Published: 2026-02-10T00:00:00.000Z
Author: The Go team
Highlights:
Today the Go team is pleased to release Go 1.26.
`

// exaMCPEnvelope 包一层 JSON-RPC 响应（SSE 与纯 JSON 两种传输共用）。
func exaMCPEnvelope(t *testing.T, result any, rpcErr any) string {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": 1}
	if rpcErr != nil {
		body["error"] = rpcErr
	} else {
		body["result"] = result
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("构造响应失败: %v", err)
	}
	return string(buf)
}

// exaMCPTextResult 是 tools/call 成功时的 result（content 数组里一段文本）。
func exaMCPTextResult(text string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
}

// exaMCPServer 起一个假 MCP 端点：记录请求，按 kind 决定响应形态。
//
// kind: "sse"（真机形态）| "json"（纯 JSON）| 其它 = 按 handle 自定义。
func exaMCPServer(t *testing.T, sse bool, text string) (*httptest.Server, *captured) {
	t.Helper()
	cap := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.Method = r.Method
		cap.Path = r.URL.Path
		cap.Query = r.URL.Query()
		cap.Header = r.Header.Clone()
		cap.Body, _ = io.ReadAll(r.Body)
		payload := exaMCPEnvelope(t, exaMCPTextResult(text), nil)
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("event: message\ndata: " + payload + "\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

// exaMCPConfig 构造免配置路径的渠道配置（端点指向假服务器，且**没有 key**）。
func exaMCPConfig(url string) ChannelConfig {
	return ChannelConfig{Options: map[string]string{exaMCPURLOption.Key: url}}
}

// exaMCPSearch 以「未配置 key + 指定免配置端点」直接调用适配器。
//
// **刻意不走 searchVia/effectiveChannel**：那会解析 EXA_API_KEY 环境变量，
// 开发机上真设了这个变量时用例会悄悄走直连路径（环境相关 flaky）。
// 「没有 key」是这些用例的前提，必须由测试自己钉死，而不是指望环境干净。
func exaMCPSearch(t *testing.T, url, query string, opts Options) (Response, error) {
	t.Helper()
	return newExa().Search(context.Background(), exaMCPConfig(url), query, opts)
}

// ---------- 元数据：key 可选但接受 key ----------

func TestExaKeyOptionalButAccepted(t *testing.T) {
	p := newExa()
	if p.NeedsKey() {
		t.Error("Exa 缺 key 也能用（免配置通道），NeedsKey 应为 false")
	}
	if !p.AcceptsKey() {
		t.Error("Exa 接受 key（有 key 走直连）——AcceptsKey 必须为 true，否则面板会藏掉 key 输入框")
	}
	if !p.DefaultReady() {
		t.Error("Exa 是刻意默认就绪的零配置渠道（用户拍板开箱即用）")
	}
	if p.OptIn() {
		t.Error("Exa 不该是 optIn——它与 defaultReady 语义矛盾")
	}
	// 没有任何配置也应就绪（这正是「开箱即用」的含义）
	if !p.Configured(ChannelConfig{}) {
		t.Error("Exa 无任何配置时应就绪（否则默认渠道的承诺不成立）")
	}
	// 停用后不就绪（用户仍可关掉它）
	if p.Configured(ChannelConfig{Disabled: true}) {
		t.Error("停用的渠道不该就绪")
	}
}

// ---------- 免配置通道：basic 工具 ----------

func TestExaMCPBasicCall(t *testing.T) {
	srv, cap := exaMCPServer(t, true, exaMCPBasicText)
	resp, err := exaMCPSearch(t, srv.URL, "Go 1.26", Options{NumResults: 3})
	if err != nil {
		t.Fatalf("免配置搜索失败: %v", err)
	}

	// 请求形态：POST 到端点，tools 走查询参数（该端点按它暴露工具）
	if cap.Method != http.MethodPost {
		t.Errorf("方法 = %s，期望 POST", cap.Method)
	}
	if got := cap.Query.Get("tools"); got != exaMCPBasicTool {
		t.Errorf("tools = %q，期望 %q（无过滤条件时用 basic 工具）", got, exaMCPBasicTool)
	}
	if got := cap.Header.Get("Accept"); !strings.Contains(got, "text/event-stream") {
		t.Errorf("Accept = %q，应同时接受 text/event-stream", got)
	}
	body := decodeBody(t, cap.Body)
	if body["method"] != "tools/call" {
		t.Errorf("method = %v，期望 tools/call", body["method"])
	}
	params, _ := body["params"].(map[string]any)
	if params["name"] != exaMCPBasicTool {
		t.Errorf("params.name = %v，期望 %q", params["name"], exaMCPBasicTool)
	}
	args, _ := params["arguments"].(map[string]any)
	if args["query"] != "Go 1.26" {
		t.Errorf("args.query = %v（无过滤条件时不该改写查询）", args["query"])
	}

	// 结果映射：两条文本块
	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2（%+v）", len(resp.Results), resp.Results)
	}
	if resp.Results[0].URL != "https://go.dev/doc/go1.26" {
		t.Errorf("首条 URL = %q", resp.Results[0].URL)
	}
	if resp.Results[0].Title != "Go 1.26 Release Notes" {
		t.Errorf("首条标题 = %q", resp.Results[0].Title)
	}
	// 与直连路径同款约定：snippet 留空、内容进 answer
	if resp.Results[0].Snippet != "" {
		t.Errorf("snippet 应留空（Exa 无独立摘要字段）: %q", resp.Results[0].Snippet)
	}
	if !strings.Contains(resp.Answer, "Go 1.26 Release Notes - The Go Programming Language") {
		t.Errorf("answer 应含 Highlights 正文，实际: %q", resp.Answer)
	}
}

// ---------- 免配置通道：过滤条件走 advanced 工具 ----------

func TestExaMCPUsesAdvancedToolWithFilters(t *testing.T) {
	srv, cap := exaMCPServer(t, true, exaMCPAdvancedJSON)
	resp, err := exaMCPSearch(t, srv.URL, "generics", Options{
		NumResults:    2,
		RecencyFilter: "week",
		DomainFilter:  []string{"go.dev", "-ads.example.com"},
	})
	if err != nil {
		t.Fatalf("免配置搜索失败: %v", err)
	}

	if got := cap.Query.Get("tools"); got != exaMCPAdvancedTool {
		t.Errorf("tools = %q，期望 %q（有过滤条件时用 advanced）", got, exaMCPAdvancedTool)
	}
	body := decodeBody(t, cap.Body)
	params, _ := body["params"].(map[string]any)
	args, _ := params["arguments"].(map[string]any)

	// 过滤条件必须是**真参数**（basic 工具只认 query/numResults，
	// 过滤只能拼进查询文本——效果差一档，所以有过滤时优先 advanced）
	inc, _ := args["includeDomains"].([]any)
	if len(inc) != 1 || inc[0] != "go.dev" {
		t.Errorf("includeDomains = %v，期望 [go.dev]", args["includeDomains"])
	}
	exc, _ := args["excludeDomains"].([]any)
	if len(exc) != 1 || exc[0] != "ads.example.com" {
		t.Errorf("excludeDomains = %v，期望 [ads.example.com]", args["excludeDomains"])
	}
	if _, ok := args["startPublishedDate"]; !ok {
		t.Error("recency=week 应转成 startPublishedDate 真参数")
	}
	if args["query"] != "generics" {
		t.Errorf("advanced 路径的 query 不该被改写: %v", args["query"])
	}

	// advanced 返回原始搜索 JSON → 解析出结果与正文
	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2", len(resp.Results))
	}
	if resp.Results[1].Title != "Go 1.26 is released" {
		t.Errorf("第二条应取 JSON 里的 title: %q", resp.Results[1].Title)
	}
	if !strings.Contains(resp.Answer, "This tutorial introduces the basics of generics") {
		t.Errorf("answer 应含正文，实际: %q", resp.Answer)
	}
}

// advanced 不可用时退到 basic，并把过滤条件拼进查询文本。
func TestExaMCPFallsBackToBasicWhenAdvancedUnavailable(t *testing.T) {
	var calls []string
	var basicQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tool := r.URL.Query().Get("tools")
		calls = append(calls, tool)
		w.Header().Set("Content-Type", "text/event-stream")
		if tool == exaMCPAdvancedTool {
			// 部署没暴露 advanced：JSON-RPC error
			_, _ = w.Write([]byte("event: message\ndata: " +
				exaMCPEnvelope(t, nil, map[string]any{"code": -32601, "message": "Method not found"}) + "\n\n"))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		basicQuery, _ = req.Params.Arguments["query"].(string)
		_, _ = w.Write([]byte("event: message\ndata: " +
			exaMCPEnvelope(t, exaMCPTextResult(exaMCPBasicText), nil) + "\n\n"))
	}))
	t.Cleanup(srv.Close)

	resp, err := exaMCPSearch(t, srv.URL, "generics", Options{DomainFilter: []string{"go.dev"}})
	if err != nil {
		t.Fatalf("advanced 不可用时应退到 basic，实际报错: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("应调两次（advanced 失败 → basic），实际 %v", calls)
	}
	if calls[1] != exaMCPBasicTool {
		t.Errorf("第二次应调 basic，实际 %q", calls[1])
	}
	// basic 只认 query/numResults → 过滤条件必须拼进查询文本
	if !strings.Contains(basicQuery, "site:go.dev") {
		t.Errorf("回退到 basic 时域名过滤应拼进查询文本，实际: %q", basicQuery)
	}
	if len(resp.Results) != 2 {
		t.Errorf("回退后仍应拿到结果，实际 %d 条", len(resp.Results))
	}
}

// 调用方取消（KindAborted）不该触发 basic 回退——再发一次请求是白烧配额。
func TestExaMCPAbortDoesNotFallBack(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 调用方已取消
	_, err := newExa().Search(ctx, exaMCPConfig(srv.URL), "q", Options{DomainFilter: []string{"go.dev"}})
	if err == nil {
		t.Fatal("期望报错")
	}
	if KindOf(err) != KindAborted {
		t.Errorf("分类 = %q，期望 %q（调用方取消）", KindOf(err), KindAborted)
	}
	if calls != 0 {
		t.Errorf("取消后不该发出请求，实际发了 %d 次", calls)
	}
}

// ---------- 响应形态与错误 ----------

func TestExaMCPAcceptsPlainJSONResponse(t *testing.T) {
	srv, _ := exaMCPServer(t, false, exaMCPBasicText)
	resp, err := exaMCPSearch(t, srv.URL, "q", Options{})
	if err != nil {
		t.Fatalf("纯 JSON 响应也该认: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2", len(resp.Results))
	}
}

func TestExaMCPRateLimitMentionsKey(t *testing.T) {
	srv, _ := captureServer(t, http.StatusTooManyRequests, `{"error":"rate limited"}`)
	_, err := exaMCPSearch(t, srv.URL, "q", Options{})
	pe := assertProviderError(t, err, KindQuota, http.StatusTooManyRequests)
	// 429 是「免费额度用尽」——必须告诉用户加 key 能解除，否则只会看到一句额度不足
	if !strings.Contains(pe.Message, "API Key") {
		t.Errorf("429 的说明应提到填 API Key 可解除限制，实际: %q", pe.Message)
	}
}

func TestExaMCPUnparseableResponseIsInvalidResponse(t *testing.T) {
	srv, _ := captureServer(t, 200, `<html>not json at all</html>`)
	_, err := exaMCPSearch(t, srv.URL, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 0)
}

func TestExaMCPRPCErrorSurfaces(t *testing.T) {
	srv, _ := captureServer(t, 200,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"no such tool"}}`)
	_, err := exaMCPSearch(t, srv.URL, "q", Options{})
	pe := assertProviderError(t, err, KindInvalidResponse, 0)
	if !strings.Contains(pe.Message, "no such tool") {
		t.Errorf("应回显 RPC 错误消息，实际: %q", pe.Message)
	}
}

// 空结果是**成功**（不是降级理由）——不该报错。
func TestExaMCPEmptyResultIsSuccess(t *testing.T) {
	srv, _ := captureServer(t, 200,
		`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":""}]}}`)
	resp, err := exaMCPSearch(t, srv.URL, "q", Options{})
	if err != nil {
		t.Fatalf("空结果不该报错: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("结果数 = %d，期望 0", len(resp.Results))
	}
}

// 与上一条相对：**一个 text 项都没有**是形态错误（不是「搜到 0 条」）。
// 两种情况必须分开——否则要么把形态错误当成空结果（用户看到「没搜到」），
// 要么把空结果当成错误（降级链白试下一个渠道）。
func TestExaMCPMissingTextContentIsInvalidResponse(t *testing.T) {
	srv, _ := captureServer(t, 200,
		`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"image","data":"xx"}]}}`)
	_, err := exaMCPSearch(t, srv.URL, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 0)
}

// ---------- 有 key：直连路径不受影响 ----------

func TestExaWithKeyUsesDirectAPI(t *testing.T) {
	srv, cap := captureServer(t, 200,
		`{"results":[{"title":"T","url":"https://x.com/1","highlights":["摘要"]}]}`)
	p := newExa()
	resp, err := p.Search(context.Background(),
		ChannelConfig{APIKey: "exa-key-1234567890", BaseURL: srv.URL}, "q", Options{})
	if err != nil {
		t.Fatalf("直连路径失败: %v", err)
	}
	if cap.Path != "/search" {
		t.Errorf("有 key 时应走直连 API 的 /search，实际路径 %q", cap.Path)
	}
	if got := cap.Header.Get("x-api-key"); got != "exa-key-1234567890" {
		t.Errorf("x-api-key = %q", got)
	}
	if len(resp.Results) != 1 || resp.Results[0].Title != "T" {
		t.Errorf("直连结果映射异常: %+v", resp.Results)
	}
}

// 免配置通道端点由设置项决定（默认官方端点）。
func TestExaMCPURLOptionDefault(t *testing.T) {
	// 钉住环境变量回退，否则开发机上真设了 EXA_MCP_URL 会掩盖默认值。
	t.Setenv(exaMCPURLOption.EnvVar, "")
	got := effectiveChannel(newExa(), ChannelConfig{})
	if got.Options[exaMCPURLOption.Key] != exaMCPDefaultURL {
		t.Errorf("默认端点 = %q，期望 %q", got.Options[exaMCPURLOption.Key], exaMCPDefaultURL)
	}
}
