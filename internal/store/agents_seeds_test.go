package store

import (
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
	if got := byID["coder"].Tools; !containsStr(got, "ripgrep") || len(got) != 7 {
		t.Fatalf("用户改过的 coder 行被同步动了: %v", got)
	}
	// 主 Agent 的委派名单（老库仍是上一版种子的值）补上新增的两个
	main := byID["main"]
	for _, want := range []string{"coder", "researcher", "tester"} {
		if !containsStr(main.Delegates, want) {
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
// agent.dispatch 是内置工具、**不在目录里**（目录是拓展目录，内置工具由代码注册），
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
			// 两类制：子 Agent 是纯执行者——白名单不含 agent.dispatch，
			// 也没有委派名单（深度恒 1）
			if containsStr(a.Tools, "agent.dispatch") {
				t.Fatalf("子 Agent %s 的白名单不该含 agent.dispatch（两类制）", a.ID)
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
		if a.IsMain {
			continue
		}
		if !containsStr(main.Delegates, a.ID) {
			t.Fatalf("主 Agent 的委派名单缺种子子 Agent %s: %v", a.ID, main.Delegates)
		}
	}
	for _, d := range main.Delegates {
		if !seen[d] {
			t.Fatalf("主 Agent 的委派名单引用了不存在的 Agent: %s", d)
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
	if !containsStr(tester.Tools, "bash") {
		t.Fatalf("测试 Agent 必须能跑命令（否则无从验证）: %v", tester.Tools)
	}
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
