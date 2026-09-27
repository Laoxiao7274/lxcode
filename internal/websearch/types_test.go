package websearch

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeNumResults(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, DefaultNumResults},
		{-3, DefaultNumResults},
		{1, 1},
		{20, 20},
		{21, MaxNumResults},
		{999, MaxNumResults},
	}
	for _, c := range cases {
		if got := NormalizeNumResults(c.in); got != c.want {
			t.Errorf("NormalizeNumResults(%d) = %d，期望 %d", c.in, got, c.want)
		}
	}
}

func TestValidRecency(t *testing.T) {
	for _, ok := range []string{"", "day", "week", "month", "year"} {
		if !ValidRecency(ok) {
			t.Errorf("ValidRecency(%q) 应为 true", ok)
		}
	}
	for _, bad := range []string{"hour", "DAY", "yesterday"} {
		if ValidRecency(bad) {
			t.Errorf("ValidRecency(%q) 应为 false", bad)
		}
	}
}

func TestDomainFilterParts(t *testing.T) {
	include, exclude := DomainFilterParts([]string{"Example.com", "-ads.example.com", "www.foo.org", "", "  ", "example.com"})
	wantInc := []string{"example.com", "foo.org"}
	wantExc := []string{"ads.example.com"}
	if !equalStrings(include, wantInc) {
		t.Errorf("include = %v，期望 %v", include, wantInc)
	}
	if !equalStrings(exclude, wantExc) {
		t.Errorf("exclude = %v，期望 %v", exclude, wantExc)
	}
}

// 同域名同时出现在 include 与 exclude 时只认先出现的那个：
// 两处都发会让渠道自己决定谁赢，结果不可预期。
func TestDomainFilterPartsConflictKeepsFirst(t *testing.T) {
	include, exclude := DomainFilterParts([]string{"-a.com", "a.com"})
	if !equalStrings(include, nil) || !equalStrings(exclude, []string{"a.com"}) {
		t.Errorf("先出现的排除应生效: include=%v exclude=%v", include, exclude)
	}
}

func TestClassifyStatus(t *testing.T) {
	cases := []struct {
		status int
		want   ErrorKind
	}{
		{401, KindCredential},
		{403, KindCredential},
		{429, KindQuota},
		{404, KindUnsupported},
		{405, KindUnsupported},
		{501, KindUnsupported},
		{400, KindInvalidRequest},
		{422, KindInvalidRequest},
		{500, KindTransient},
		{503, KindTransient},
		{418, KindInvalidRequest},
	}
	for _, c := range cases {
		if got := ClassifyStatus(c.status); got != c.want {
			t.Errorf("ClassifyStatus(%d) = %q，期望 %q", c.status, got, c.want)
		}
	}
}

// 降级语义的核心纪律：只有「换个渠道可能好」的错误才降级。
// 凭证错/参数错降级会把配置问题掩盖成搜索成功。
func TestShouldFallback(t *testing.T) {
	fallback := []ErrorKind{KindTransient, KindQuota, KindNetwork, KindInvalidResponse, KindUnsupported}
	for _, k := range fallback {
		err := &ProviderError{Provider: "x", Kind: k}
		if !ShouldFallback(err) {
			t.Errorf("分类 %q 应触发降级", k)
		}
	}
	noFallback := []ErrorKind{KindCredential, KindConfig, KindAuth, KindInvalidRequest, KindAborted, KindUnknown}
	for _, k := range noFallback {
		err := &ProviderError{Provider: "x", Kind: k}
		if ShouldFallback(err) {
			t.Errorf("分类 %q 不该触发降级", k)
		}
	}
}

func TestShouldFallbackNeverOnCancel(t *testing.T) {
	if ShouldFallback(nil) {
		t.Error("nil 不该降级")
	}
	// 用户取消：即使被包成可降级分类，也不该继续试下一个渠道。
	wrapped := &ProviderError{Provider: "x", Kind: KindNetwork, Err: context.Canceled}
	if ShouldFallback(wrapped) {
		t.Error("ctx 取消不该降级（用户已经不要这次搜索了）")
	}
	if ShouldFallback(context.DeadlineExceeded) {
		t.Error("裸 DeadlineExceeded 不该降级（调用方自己的 deadline 到点）")
	}
}

// 渠道自身超时必须降级——这是降级链最常生效的场景。
//
// 回归钉子：曾经 ClassifyTransport 拿到的是加过超时的派生 ctx，于是我们的
// 请求超时被判成 KindAborted（当成用户取消），ShouldFallback 又无条件拦
// DeadlineExceeded，两层叠加导致**主渠道一慢整次搜索就失败**。
func TestChannelTimeoutFallsBack(t *testing.T) {
	// 调用方还活着 → 超时是渠道侧问题 → KindNetwork（可降级）。
	live := context.Background()
	if got := ClassifyTransport(live, context.DeadlineExceeded); got != KindNetwork {
		t.Errorf("渠道超时应归 KindNetwork，实际 %q", got)
	}
	err := &ProviderError{Provider: "slow", Kind: KindNetwork, Err: context.DeadlineExceeded}
	if !ShouldFallback(err) {
		t.Error("渠道超时应触发降级（换一个渠道可能就好了）")
	}
	// 调用方自己的 ctx 结束了 → 真的中止 → 不降级。
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	if got := ClassifyTransport(dead, context.DeadlineExceeded); got != KindAborted {
		t.Errorf("调用方已结束应归 KindAborted，实际 %q", got)
	}
}

