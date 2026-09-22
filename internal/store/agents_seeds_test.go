package store

import (
	"strings"
	"testing"
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
