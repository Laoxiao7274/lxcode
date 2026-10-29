package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/tools"
	"github.com/moyunteng/lxcode/internal/websearch"
)

// newSearchStack 起一个装配了搜索服务的完整 WS 服务端（含真实客户端）。
// 复用 server_test.go 的 dialTest/wsTestClient——广播只有经过真连接
// 才能验证（直接调方法测不出「客户端收不收得到」）。
func newSearchStack(t *testing.T) (*Server, *wsTestClient, *websearch.Service) {
	t.Helper()
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatalf("加载注册表失败: %v", err)
	}
	svc, err := websearch.LoadService(filepath.Join(t.TempDir(), "search.json"))
	if err != nil {
		t.Fatalf("加载搜索服务失败: %v", err)
	}
	srv := NewServer(reg)
	srv.AttachSearch(svc)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	client := dialTest(t, "ws"+strings.TrimPrefix(ts.URL, "http")+protocol.Path)
	t.Cleanup(func() { client.conn.Close() })
	return srv, client, svc
}

// 渠道列表必须列出**全部内置渠道**（含未配置的）——只回已配置的话
// 用户永远看不到「还能配什么」。
//
// 就绪判定的契约（2026-09-27 起）：未配置的渠道一律不就绪，**除非**它是
// 刻意默认就绪的零配置渠道（`default_ready`，目前只有 Exa 的免配置通道）
// ——「开箱即用的默认渠道」与「某个渠道意外静默就绪」必须分得开，前者是
// 用户拍板的产品决策，后者是 bug。
func TestSearchChannelsListIncludesUnconfigured(t *testing.T) {
	_, client, _ := newSearchStack(t)
	resp := client.call(protocol.MethodSearchChannelsList, map[string]any{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("渠道列表失败: %+v", resp)
	}
	res := decode[protocol.SearchChannelsResult](t, resp.Result)
	if len(res.Channels) < 6 {
		t.Fatalf("应列出全部内置渠道，实际 %d 个", len(res.Channels))
	}
	// 至少要有一个刻意默认就绪的渠道——否则「不配置也能搜」的承诺不成立。
	defaultReady := 0
	for _, ch := range res.Channels {
		if ch.ID == "" || ch.Label == "" || ch.Desc == "" {
			t.Errorf("渠道元数据不完整: %+v", ch)
		}
		if ch.DefaultReady {
			defaultReady++
			if !ch.Configured {
				t.Errorf("刻意默认就绪的渠道 %s 未配置时也该就绪", ch.ID)
			}
			continue
		}
		if ch.Configured {
			t.Errorf("未配置的渠道不该报告就绪: %s", ch.ID)
		}
	}
	if defaultReady == 0 {
		t.Error("内置渠道里应至少有一个 default_ready（开箱可用的默认渠道）")
	}
	// 有默认就绪渠道 ⇒ 整栈就绪（工具据此不再提示「未配置」）。
	if !res.Ready {
		t.Error("存在默认就绪渠道时 Ready 应为 true")
	}
}

// 保存渠道：落盘 + 广播 search.changed + 列表反映。
func TestSearchChannelSaveAndBroadcast(t *testing.T) {
	_, client, svc := newSearchStack(t)
	resp := client.call(protocol.MethodSearchChannelSave, protocol.SearchChannelSaveParams{
		ID: "tavily", APIKey: "tvly-test-key", Enabled: true,
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("保存渠道失败: %+v", resp)
	}
	// 广播必须真的到达客户端（这是「前端能自动刷新」的契约）。
	if ev := client.waitEvent(protocol.EventSearchChanged); ev == nil {
		t.Fatal("保存后应广播 search.changed")
	}

	list := decode[protocol.SearchChannelsResult](t, client.call(protocol.MethodSearchChannelsList, map[string]any{}).Result)
	if !list.Ready {
		t.Error("配置后 Ready 应为 true")
	}
	if list.Primary != "tavily" {
		t.Errorf("首次配置应自动设为主渠道，实际 %q", list.Primary)
	}
	var found bool
	for _, ch := range list.Channels {
		if ch.ID == "tavily" {
			found = true
			if !ch.Configured || !ch.Primary {
				t.Errorf("tavily 应报告已就绪且为主渠道: %+v", ch)
			}
		}
	}
	if !found {
		t.Fatal("列表应含 tavily")
	}
	// 落盘了才算保存成功（重启后仍在）。
	reloaded, err := websearch.LoadConfig(svc.Path())
	if err != nil {
		t.Fatalf("重读配置失败: %v", err)
	}
	if reloaded.Channels["tavily"].APIKey != "tvly-test-key" {
		t.Errorf("配置未落盘: %+v", reloaded.Channels)
	}
}

// 未知渠道 id 报参数错误（不是内部错误）。
func TestSearchChannelSaveUnknownID(t *testing.T) {
	_, client, _ := newSearchStack(t)
	resp := client.call(protocol.MethodSearchChannelSave, protocol.SearchChannelSaveParams{
		ID: "不存在", APIKey: "k", Enabled: true,
	})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("未知渠道应报参数错误，实际 %+v", resp)
	}
}

// 删除渠道后主渠道回落。
func TestSearchChannelRemove(t *testing.T) {
	_, client, _ := newSearchStack(t)
	client.call(protocol.MethodSearchChannelSave, protocol.SearchChannelSaveParams{
		ID: "tavily", APIKey: "k", Enabled: true,
	})
	client.call(protocol.MethodSearchChannelSave, protocol.SearchChannelSaveParams{
		ID: "searxng", BaseURL: "https://searx.example.com", Enabled: true,
	})
	resp := client.call(protocol.MethodSearchChannelRemove, protocol.SearchChannelRefParams{ID: "tavily"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("删除渠道失败: %+v", resp)
	}
	list := decode[protocol.SearchChannelsResult](t, client.call(protocol.MethodSearchChannelsList, map[string]any{}).Result)
	if list.Primary != "searxng" {
		t.Errorf("主渠道应回落到 searxng，实际 %q", list.Primary)
	}
}

// 设主渠道。
func TestSearchPrimarySet(t *testing.T) {
	_, client, _ := newSearchStack(t)
	client.call(protocol.MethodSearchChannelSave, protocol.SearchChannelSaveParams{
		ID: "tavily", APIKey: "k", Enabled: true,
	})
	resp := client.call(protocol.MethodSearchPrimarySet, protocol.SearchPrimarySetParams{ID: "tavily"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("设主渠道失败: %+v", resp)
	}
	got := decode[protocol.SearchChannelsResult](t,
		client.call(protocol.MethodSearchChannelsList, map[string]any{}).Result).Primary
	if got != "tavily" {
		t.Errorf("主渠道 = %q，期望 tavily", got)
	}
	if resp := client.call(protocol.MethodSearchPrimarySet, protocol.SearchPrimarySetParams{ID: "不存在"}); resp.Error == nil {
		t.Error("未知渠道应报错")
	}
}

// 未装配搜索服务时，方法必须报「未装配」而不是 panic。
func TestSearchMethodsWithoutService(t *testing.T) {
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatalf("加载注册表失败: %v", err)
	}
	s := NewServer(reg)
	for _, m := range []string{
		protocol.MethodSearchChannelSave,
		protocol.MethodSearchChannelRemove,
		protocol.MethodSearchPrimarySet,
		protocol.MethodSearchTest,
	} {
		resp := dispatchOne(s, m, map[string]any{"id": "tavily"})
		if resp.Error == nil {
			t.Errorf("%s 未装配时应报错", m)
			continue
		}
		if resp.Error.Code != protocol.CodeInternal {
			t.Errorf("%s 未装配应报内部错误，实际 %d", m, resp.Error.Code)
		}
	}
	// 列表在未装配时返回空载荷（不是错误——前端照常渲染空列表）。
	resp := dispatchOne(s, protocol.MethodSearchChannelsList, nil)
	if resp.Error != nil {
		t.Fatalf("未装配时列表不该报错: %+v", resp.Error)
	}
	if len(decode[protocol.SearchChannelsResult](t, resp.Result).Channels) != 0 {
		t.Error("未装配时渠道列表应为空")
	}
}

// 装配搜索服务后，工具注册表必须能真的搜（否则模型调用 web_search 会拿到「未装配」）。
func TestAttachSearchWiresTool(t *testing.T) {
	s, _, _ := newSearchStack(t)
	d, ok := s.treg.Get("web_search")
	if !ok {
		t.Fatal("装配后注册表应有 web_search")
	}
	// 搜索是只读操作：低危 + 非变更类（strict 只读模式必须放行）。
	if d.Risk != tools.RiskLow {
		t.Errorf("web_search 应为低危，实际 %v", d.Risk)
	}
	if s.treg.IsMutating(d.Name) {
		t.Errorf("%s 不该是变更类工具", d.Name)
	}
}

// search.test：未配置渠道报错、未知渠道报参数错误。
func TestSearchTestMethod(t *testing.T) {
	_, client, _ := newSearchStack(t)
	if resp := client.call(protocol.MethodSearchTest, protocol.SearchTestParams{ID: "tavily"}); resp.Error == nil {
		t.Fatal("未配置渠道的测试应报错")
	}
	resp := client.call(protocol.MethodSearchTest, protocol.SearchTestParams{ID: "不存在"})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("未知渠道应报参数错误，实际 %+v", resp)
	}
}

// 端到端验收：配一个指向本地假渠道的 searxng → 经协议 search.test 打通
// 全链（配置 → 服务 → 适配器 → HTTP → 结果映射 → 协议载荷）。
//
// 用 searxng 而不是付费渠道：它只需实例地址，最贴近「自建」这条真实路径，
// 且能直接指向 httptest（不打真网络、不花钱）。
func TestSearchTestEndToEnd(t *testing.T) {
	srv, client, _ := newSearchStack(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("searxng 请求必须带 format=json，实际 %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"title":"Go 泛型教程","url":"https://go.dev/blog/generics","content":"泛型入门"},
			{"title":"另一篇","url":"https://example.com/x","content":"内容"}
		],"answers":["引擎直出答案"]}`))
	}))
	t.Cleanup(fake.Close)

	if resp := client.call(protocol.MethodSearchChannelSave, protocol.SearchChannelSaveParams{
		ID: "searxng", BaseURL: fake.URL, Enabled: true,
	}); resp == nil || resp.Error != nil {
		t.Fatalf("配置 searxng 失败: %+v", resp)
	}

	resp := client.call(protocol.MethodSearchTest, protocol.SearchTestParams{
		ID: "searxng", Query: "go 泛型",
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("测试渠道失败: %+v", resp)
	}
	res := decode[protocol.SearchTestResult](t, resp.Result)
	if res.Provider != "searxng" {
		t.Errorf("作答渠道 = %q", res.Provider)
	}
	if len(res.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2: %+v", len(res.Results), res.Results)
	}
	if res.Results[0].Title != "Go 泛型教程" {
		t.Errorf("标题映射不对: %+v", res.Results[0])
	}
	if res.Results[0].URL != "https://go.dev/blog/generics" {
		t.Errorf("URL 映射不对: %+v", res.Results[0])
	}
	if !strings.Contains(res.Answer, "引擎直出答案") {
		t.Errorf("answer 应含引擎直出内容: %q", res.Answer)
	}
	// 默认查询词兜底：用户点测试通常不想先想关键词。
	if res.ElapsedMS < 0 {
		t.Errorf("耗时不该为负: %d", res.ElapsedMS)
	}

	// 默认查询词路径（不传 query）也要能打通。
	resp = client.call(protocol.MethodSearchTest, protocol.SearchTestParams{ID: "searxng"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("不传查询词的测试应能跑: %+v", resp)
	}
	_ = srv
}

// 装配搜索服务后，模型真的能通过 web_search 工具拿到结果（工具→服务全链）。
func TestWebSearchToolEndToEnd(t *testing.T) {
	srv, client, _ := newSearchStack(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"T","url":"https://a.com","content":"片段"}]}`))
	}))
	t.Cleanup(fake.Close)
	client.call(protocol.MethodSearchChannelSave, protocol.SearchChannelSaveParams{
		ID: "searxng", BaseURL: fake.URL, Enabled: true,
	})

	out := srv.treg.Execute(t.Context(), toolCallFor("web_search", `{"query":"测试","num_results":3}`))
	if !strings.Contains(out, "searxng") {
		t.Errorf("工具结果应标明作答渠道: %s", out)
	}
	if !strings.Contains(out, "https://a.com") {
		t.Errorf("工具结果应含检索到的链接: %s", out)
	}
}

