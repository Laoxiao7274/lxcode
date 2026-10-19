// 组合键（provider::id）的内核契约。三条纪律各有钉子：
//
//  1. **并存**：两家提供商的同名模型是两个独立条目，互不覆盖（这是引入 provider 的全部理由）；
//  2. **存量零迁移**：没有 provider 字段的旧 models.json 原样加载，引用（纯 id）照旧解析；
//  3. **不静默回落**：引用解析不出就报错，不猜「大概是这个」。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func providerModel(provider, id string) ModelConfig {
	m := sampleModel(id)
	m.Provider = provider
	return m
}

func TestKeyComposition(t *testing.T) {
	if got := (ModelConfig{ID: "gpt-4o"}).Key(); got != "gpt-4o" {
		t.Fatalf("无 provider 时键应是纯 id: %q", got)
	}
	if got := (ModelConfig{ID: "gpt-4o", Provider: "openrouter"}).Key(); got != "openrouter::gpt-4o" {
		t.Fatalf("组合键拼错: %q", got)
	}
	p, id := SplitKey("openrouter::gpt-4o")
	if p != "openrouter" || id != "gpt-4o" {
		t.Fatalf("SplitKey: %q %q", p, id)
	}
	// 纯 id 与畸形键都原样回落成「无 provider」——引用不该被猜着解释。
	for _, ref := range []string{"gpt-4o", "::gpt-4o", "gpt-4o::", "a::b::c"} {
		if p, id := SplitKey(ref); id == "" || (p == "" && id != ref) {
			t.Fatalf("SplitKey(%q) = %q %q", ref, p, id)
		}
	}
}

func TestValidateRejectsSeparator(t *testing.T) {
	// 分隔符出现在 id/provider 里会让引用解析歧义——早拒。
	if err := providerModel("a::b", "x").validate(); err == nil {
		t.Fatal("provider 含 :: 应拒绝")
	}
	m := sampleModel("x")
	m.ID = "a::b"
	if err := m.validate(); err == nil {
		t.Fatal("id 含 :: 应拒绝")
	}
}

func TestSameNameDifferentProvidersCoexist(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.Add(providerModel("p1", "gpt-4o")); err != nil {
		t.Fatalf("Add p1: %v", err)
	}
	if err := r.Add(providerModel("p2", "gpt-4o")); err != nil {
		t.Fatalf("同名不同 provider 应能并存: %v", err)
	}
	// 同一 provider 下同名才是重复
	if err := r.Add(providerModel("p1", "gpt-4o")); err == nil {
		t.Fatal("同一 provider 重复添加应报错")
	}
	if got := r.List(); len(got) != 2 {
		t.Fatalf("应有两条: %+v", got)
	}
	// 各自取回，属性独立
	m1, ok := r.Get("p1::gpt-4o")
	if !ok || m1.Provider != "p1" {
		t.Fatalf("Get p1: %+v %v", m1, ok)
	}
	m2, ok := r.Get("p2::gpt-4o")
	if !ok || m2.Provider != "p2" {
		t.Fatalf("Get p2: %+v %v", m2, ok)
	}
	// 只删一条，另一条不受影响
	if err := r.Remove("p1::gpt-4o"); err != nil {
		t.Fatalf("Remove p1: %v", err)
	}
	if _, ok := r.Get("p2::gpt-4o"); !ok {
		t.Fatal("删 p1 不该动 p2")
	}
	if _, ok := r.Get("p1::gpt-4o"); ok {
		t.Fatal("p1 应已删除")
	}
}