func TestKindOfNonProviderError(t *testing.T) {
	if got := KindOf(errors.New("随便什么错")); got != KindUnknown {
		t.Errorf("非 ProviderError 应归 KindUnknown，实际 %q", got)
	}
}

// 脱敏：错误消息里回显 key 会把用户的密钥写进会话历史（可搜索、可导出）。
func TestRedact(t *testing.T) {
	key := "tvly-secret-key-1234567890"
	msg := `{"error":"invalid api key tvly-secret-key-1234567890"}`
	got := Redact(msg, key)
	if strings.Contains(got, key) {
		t.Errorf("脱敏后仍含完整 key: %s", got)
	}
	if !strings.Contains(got, "***") {
		t.Errorf("脱敏后应有掩码: %s", got)
	}
	if Redact(msg, "") != msg {
		t.Error("key 为空时不该改动文本")
	}
}

// 短 key 不做前缀替换（否则会把无关文本里恰好相同的前 8 字符也抹掉）。
func TestRedactShortKeyNoPrefixOverreach(t *testing.T) {
	key := "short"
	msg := "short is the key"
	if got := Redact(msg, key); got != "*** is the key" {
		t.Errorf("短 key 只做整串替换，实际 %q", got)
	}
}

func TestHostMatchesDomain(t *testing.T) {
	cases := []struct {
		host, domain string
		want         bool
	}{
		{"example.com", "example.com", true},
		{"a.example.com", "example.com", true},
		{"notexample.com", "example.com", false},
		{"example.com", "www.example.com", true},
		{"A.Example.COM", "example.com", true},
	}
	for _, c := range cases {
		if got := HostMatchesDomain(c.host, c.domain); got != c.want {
			t.Errorf("HostMatchesDomain(%q, %q) = %v，期望 %v", c.host, c.domain, got, c.want)
		}
	}
}

func TestMatchesDomainFilter(t *testing.T) {
	if !MatchesDomainFilter("https://a.com/x", nil, nil) {
		t.Error("无过滤应全部通过")
	}
	if MatchesDomainFilter("https://a.com/x", []string{"b.com"}, nil) {
		t.Error("不在 include 里应被过滤")
	}
	if !MatchesDomainFilter("https://sub.a.com/x", []string{"a.com"}, nil) {
		t.Error("子域应匹配 include")
	}
	if MatchesDomainFilter("https://a.com/x", nil, []string{"a.com"}) {
		t.Error("在 exclude 里应被过滤")
	}
	// URL 无法解析时视为不通过（无法判定的不该放行）。
	if MatchesDomainFilter("不是URL", []string{"a.com"}, nil) {
		t.Error("非法 URL 应被过滤")
	}
}

func TestBuildSiteQuery(t *testing.T) {
	got := BuildSiteQuery("golang 泛型", []string{"a.com", "b.com"}, []string{"c.com"})
	want := "golang 泛型 site:a.com OR site:b.com -site:c.com"
	if got != want {
		t.Errorf("BuildSiteQuery = %q，期望 %q", got, want)
	}
	if got := BuildSiteQuery("q", nil, nil); got != "q" {
		t.Errorf("无过滤时不该改动查询: %q", got)
	}
	if got := BuildSiteQuery("q", []string{"a.com"}, nil); got != "q site:a.com" {
		t.Errorf("单个 include 不该用 OR: %q", got)
	}
}

func TestSourceAnswer(t *testing.T) {
	got := SourceAnswer([]Result{
		{Title: "A", URL: "https://a.com", Snippet: "片段 A"},
		{Title: "B", URL: "https://b.com"},
	})
	want := "片段 A\nSource: A (https://a.com)\n\nSource: B (https://b.com)"
	if got != want {
		t.Errorf("SourceAnswer = %q，期望 %q", got, want)
	}
}

func TestCollapseSpaces(t *testing.T) {
	if got := CollapseSpaces("  a\n\t b   c  "); got != "a b c" {
		t.Errorf("CollapseSpaces = %q", got)
	}
}

func TestSourceTitle(t *testing.T) {
	if got := sourceTitle("标题", 1); got != "标题" {
		t.Errorf("有标题时不该回落: %q", got)
	}
	if got := sourceTitle("  ", 2); got != "Source 2" {
		t.Errorf("空标题应回落 Source N: %q", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
