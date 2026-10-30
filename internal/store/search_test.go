// search_test.go —— Search 扫描成本护栏的行为测试：
// 超时早退（timedOut 标记 + 部分结果）、session 过滤、days 过滤。
// 基础搜索行为（排序/上下文/role 过滤/总数语义）在 store_test.go 的 TestSearch 系列。
package store

import (
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/llm"
)

// touchSession 把会话的 updated_at 改成 now-ago（days 过滤测试用——
// AppendMsg 只会把它推到现在，构造「N 天前的旧会话」得直接改库）。
func touchSession(t *testing.T, s *Store, id string, ago time.Duration) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`,
		time.Now().Add(-ago).Format(time.RFC3339Nano), id); err != nil {
		t.Fatal(err)
	}
}

// TestSearchTimedOut：DeadlineMS=1ms 强制超时——小库也许来得及扫完，
// 所以断言是「或全」形态：要么完整返回（timedOut=false，与无限时一致），
// 要么标记 timedOut 且已命中的照常返回（total ≥ len(hits)）。
// 超时行为本身（大库上必然触发）由真库实测覆盖。
func TestSearchTimedOut(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	if _, err := s.AppendMsg(id, llm.Message{Role: "user", Content: "唯一关键词在这里"}); err != nil {
		t.Fatal(err)
	}
	hits, total, timedOut, err := s.Search(SearchQuery{Pattern: "唯一关键词", Max: 10, DeadlineMS: 1})
	if err != nil {
		t.Fatal(err)
	}
	if timedOut {
		// 超时路径：命中若返回则仍可用；total 不小于已返回命中数
		if total < len(hits) {
			t.Fatalf("超时后 total(%d) 不应小于已返回命中数(%d)", total, len(hits))
		}
	} else if len(hits) != 1 {
		t.Fatalf("未超时应完整命中 1 条: %d", len(hits))
	}
	// **零命中 + 超时也必须能报告**（timedOut 独立成值而非命中标记的原因）：
	_, _, timedOut, err = s.Search(SearchQuery{Pattern: "绝不存在的词", Max: 10, DeadlineMS: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 小库可能真在 1ms 内扫完——两种都合法，关键是返回通道存在（编译期已证）。
	_ = timedOut
}

// TestSearchSessionFilter：session 过滤只命中指定会话，其他会话的相同词排除。
func TestSearchSessionFilter(t *testing.T) {
	s := openTestStore(t)
	idA, _ := s.Create()
	if _, err := s.AppendMsg(idA, llm.Message{Role: "user", Content: "端口 8080 被占用"}); err != nil {
		t.Fatal(err)
	}
	idB, _ := s.Create()
	if _, err := s.AppendMsg(idB, llm.Message{Role: "user", Content: "端口 8080 被占用"}); err != nil {
		t.Fatal(err)
	}
	hits, total, _, err := s.Search(SearchQuery{Pattern: "端口", Max: 10, Session: idA})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(hits) != 1 || hits[0].SessionID != idA {
		t.Fatalf("session 过滤应只命中 idA: total=%d hits=%+v", total, hits)
	}
	// 不存在的 session：零命中
	hits, total, _, err = s.Search(SearchQuery{Pattern: "端口", Max: 10, Session: "不存在的id"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(hits) != 0 {
		t.Fatalf("不存在的 session 应零命中: total=%d hits=%+v", total, hits)
	}
}

// TestSearchDaysFilter：days 过滤按 sessions.updated_at——新会话命中，旧会话排除。
func TestSearchDaysFilter(t *testing.T) {
	s := openTestStore(t)
	idNew, _ := s.Create()
	if _, err := s.AppendMsg(idNew, llm.Message{Role: "user", Content: "老话题重现"}); err != nil {
		t.Fatal(err)
	}
	idOld, _ := s.Create()
	if _, err := s.AppendMsg(idOld, llm.Message{Role: "user", Content: "老话题重现"}); err != nil {
		t.Fatal(err)
	}
	// idOld 伪装成 30 天前（新建恒是现在，必须直接改库）
	touchSession(t, s, idOld, 30*24*time.Hour)
	// 不带 days：两条都命中
	_, total, _, err := s.Search(SearchQuery{Pattern: "老话题", Max: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("不带 days 应命中 2 条: %d", total)
	}
	// days=7：只有新会话
	hits, total, _, err := s.Search(SearchQuery{Pattern: "老话题", Max: 10, Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(hits) != 1 || hits[0].SessionID != idNew {
		t.Fatalf("days=7 应只命中新会话: total=%d hits=%+v", total, hits)
	}
	// days=60：旧的也回来
	_, total, _, err = s.Search(SearchQuery{Pattern: "老话题", Max: 10, Days: 60})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("days=60 应命中 2 条: %d", total)
	}
	// days 与 session 可叠加
	hits, total, _, err = s.Search(SearchQuery{Pattern: "老话题", Max: 10, Days: 7, Session: idOld})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(hits) != 0 {
		t.Fatalf("days=7 叠加旧会话应零命中: total=%d hits=%+v", total, hits)
	}
}

// TestFormatSearchHitsTimedOut：超时标记在格式化文本里如实转告模型——
// 包括零命中场景（「没有匹配」+「可能不全」，不许模型当成全集）。
func TestFormatSearchHitsTimedOut(t *testing.T) {
	got := FormatSearchHits(nil, 0, true, "")
	if !strings.Contains(got, "可能不全") || !strings.Contains(got, "没有匹配") {
		t.Fatalf("零命中+超时应提示可能不全: %q", got)
	}
	hits := []SearchHit{{SessionID: "s1", Content: "x", Index: 1, Role: "user"}}
	got = FormatSearchHits(hits, 5, true, "")
	if !strings.Contains(got, "可能不全") || !strings.Contains(got, "共 5 条") {
		t.Fatalf("命中+超时应同时带总数与可能不全提示: %q", got)
	}
	// 非超时文本不掺入提示（存量文案不变——Android RealBackend 有文案映射依赖）
	got = FormatSearchHits(hits, 5, false, "")
	if strings.Contains(got, "可能不全") {
		t.Fatalf("未超时不应出现提示: %q", got)
	}
}

// TestSearchWorkspaceFilter：workspace 过滤——项目会话命中、未分组排除、
// 子会话（拷贝父 workspace 的行）一起命中、不存在的项目零命中。
func TestSearchWorkspaceFilter(t *testing.T) {
	s := openTestStore(t)
	idProj, _ := s.Create()
	if err := s.SessionWorkspace(idProj, "proj-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(idProj, llm.Message{Role: "user", Content: "构建失败看这里"}); err != nil {
		t.Fatal(err)
	}
	// 子会话：CreateChild 从父行 SELECT workspace——拷贝值应随父一起命中
	idChild, err := s.CreateChild(idProj, "coder", "disp-1", idProj, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(idChild, llm.Message{Role: "assistant", Content: "子会话修复记录"}); err != nil {
		t.Fatal(err)
	}
	idUngrouped, _ := s.Create()
	if _, err := s.AppendMsg(idUngrouped, llm.Message{Role: "user", Content: "构建失败看这里"}); err != nil {
		t.Fatal(err)
	}
	idOther, _ := s.Create()
	if err := s.SessionWorkspace(idOther, "proj-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(idOther, llm.Message{Role: "user", Content: "构建失败看这里"}); err != nil {
		t.Fatal(err)
	}

	// 不带 workspace：全部命中（全库语义不变）
	_, total, _, err := s.Search(SearchQuery{Pattern: "构建失败", Max: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("不带 workspace 应命中 3 条: %d", total)
	}
	// 限定 proj-a：父 + 子命中，未分组与别的项目排除
	hits, total, _, err := s.Search(SearchQuery{Pattern: "构建失败", Max: 10, Workspace: "proj-a"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(hits) != 1 || hits[0].SessionID != idProj {
		t.Fatalf("workspace=proj-a 应只命中父会话: total=%d hits=%+v", total, hits)
	}
	// 限定 proj-b：只有 proj-b 的会话
	hits, total, _, err = s.Search(SearchQuery{Pattern: "构建失败", Max: 10, Workspace: "proj-b"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(hits) != 1 || hits[0].SessionID != idOther {
		t.Fatalf("workspace=proj-b 应只命中 idOther: total=%d hits=%+v", total, hits)
	}
	// 子会话自己的内容也能按父的 workspace 搜到（workspace 过滤涵盖子会话的核心断言）
	hits, total, _, err = s.Search(SearchQuery{Pattern: "子会话修复", Max: 10, Workspace: "proj-a"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(hits) != 1 || hits[0].SessionID != idChild {
		t.Fatalf("workspace 过滤应命中子会话（拷贝父的值）: total=%d hits=%+v", total, hits)
	}
	// 不存在的项目：零命中
	hits, total, _, err = s.Search(SearchQuery{Pattern: "构建失败", Max: 10, Workspace: "不存在的项目"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(hits) != 0 {
		t.Fatalf("不存在的 workspace 应零命中: total=%d hits=%+v", total, hits)
	}
	// workspace 与 session 叠加：session 本身不属于该项目 → 零命中
	hits, total, _, err = s.Search(SearchQuery{Pattern: "构建失败", Max: 10, Session: idProj, Workspace: "proj-b"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(hits) != 0 {
		t.Fatalf("workspace 与 session 叠加应零命中: total=%d hits=%+v", total, hits)
	}
	// workspace 与 days 叠加（走 AND 拼接分支）
	_, total, _, err = s.Search(SearchQuery{Pattern: "构建失败", Max: 10, Days: 7, Workspace: "proj-a"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("days=7 叠加 workspace=proj-a 应命中 1 条（都是新会话）: %d", total)
	}
}

// TestFormatSearchHitsScope：范围说明在结果头部注明（限定项目时模型才知道
// 看到的是子集）；空 scope 不掺入任何内容（全库形态与改动前逐字节一致）。
func TestFormatSearchHitsScope(t *testing.T) {
	hits := []SearchHit{{SessionID: "s1", Content: "x", Index: 1, Role: "user"}}
	got := FormatSearchHits(hits, 1, false, "已限定当前项目 proj-a")
	if !strings.Contains(got, "范围说明") || !strings.Contains(got, "proj-a") {
		t.Fatalf("非全库扫描应在头部注明范围: %q", got)
	}
	if strings.Index(got, "范围说明") > len("（范围说明") && strings.Index(got, "\n") > strings.Index(got, "范围说明") {
		t.Fatalf("范围说明应在第一行: %q", got)
	}
	plain := FormatSearchHits(hits, 1, false, "")
	if strings.Contains(plain, "范围说明") {
		t.Fatalf("空 scope 不应出现范围说明: %q", plain)
	}
	// 零命中同样带范围说明（「没搜到」要能被理解成「这个项目里没有」）
	none := FormatSearchHits(nil, 0, false, "已限定当前项目 proj-a")
	if !strings.Contains(none, "范围说明") || !strings.Contains(none, "没有匹配") {
		t.Fatalf("零命中也应带范围说明: %q", none)
	}
}