func TestLegacyFileLoadsWithoutProvider(t *testing.T) {
	// 存量 models.json：没有 provider 字段，roles 存纯 id —— 零修改必须照旧工作。
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	legacy := `{"version":1,"models":[` +
		`{"id":"m1","base_url":"http://a/v1","model":"m1","enabled":true},` +
		`{"id":"m2","base_url":"http://b/v1","model":"m2","enabled":true}],` +
		`"roles":{"default":"m1","vision":"m2"}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Load(path)
	if err != nil {
		t.Fatalf("加载存量注册表: %v", err)
	}
	m, ok := r.Get("m1")
	if !ok || m.Provider != "" || m.Key() != "m1" {
		t.Fatalf("存量条目应保持纯 id 键: %+v", m)
	}
	// 角色绑定照旧解析
	got, err := r.ModelForRole(RoleDefault)
	if err != nil || got.ID != "m1" {
		t.Fatalf("default 角色: %+v %v", got, err)
	}
	// 角色引用值落盘归一后仍是纯 id（没有 provider 可加）
	if err := r.SetRole(RoleVision, "m2"); err != nil {
		t.Fatal(err)
	}
	if got := r.RoleBindings()[RoleVision]; got != "m2" {
		t.Fatalf("存量角色值应保持纯 id: %q", got)
	}
}

func TestRoleBindingAcceptsBothForms(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.Add(providerModel("p1", "m")); err != nil {
		t.Fatal(err)
	}
	if err := r.SetRole(RoleDefault, "p1::m"); err != nil {
		t.Fatalf("组合键绑定: %v", err)
	}
	if got := r.RoleBindings()[RoleDefault]; got != "p1::m" {
		t.Fatalf("绑定值应为规范键: %q", got)
	}
	m, err := r.ModelForRole(RoleDefault)
	if err != nil || m.Key() != "p1::m" {
		t.Fatalf("ModelForRole: %+v %v", m, err)
	}
	// 被角色引用时拒绝删除
	if err := r.Remove("p1::m"); err == nil {
		t.Fatal("被引用时应拒绝删除")
	}
	// 裸 id **不能**跨 provider 顶替 p1 的条目：解析不出就是错
	if err := r.SetRole(RoleDefault, "m"); err == nil {
		t.Fatal("裸 id 不该命中带 provider 的条目（跨 provider 回落是猜）")
	}
	// 存量裸引用（条目本来就无 provider）照旧解析
	if err := r.Add(sampleModel("legacy")); err != nil {
		t.Fatal(err)
	}
	if err := r.SetRole(RoleDefault, "legacy"); err != nil {
		t.Fatalf("存量裸 id 绑定: %v", err)
	}
	if got, _ := r.RoleBindings()[RoleDefault]; got != "legacy" {
		t.Fatalf("存量绑定值应保持纯 id: %q", got)
	}
}

func TestUpdateLocatesByCompositeKey(t *testing.T) {
	r, path := newTestRegistry(t)
	if err := r.Add(providerModel("p1", "m")); err != nil {
		t.Fatal(err)
	}
	m, _ := r.Get("p1::m")
	m.ContextWindow = 65536
	if err := r.Update(m); err != nil {
		t.Fatalf("按组合键 Update: %v", err)
	}
	if got, _ := r.Get("p1::m"); got.ContextWindow != 65536 {
		t.Fatalf("Update 未生效: %+v", got)
	}
	// 落盘/重载后仍是同一条（provider 字段进磁盘）
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"provider": "p1"`) {
		t.Fatalf("provider 未落盘: %s", raw)
	}
	re, err := Load(path)
	if err != nil {
		t.Fatalf("重载: %v", err)
	}
	if got, ok := re.Get("p1::m"); !ok || got.ContextWindow != 65536 {
		t.Fatalf("重载后: %+v", got)
	}
	// 换主键（改 provider）按新键定位：旧键不存在 → 明确报错，不隐式搬家
	m.Provider = "p2"
	if err := r.Update(m); err == nil {
		t.Fatal("改 provider 等于换主键，旧键不该被更新")
	}
	// 纯 id 键（存量条目）照旧按 id 定位
	if err := r.Add(sampleModel("legacy")); err != nil {
		t.Fatal(err)
	}
	lm, _ := r.Get("legacy")
	lm.MaxOutputTokens = 4096
	if err := r.Update(lm); err != nil {
		t.Fatalf("存量条目 Update: %v", err)
	}
	if got, _ := r.Get("legacy"); got.MaxOutputTokens != 4096 {
		t.Fatalf("存量 Update 未生效: %+v", got)
	}
}

func TestDuplicateCompositeKeyInFileRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	body := `{"version":1,"models":[` +
		`{"id":"m","provider":"p","base_url":"http://a/v1","model":"m","enabled":true},` +
		`{"id":"m","provider":"p","base_url":"http://b/v1","model":"m","enabled":true}],` +
		`"roles":{}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("重复组合键应拒绝加载")
	}
}

func TestResolveDoesNotGuess(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.Add(providerModel("p1", "m")); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Resolve("p2::m"); ok {
		t.Fatal("provider 不匹配不应命中")
	}
	if _, ok := r.Resolve("ghost"); ok {
		t.Fatal("不存在的引用应返回 false")
	}
	// 组合键被引用但该 provider 下没有同名条目 → 不回落到别的 provider
	if err := r.Add(providerModel("p3", "other")); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Resolve("p3::m"); ok {
		t.Fatal("跨 provider 回落是猜，必须禁止")
	}
}

// 磁盘格式版本不变（新增可选字段对旧文件兼容）——这条钉住「不触发迁移」。
func TestFileVersionUnchanged(t *testing.T) {
	if fileVersion != 1 {
		t.Fatalf("fileVersion 应保持 1（新增可选 provider 字段不破坏旧文件）: %d", fileVersion)
	}
	var raw map[string]any
	b, _ := json.Marshal(ModelConfig{ID: "m", BaseURL: "http://a/v1", Model: "m"})
	_ = json.Unmarshal(b, &raw)
	if _, has := raw["provider"]; has {
		t.Fatal("无 provider 的条目不应写出 provider 字段（存量文件保持逐字）")
	}
}
