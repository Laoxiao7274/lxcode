// session.fulltext（UI 全文搜索——A1）：子串匹配转发 store.Search 的用例。
//
// 与 agent 工具 session_search 的分工：fulltext 面向用户输入，**子串**语义
//（模式经 QuoteMeta 转义——括号/星号是字面量），正则语义只保留给 agent。
package server

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// fulltextSearch 发一次 session.fulltext 并返回结果（失败即 Fatal）。
func fulltextSearch(t *testing.T, client *wsTestClient, query string) protocol.SessionFulltextResult {
	t.Helper()
	resp := client.call(protocol.MethodSessionFulltext, protocol.SessionFulltextParams{Query: query})
	if resp == nil || resp.Error != nil {
		t.Fatalf("session.fulltext(%q) 失败: %+v", query, resp)
	}
	var r protocol.SessionFulltextResult
	if err := unmarshalResult(resp, &r); err != nil {
		t.Fatalf("解析应答: %v", err)
	}
	return r
}

// TestFulltextSubstringNotRegex：子串语义——正则元字符按字面量匹配。
// 会话里存一段含括号的文本，用 `(未命名)`（正则里是合法分组但会改语义）与
// `(未命名`（正则里是非法模式，QuoteMeta 后变成合法字面量）都能命中。
func TestFulltextSubstringNotRegex(t *testing.T) {
	srv, client, _ := newTestServer(t, immediateStream)
	id := newSessionForTest(t, client)
	sendTurn(t, srv, client, id, "帮我处理 (未命名) 会话的标题")

	// 字面子串命中
	r := fulltextSearch(t, client, "(未命名)")
	if r.Total == 0 {
		t.Fatalf("字面子串 (未命名) 应命中，got total=0 hits=%d", len(r.Hits))
	}
	found := false
	for _, h := range r.Hits {
		if h.SessionID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("命中应归属该会话 %s，hits=%+v", id, r.Hits)
	}

	// 不完整括号：正则非法（Compile 会报错），子串语义下是合法字面量
	r = fulltextSearch(t, client, "(未命名")
	if r.Total == 0 {
		t.Fatalf("字面子串 (未命名 应命中（正则语义下这是非法模式——会被误报错误），got total=0")
	}
}

// TestFulltextNoMatchAndValidation：零命中正常返回（不是错误）；空 query 报参数错。
func TestFulltextNoMatchAndValidation(t *testing.T) {
	srv, client, _ := newTestServer(t, immediateStream)
	id := newSessionForTest(t, client)
	sendTurn(t, srv, client, id, "普通一句话")

	r := fulltextSearch(t, client, "绝不存在的词xyz")
	if r.Total != 0 || len(r.Hits) != 0 {
		t.Fatalf("无命中应 total=0 hits=0，got %+v", r)
	}

	// 空 query：参数错误
	resp := client.call(protocol.MethodSessionFulltext, protocol.SessionFulltextParams{Query: "  "})
	if resp == nil || resp.Error == nil {
		t.Fatalf("空 query 应报参数错误，got %+v", resp)
	}
}

// TestFulltextExcludesArchived：归档会话不进命中（store.Search 的 archived=0 口径
// 在 fulltext 上保持——侧栏搜索搜的是还能打开的会话）。
func TestFulltextExcludesArchived(t *testing.T) {
	srv, client, _ := newTestServer(t, immediateStream)
	id := newSessionForTest(t, client)
	sendTurn(t, srv, client, id, "独特标记词qwertyuiop")
	// 归档它
	resp := client.call(protocol.MethodSessionArchive, protocol.SessionArchiveParams{ID: id, Archived: true})
	if resp == nil || resp.Error != nil {
		t.Fatalf("归档失败: %+v", resp)
	}

	r := fulltextSearch(t, client, "qwertyuiop")
	for _, h := range r.Hits {
		if h.SessionID == id {
			t.Fatalf("归档会话不应出现在 fulltext 命中里: %+v", r.Hits)
		}
	}
}