// 渠道私有设置项（options）的端到端链路：面板保存 → 载荷回带 → 落盘 → 就绪判定。
//
// 这条钉住的是「设置面板真的配得动一个带私有设置的渠道」——Bright Data 的 zone
// 在此前只能靠环境变量，面板配不出一个能用的渠道（配了也是「未就绪」）。
func TestSearchChannelOptionsEndToEnd(t *testing.T) {
	_, client, svc := newSearchStack(t)
	// 清掉环境变量回退，否则开发机上真设了的值会掩盖落盘与回带。
	t.Setenv("BRIGHTDATA_SERP_ZONE", "")

	// 只填 key 不填 zone：渠道必须仍是「未就绪」（半配好的渠道不该进降级链）。
	client.call(protocol.MethodSearchChannelSave, protocol.SearchChannelSaveParams{
		ID: "brightdata", APIKey: "bd-test-key-1234567890", Enabled: true,
	})
	list := decode[protocol.SearchChannelsResult](t, client.call(protocol.MethodSearchChannelsList, map[string]any{}).Result)
	if ch := findChannel(t, list, "brightdata"); ch.Configured {
		t.Error("缺必填设置项 zone 时不该报告就绪")
	}

	// 补上 zone：渠道就绪，且取值必须回带到前端（面板要能回显）。
	resp := client.call(protocol.MethodSearchChannelSave, protocol.SearchChannelSaveParams{
		ID: "brightdata", APIKey: "bd-test-key-1234567890", Enabled: true,
		Options: map[string]string{"zone": "my_serp_zone"},
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("保存带设置项的渠道失败: %+v", resp)
	}
	list = decode[protocol.SearchChannelsResult](t, client.call(protocol.MethodSearchChannelsList, map[string]any{}).Result)
	ch := findChannel(t, list, "brightdata")
	if !ch.Configured {
		t.Errorf("key + zone 齐备时应报告就绪: %+v", ch)
	}
	if ch.Options["zone"] != "my_serp_zone" {
		t.Errorf("设置项没回带: %v", ch.Options)
	}
	// 声明必须一起给前端——面板靠它渲染输入项（含必填标记与说明）。
	if len(ch.OptionSpecs) != 1 || ch.OptionSpecs[0].Key != "zone" {
		t.Fatalf("设置项声明没给前端: %+v", ch.OptionSpecs)
	}
	if !ch.OptionSpecs[0].Required || ch.OptionSpecs[0].Label == "" || ch.OptionSpecs[0].EnvVar == "" {
		t.Errorf("设置项声明不完整（面板渲染不出必填提示与回退说明）: %+v", ch.OptionSpecs[0])
	}

	// 落盘了才算保存成功（重启后仍在）。
	reloaded, err := websearch.LoadConfig(svc.Path())
	if err != nil {
		t.Fatalf("重读配置失败: %v", err)
	}
	if got := reloaded.Channels["brightdata"].Options["zone"]; got != "my_serp_zone" {
		t.Errorf("设置项未落盘: %q（%+v）", got, reloaded.Channels["brightdata"])
	}

	// 清空设置项：回到未就绪，且磁盘上不留空值。
	client.call(protocol.MethodSearchChannelSave, protocol.SearchChannelSaveParams{
		ID: "brightdata", APIKey: "bd-test-key-1234567890", Enabled: true,
		Options: map[string]string{"zone": "   "},
	})
	reloaded, err = websearch.LoadConfig(svc.Path())
	if err != nil {
		t.Fatalf("重读配置失败: %v", err)
	}
	if got := reloaded.Channels["brightdata"].Options; len(got) != 0 {
		t.Errorf("空白设置项应被丢弃，实际 %v", got)
	}
}

// findChannel 在快照里按 id 取渠道（找不到即失败）。
func findChannel(t *testing.T, list protocol.SearchChannelsResult, id string) websearch.Channel {
	t.Helper()
	for _, ch := range list.Channels {
		if ch.ID == id {
			return ch
		}
	}
	t.Fatalf("快照里没有渠道 %s", id)
	return websearch.Channel{}
}

// decode 把 any 载荷转成具体类型（走 JSON 往返，与真实客户端同路径）。
func decode[T any](t *testing.T, v any) T {
	t.Helper()
	var out T
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化载荷失败: %v", err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("反序列化载荷失败: %v", err)
	}
	return out
}

// dispatchOne 直接走分发（无客户端——测未装配路径）。
func dispatchOne(s *Server, method string, params any) *protocol.Response {
	raw, _ := json.Marshal(params)
	if params == nil {
		raw = nil
	}
	return s.dispatch(&wsClient{}, &protocol.Request{ID: json.RawMessage(`1`), Method: method, Params: raw})
}

// toolCallFor 构造一次工具调用（走注册表执行）。
func toolCallFor(name, args string) llm.ToolCall {
	tc := llm.ToolCall{ID: "call-1"}
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}
