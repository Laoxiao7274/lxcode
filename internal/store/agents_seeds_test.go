package store

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// TestSeedToolsRunnable：种子目录里的外部二进制工具必须**可执行**——
// 用户报告过「给了 ripgrep，模型答『注册表没有』」，根因就是种子给了个
// 空 command 的承诺（空 command 进不了注册表）。契约：
//   - ripgrep 带真实命令模板，且模板里的占位符都是已声明的参数；
//   - 未实现的条目（browser）command 留空——它是「声明了没实现」的形态，
//     注册表跳过它、目录页标「未配置」，而不是假装能用。
func TestSeedToolsRunnable(t *testing.T) {
	byID := map[string]struct {
		command string
		params  map[string]bool
	}{}
	for _, tl := range seedTools {
		declared := map[string]bool{}
		for _, p := range tl.Params {
			declared[p.Name] = true
		}
		byID[tl.ID] = struct {
			command string
			params  map[string]bool
		}{tl.Command, declared}
	}

	rg, ok := byID["ripgrep"]
	if !ok {
		t.Fatal("种子缺 ripgrep（AGENTS.md §2.1 的「Go 主刀、Rust 武器库」样板）")
	}
	if strings.TrimSpace(rg.command) == "" {
		t.Fatal("ripgrep 的 command 不能为空——空 command 的工具进不了注册表，模型只会说「注册表没有」")
	}
	if !strings.Contains(rg.command, "{pattern}") {
		t.Fatalf("ripgrep 的命令模板应含 {pattern}: %q", rg.command)
	}
	// 模板里的占位符必须是已声明的参数（否则 CustomDef 注册时直接报错）
	for _, ph := range placeholdersOf(rg.command) {
		if !rg.params[ph] {
			t.Fatalf("ripgrep 的命令模板引用了未声明的参数 {%s}: %q", ph, rg.command)
		}
	}
	// 可选参数（path/glob）必须存在——模板里用到了它们，缺省时整 token 丢掉
	for _, name := range []string{"path", "glob"} {
		if !rg.params[name] {
			t.Fatalf("ripgrep 应声明可选参数 %s（模板里用到）", name)
		}
	}

	if b, ok := byID["browser"]; ok && strings.TrimSpace(b.command) != "" {
		t.Fatal("browser 还没有对应实现，command 应留空（目录标「未配置」）——别给它一个跑不起来的承诺")
	}
}

// TestSyncCatalogSeedsBackfillsOldDB：老库的种子行必须被同步——代码修好了种子
// （给 ripgrep 补上命令、加 read_skill），老库不能永远吃不到（用户报告过：
// 「给了 ripgrep，模型答『注册表没有』」，而种子行 custom=0 又没编辑入口）。
// 边界：用户自建（custom=1）的行一律不碰。
func TestSyncCatalogSeedsBackfillsOldDB(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// 模拟老库：种子行的 command 被清空、种子条目缺失、外加一条用户自建行
	if _, err := st.db.Exec(`UPDATE tools SET command='' WHERE id='ripgrep'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DELETE FROM tools WHERE id='read_skill'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO tools
		(id, desc, risk, source, params, doc, server, command, example, package_file, custom, created_at, updated_at)
		VALUES ('mine', '我的工具', 'low', 'binary', '[]', '', '', 'echo {x}', '', '', 1, 't', 't')`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("重开 Open: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() }) // Windows：句柄挡 TempDir 删除
	list, err := reopened.ListTools()
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	byID := map[string]sessiondata.ToolSpec{}
	for _, x := range list {
		byID[x.ID] = x
	}
	if strings.TrimSpace(byID["ripgrep"].Command) == "" {
		t.Fatal("老库的种子行未被同步：ripgrep 的 command 仍为空（模型仍会说「注册表没有」）")
	}
	if _, ok := byID["read_skill"]; !ok {
		t.Fatal("缺失的种子条目未被补进老库：read_skill")
	}
	if got := byID["mine"]; got.Command != "echo {x}" {
		t.Fatalf("用户自建行被同步动了（custom=1 必须不碰）: %+v", got)
	}
}

// TestSyncSeedAgentsBackfillsOldDB：老库必须吃到**新增的**种子 Agent——Agent 种子
// 原先只在库空时整套注入，于是「代码加了新 Agent，老库永远吃不到」（工具/模块早有
// 同款同步）。边界：已有行一律不碰（用户改过的 coder 行保持原样），主 Agent 的
// 委派名单只在没被动过（仍含上一版种子子 Agent）时才补新增的。
func TestSyncSeedAgentsBackfillsOldDB(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// 模拟老库：两个新种子 Agent 缺失、coder 被用户改过（加了 ripgrep）、
	// 主 Agent 的委派名单还是上一版种子的值
	if _, err := st.db.Exec(`DELETE FROM agents WHERE id IN ('researcher','tester')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE agents SET tools='["read_file","search","edit","write_file","bash","todo","ripgrep"]' WHERE id='coder'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE agents SET delegates='["coder"]' WHERE is_main=1`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("重开 Open: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	list, err := reopened.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	byID := map[string]sessiondata.AgentDef{}
	for _, a := range list {
		byID[a.ID] = a
	}
	for _, id := range []string{"researcher", "tester"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("新增的种子 Agent 未补进老库: %s（名单 %v）", id, list)
		}
	}
	// 用户改过的行不碰：coder 的 ripgrep 还在，且没有被重复追加
	if got := byID["coder"].Tools; !slices.Contains(got, "ripgrep") || len(got) != 7 {
		t.Fatalf("用户改过的 coder 行被同步动了: %v", got)
	}
	// 主 Agent 的委派名单（老库仍是上一版种子的值）补上新增的两个
	main := byID["main"]
	for _, want := range []string{"coder", "researcher", "tester"} {
		if !slices.Contains(main.Delegates, want) {
			t.Fatalf("主 Agent 的委派名单未补上 %s: %v", want, main.Delegates)
		}
	}
	if len(main.Delegates) != 3 {
		t.Fatalf("主 Agent 的委派名单应恰为三个（不重复追加）: %v", main.Delegates)
	}

	// 幂等：再开一次，名单与行数都不变
	again, err := Open(dir)
	if err != nil {
		t.Fatalf("再开 Open: %v", err)
	}
	t.Cleanup(func() { _ = again.Close() })
	list2, err := again.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	if len(list2) != len(list) {
		t.Fatalf("种子同步应幂等（数量稳定）: %d vs %d", len(list2), len(list))
	}
	for _, a := range list2 {
		if a.ID == "main" && len(a.Delegates) != 3 {
			t.Fatalf("主 Agent 委派名单被重复追加: %v", a.Delegates)
		}
	}
}

// TestSyncSeedAgentsKeepsUserEditedDelegates：用户动过主 Agent 的委派名单
// （删掉了种子子 Agent）时一律不碰——补名单只在"没被动过"时做，否则就是把用户
// 删掉的东西反复塞回去。
func TestSyncSeedAgentsKeepsUserEditedDelegates(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := st.db.Exec(`DELETE FROM agents WHERE id IN ('researcher','tester')`); err != nil {
		t.Fatal(err)
	}
	// 用户把种子子 Agent 从名单里删光了（空名单 = 明确的用户编辑）
	if _, err := st.db.Exec(`UPDATE agents SET delegates='[]' WHERE is_main=1`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("重开 Open: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	list, err := reopened.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]sessiondata.AgentDef{}
	for _, a := range list {
		byID[a.ID] = a
	}
	if _, ok := byID["researcher"]; !ok {
		t.Fatal("缺失的种子 Agent 行仍应补进老库（行与名单是两件事）")
	}
	if got := byID["main"].Delegates; len(got) != 0 {
		t.Fatalf("用户动过的委派名单不该被改: %v", got)
	}
}

// TestSeedAgentsSelfConsistent：种子 Agent 自洽——每个种子引用的 workflow/skills/
// tools id 必须真实存在（server 按 id 解析时**缺失静默跳过**：打错就是一份空提示词，
// 没有任何报错），并守住两类制与执行面边界。
//
// 工具面的判据 = 目录种子（command 非空的，能进注册表）∪ 注册表内置工具：
// agent_dispatch 是内置工具、**不在目录里**（目录是拓展目录，内置工具由代码注册），
// 所以只查目录会把主 Agent 误判成引用了不存在的工具。测试引 tools 包拿内置清单
// 是允许的（架构守卫只查非测试文件；tools 不反向依赖 store，不成环）。
func TestSeedAgentsSelfConsistent(t *testing.T) {
	modKind := map[string]string{}
	for _, m := range seedModules {
		modKind[m.ID] = m.Kind
	}
	// 工具面：目录里能跑的种子工具（binary 有 command 才进得了注册表）+ 注册表
	// 内置工具（builtin 条目由 Go 代码实现，Command 本来就是空的——不能按空
	// command 判成"没实现"，那是把 read_file 这类误判掉）
	validTool := map[string]bool{}
	for _, tl := range seedTools {
		if tl.Command != "" {
			validTool[tl.ID] = true
		}
	}
	for _, name := range tools.New().Order() {
		validTool[name] = true
	}
	seen := map[string]bool{}
	var hasMain bool
	for _, a := range seedAgents {
		if seen[a.ID] {
			t.Fatalf("种子 Agent id 重复: %s", a.ID)
		}
		seen[a.ID] = true
		if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Desc) == "" {
			t.Fatalf("种子 Agent 缺名字或职责描述（主 Agent 的选人信号）: %+v", a)
		}
		if a.IsMain {
			hasMain = true
		}
		if a.Workflow != "" {
			kind, ok := modKind[a.Workflow]
			if !ok {
				t.Fatalf("%s 引用了不存在的流程模块 %s（解析静默跳过 = 空提示词）", a.ID, a.Workflow)
			}
			if kind != "process" {
				t.Fatalf("%s 的流程模块 %s 不是 process: %s", a.ID, a.Workflow, kind)
			}
		}
		for _, s := range a.Skills {
			kind, ok := modKind[s]
			if !ok {
				t.Fatalf("%s 引用了不存在的技能模块 %s", a.ID, s)
			}
			if kind != "skill" {
				t.Fatalf("%s 的技能 %s 不是 skill: %s", a.ID, s, kind)
			}
		}
		for _, tl := range a.Tools {
			if !validTool[tl] {
				t.Fatalf("%s 的白名单引用了不可用的工具 %s（不存在，或声明了没实现——提示词会点名「当前不可用」）", a.ID, tl)
			}
		}
		if !a.IsMain {
			// 两类制：子 Agent 是纯执行者——白名单不含 agent_dispatch，
			// 也没有委派名单（深度恒 1）
			if slices.Contains(a.Tools, "agent_dispatch") {
				t.Fatalf("子 Agent %s 的白名单不该含 agent_dispatch（两类制）", a.ID)
			}
			if len(a.Delegates) != 0 {
				t.Fatalf("子 Agent %s 不该有委派名单（两类制深度恒 1）: %v", a.ID, a.Delegates)
			}
		}
	}
	if !hasMain {
		t.Fatal("种子缺主 Agent")
	}
	// 主 Agent 的委派名单必须指向存在的启用子 Agent，且含全部种子子 Agent
	//（否则「加了却派不出去」——派发校验会拒）
	main := sessiondata.AgentDef{}
	for _, a := range seedAgents {
		if a.IsMain {
			main = a
		}
	}
	for _, a := range seedAgents {
		if a.IsMain || seedNonDelegatable[a.ID] {
			// 合并 Agent 这类由其他 producer 拉起的子 Agent 不进委派名单
			//（它不是被派活的，见 seedNonDelegatable）
			continue
		}
		if !slices.Contains(main.Delegates, a.ID) {
			t.Fatalf("主 Agent 的委派名单缺种子子 Agent %s: %v", a.ID, main.Delegates)
		}
	}
	for _, d := range main.Delegates {
		if !seen[d] {
			t.Fatalf("主 Agent 的委派名单引用了不存在的 Agent: %s", d)
		}
	}
}

// TestBuiltinToolsPresentInCatalog：**每个注册表内置工具都必须在目录种子里有条目**。
//
// 为什么需要这条（自洽性测试抓不到）：TestSeedAgentsSelfConsistent 的工具面判据是
// 「目录种子 ∪ 注册表内置」，所以一个内置工具即使目录里没有条目，白名单引用它照样
// 通过校验——运行期也确实可用（注册表有实现）。但**前端 Agent 编辑器渲染 chip 只读
// 目录**（AgentEditor 的 builtinToolChips = 目录里 source=builtin 的条目），目录里
// 没有 = 用户在界面上勾不到它 = 这个工具谁都授权不了。
//
// 实际发生过：web_search 与 agent_dispatch 都不在目录里，而默认会话用的主 Agent
// 白名单只有 agent_dispatch —— web_search 做完了却没有任何入口能用上它。
func TestBuiltinToolsPresentInCatalog(t *testing.T) {
	inCatalog := map[string]bool{}
	for _, tl := range seedTools {
		if tl.Source == "builtin" {
			inCatalog[tl.ID] = true
		}
	}
	for _, name := range tools.New().Order() {
		if !inCatalog[name] {
			t.Errorf("内置工具 %s 不在目录种子里——Agent 编辑器渲染不出它的 chip，用户无法授权（只加白名单没用）", name)
		}
	}
}

// TestSeedAgentsCoverExecutionSurfaces：名单要覆盖三种执行面（实现 / 勘察 / 验证）
// ——调研 Agent 只读（无变更类工具），测试 Agent 能跑命令（bash）。
func TestSeedAgentsCoverExecutionSurfaces(t *testing.T) {
	byID := map[string]sessiondata.AgentDef{}
	for _, a := range seedAgents {
		byID[a.ID] = a
	}
	researcher, ok := byID["researcher"]
	if !ok {
		t.Fatal("种子缺调研 Agent")
	}
	for _, tl := range researcher.Tools {
		if tl == "edit" || tl == "write_file" || tl == "bash" {
			t.Fatalf("调研 Agent 是只读面，不该含变更类工具 %s: %v", tl, researcher.Tools)
		}
	}
	if researcher.Approval != "strict" {
		t.Fatalf("调研 Agent 的权限默认应为 strict（只读，纵深防御）: %q", researcher.Approval)
	}
	tester, ok := byID["tester"]
	if !ok {
		t.Fatal("种子缺测试 Agent")
	}
	if !slices.Contains(tester.Tools, "bash") {
		t.Fatalf("测试 Agent 必须能跑命令（否则无从验证）: %v", tester.Tools)
	}
}

// agentToolsOf 读回某个 Agent 的白名单（补种的断言口径）。
func agentToolsOf(t *testing.T, s *Store, id string) []string {
	t.Helper()
	var raw string
	if err := s.db.QueryRow(`SELECT tools FROM agents WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return decodeStrList(raw)
}

// agentDescOf 读回某个 Agent 的职责描述（主 Agent 的选人信号，补种断言口径）。
func agentDescOf(t *testing.T, s *Store, id string) string {
	t.Helper()
	var desc string
	if err := s.db.QueryRow(`SELECT desc FROM agents WHERE id = ?`, id).Scan(&desc); err != nil {
		t.Fatal(err)
	}
	return desc
}

// setAgentDescOf 把某个 Agent 的职责描述改成给定值（造"老库"或"用户改过"场景用）。
func setAgentDescOf(t *testing.T, s *Store, id, desc string) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE agents SET desc=? WHERE id=?`, desc, id); err != nil {
		t.Fatal(err)
	}
}

// setAgentToolsOf 把某个 Agent 的白名单改成给定值（造"老库"场景用）。
func setAgentToolsOf(t *testing.T, s *Store, id string, tools []string) {
	t.Helper()
	raw, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE agents SET tools=? WHERE id=?`, string(raw), id); err != nil {
		t.Fatal(err)
	}
}

// TestSeedResearcherHasWebSearch：调研 Agent 的白名单必须含 web_search。
//
// 它是调研的核心能力之一（本仓库代码之外的资料都靠它），而且是只读工具——
// approval=strict 的调研面照样能用（Mutates=false）。新装的库由种子直接写入，
// 已有库靠 topUpSeedAgents 补（见下面的测试）。
func TestSeedResearcherHasWebSearch(t *testing.T) {
	s := openTestStore(t)
	got := agentToolsOf(t, s, "researcher")
	if !slices.Contains(got, "web_search") {
		t.Fatalf("调研 Agent 的白名单缺 web_search（调研的一半资料靠它）: %v", got)
	}
}

// TestSeedResearcherHasWebFetch：调研 Agent 的白名单必须含 web_fetch。
//
// 与 web_search 同一条纪律的另一半：搜索只回标题与摘要，**正文靠抓取**。
// 调研 Agent 拿不到 web_fetch 就只能凭摘要猜——工具做完了没人用得上，
// 正是 web_search 栽过的那一跤。
func TestSeedResearcherHasWebFetch(t *testing.T) {
	s := openTestStore(t)
	got := agentToolsOf(t, s, "researcher")
	if !slices.Contains(got, "web_fetch") {
		t.Fatalf("调研 Agent 的白名单缺 web_fetch（搜索只给摘要，正文要靠它）: %v", got)
	}
}

// TestSeedResearcherDescMentionsWebFetch：职责描述必须点明抓正文。
//
// 与联网搜索同理：描述是主 Agent 的选人信号，漏了「抓取网页正文」，
// 主 Agent 就不知道「去读那篇文档的全文」该派给谁。
func TestSeedResearcherDescMentionsWebFetch(t *testing.T) {
	byID := map[string]sessiondata.AgentDef{}
	for _, a := range seedAgents {
		byID[a.ID] = a
	}
	if got := byID["researcher"].Desc; !strings.Contains(got, "抓取网页正文") {
		t.Fatalf("调研 Agent 的职责描述没提抓取网页正文——主 Agent 的选人信号里缺这项能力: %q", got)
	}
}

// TestSeedResearcherDescMentionsWebSearch：调研 Agent 的**职责描述**必须点明联网搜索。
//
// 描述不只是给人看的说明——它是**主 Agent 的选人信号**：可委派名单按 desc 逐字
// 生成（compose.go 的 ④），主 Agent 只看描述决定把任务派给谁。所以描述里漏掉
// 「联网搜索」，主 Agent 就不知道该把「查外部资料」派给谁——工具给了、白名单也
// 勾了，选人那一步没有信号，这个能力照样等于不存在（用户实测反馈）。
func TestSeedResearcherDescMentionsWebSearch(t *testing.T) {
	byID := map[string]sessiondata.AgentDef{}
	for _, a := range seedAgents {
		byID[a.ID] = a
	}
	got := byID["researcher"].Desc
	if !strings.Contains(got, "联网搜索") {
		t.Fatalf("调研 Agent 的职责描述没提联网搜索——主 Agent 的选人信号里就没有这个能力: %q", got)
	}
}

// TestTopUpSeedAgentsBackfillsUntouchedWhitelist：老库升级的补种（白名单）。
//
// 场景：库里那行是上一版种子写的（白名单没有 web_search），而 Agent 名单的种子
// 同步只插缺失行、不 UPDATE 已有行——不补种的话，代码给种子白名单加了工具，
// 已有库永远吃不到：目录里有它、编辑器里能勾，但没有任何 Agent 勾着它，
// 工具做完了没人用得上（web_search 的实际遭遇）。
func TestTopUpSeedAgentsBackfillsUntouchedWhitelist(t *testing.T) {
	s := openTestStore(t)
	base := seedAgentBaselines["researcher"]
	setAgentToolsOf(t, s, "researcher", base.Tools) // 回到"上一版"
	if err := s.syncCatalogSeeds("2026-09-28T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	got := agentToolsOf(t, s, "researcher")
	if !slices.Contains(got, "web_search") {
		t.Fatalf("没动过的白名单应该被补上 web_search，实际: %v", got)
	}
	// 补种只加不减：基线里的工具一个都不能丢
	if !containsAll(got, base.Tools) {
		t.Fatalf("补种弄丢了原有工具: %v", got)
	}
	// 幂等：再同步一次不该有任何变化（也不该动 updated_at）
	before := strings.Join(got, ",")
	if err := s.syncCatalogSeeds("2026-09-28T00:00:01Z"); err != nil {
		t.Fatal(err)
	}
	if after := strings.Join(agentToolsOf(t, s, "researcher"), ","); after != before {
		t.Fatalf("补种不幂等: %s → %s", before, after)
	}
}

// TestTopUpSeedAgentsLeavesEditedWhitelistAlone：用户删过种子工具就完全不碰白名单。
//
// 无条件追加会把用户删掉的工具每次 Open 都塞回去——那就是吃掉用户的编辑。
func TestTopUpSeedAgentsLeavesEditedWhitelistAlone(t *testing.T) {
	s := openTestStore(t)
	edited := []string{"read_file", "search", "session_search"} // 用户删了 ripgrep
	setAgentToolsOf(t, s, "researcher", edited)
	if err := s.syncCatalogSeeds("2026-09-28T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	got := agentToolsOf(t, s, "researcher")
	if strings.Join(got, ",") != strings.Join(edited, ",") {
		t.Fatalf("用户动过的白名单被改了（补种越界）: %v", got)
	}
}

// TestTopUpSeedAgentsBackfillsUntouchedDesc：老库升级的补种（职责描述）。
//
// 场景同上，但坏的是选人信号那一半：库里的描述还是上一版的，而主 Agent
// 只按描述选人——描述不补，主 Agent 就不知道「查外部资料」该派给谁。
func TestTopUpSeedAgentsBackfillsUntouchedDesc(t *testing.T) {
	s := openTestStore(t)
	base := seedAgentBaselines["researcher"]
	setAgentDescOf(t, s, "researcher", base.Desc) // 回到"上一版"
	if err := s.syncCatalogSeeds("2026-09-28T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	got := agentDescOf(t, s, "researcher")
	if got != seedResearcherDesc(t) {
		t.Fatalf("没改过的描述应该被更新成本版，实际: %q", got)
	}
	if !strings.Contains(got, "联网搜索") {
		t.Fatalf("补种后的描述仍没有联网搜索——主 Agent 的选人信号还是缺的: %q", got)
	}
}

// TestTopUpSeedAgentsLeavesEditedDescAlone：用户改过职责描述就完全不碰。
func TestTopUpSeedAgentsLeavesEditedDescAlone(t *testing.T) {
	s := openTestStore(t)
	edited := "我自己写的调研 Agent 说明"
	setAgentDescOf(t, s, "researcher", edited)
	if err := s.syncCatalogSeeds("2026-09-28T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if got := agentDescOf(t, s, "researcher"); got != edited {
		t.Fatalf("用户改过的描述被覆盖了（补种越界）: %q", got)
	}
}

// TestTopUpSeedAgentsSkipsMainAgent：主 Agent 的工具是结构性的（调度通道 +
// 合并进程入口 + 工作区三件套），不参与补种。
func TestTopUpSeedAgentsSkipsMainAgent(t *testing.T) {
	s := openTestStore(t)
	if err := s.syncCatalogSeeds("2026-09-28T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	got := agentToolsOf(t, s, "main")
	// 主 Agent 恒为「调度 + 合并进程入口 + 工作区四件套」（结构成员，不由补种改写）
	for _, want := range []string{"agent_dispatch", "merge_request",
		"workspace_status", "workspace_sync", "workspace_rollback", "workspace_publish"} {
		if !slices.Contains(got, want) {
			t.Fatalf("主 Agent 的工具应含 %s（结构性）: %v", want, got)
		}
	}
	if len(got) != 6 {
		t.Fatalf("主 Agent 的工具应恒为 agent_dispatch + merge_request + 工作区四件套: %v", got)
	}
}

// TestSeedAgentBaselinesIsMeaningful：基线表引用的 id 必须是真的种子子 Agent，
// 且基线必须是当前种子白名单的**真子集**、描述必须与当前种子**不同**——
// 否则那条目没有任何补种作用（拼错 id、忘了留旧值、或描述没改过都会静默失效，
// 补种等于没写）。
func TestSeedAgentBaselinesIsMeaningful(t *testing.T) {
	byID := map[string]sessiondata.AgentDef{}
	for _, a := range seedAgents {
		byID[a.ID] = a
	}
	for id, base := range seedAgentBaselines {
		a, ok := byID[id]
		if !ok {
			t.Fatalf("seedAgentBaselines 引用了不存在的种子 Agent: %s", id)
		}
		if a.IsMain {
			t.Fatalf("seedAgentBaselines 不该含主 Agent（工具是结构性的）: %s", id)
		}
		if !containsAll(a.Tools, base.Tools) {
			t.Fatalf("%s 的工具基线不是当前种子白名单的子集（留错了旧值）: 基线 %v / 种子 %v", id, base.Tools, a.Tools)
		}
		if len(a.Tools) == len(base.Tools) {
			t.Fatalf("%s 的工具基线与当前种子白名单等长——没有新增工具可补，这条目是多余的", id)
		}
		if base.Desc == "" || base.Desc == a.Desc {
			t.Fatalf("%s 的描述基线与当前种子相同——没有新描述可补，这条目是多余的", id)
		}
	}
}

// seedResearcherDesc 取调研 Agent 的当前种子描述（断言口径）。
func seedResearcherDesc(t *testing.T) string {
	t.Helper()
	for _, a := range seedAgents {
		if a.ID == "researcher" {
			return a.Desc
		}
	}
	t.Fatal("种子缺调研 Agent")
	return ""
}

// placeholdersOf 提取模板里的 {name}（与 tools 包的校验同语义——这里只需
// 覆盖种子命令的用法，不引 tools 包避免 store → tools 的反向依赖）。
func placeholdersOf(template string) []string {
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(template); i++ {
		if template[i] != '{' {
			continue
		}
		j := strings.IndexByte(template[i:], '}')
		if j < 0 {
			break
		}
		name := template[i+1 : i+j]
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		i += j
	}
	return out
}
