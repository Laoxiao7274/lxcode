package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestRegistry 构造一个落在临时目录里的空注册表（config/ 目录尚不存在，
// 覆盖"首次运行 + 父目录缺失"路径）。
func newTestRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config", "models.json")
	r, err := Load(path)
	if err != nil {
		t.Fatalf("Load 空注册表: %v", err)
	}
	return r, path
}

func sampleModel(id string) ModelConfig {
	return ModelConfig{
		ID:              id,
		BaseURL:         "http://127.0.0.1:8080/v1",
		Model:           id + "-q4_k_m",
		ContextWindow:   32768,
		MaxOutputTokens: 8192,
		Enabled:         true,
	}
}

func TestCRUD(t *testing.T) {
	r, path := newTestRegistry(t)

	if err := r.Add(sampleModel("a")); err != nil {
		t.Fatalf("Add a: %v", err)
	}
	if err := r.Add(sampleModel("b")); err != nil {
		t.Fatalf("Add b: %v", err)
	}

	got := r.List()
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("List 顺序不符（应按添加顺序）: %+v", got)
	}

	m, ok := r.Get("a")
	if !ok || m.Model != "a-q4_k_m" {
		t.Fatalf("Get a: %+v ok=%v", m, ok)
	}
	if _, ok := r.Get("ghost"); ok {
		t.Fatal("Get 不存在的模型应返回 false")
	}

	// 删除中间条目后顺序保持稳定
	if err := r.Remove("a"); err != nil {
		t.Fatalf("Remove a: %v", err)
	}
	got = r.List()
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("删除后 List 不符: %+v", got)
	}

	upd := sampleModel("b")
	upd.DisplayName = "B 改名"
	upd.Enabled = false
	if err := r.Update(upd); err != nil {
		t.Fatalf("Update b: %v", err)
	}
	if m, _ := r.Get("b"); m.DisplayName != "B 改名" || m.Enabled {
		t.Fatalf("Update 未生效: %+v", m)
	}
	if err := r.Update(sampleModel("ghost")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新不存在的条目应报 ErrNotFound, got %v", err)
	}

	// 变更即落盘：重载后状态保持
	r2, err := Load(path)
	if err != nil {
		t.Fatalf("重载: %v", err)
	}
	if m, ok := r2.Get("b"); !ok || m.DisplayName != "B 改名" || m.Enabled {
		t.Fatalf("持久化丢失: %+v ok=%v", m, ok)
	}
	if _, ok := r2.Get("a"); ok {
		t.Fatal("已删除的 a 不应持久化")
	}
}

func TestAddDuplicateID(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.Add(sampleModel("dup")); err != nil {
		t.Fatalf("首次 Add: %v", err)
	}
	if err := r.Add(sampleModel("dup")); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("重复 Add 应报 ErrDuplicateID, got %v", err)
	}
}

func TestValidate(t *testing.T) {
	r, _ := newTestRegistry(t)
	cases := []struct {
		name string
		mut  func(*ModelConfig)
		want string // 错误信息须包含的子串
	}{
		{"id 为空", func(m *ModelConfig) { m.ID = "" }, "id"},
		{"model 为空", func(m *ModelConfig) { m.Model = "" }, "model"},
		{"base_url 无 scheme", func(m *ModelConfig) { m.BaseURL = "127.0.0.1:8080" }, "base_url"},
		{"base_url 非 http 协议", func(m *ModelConfig) { m.BaseURL = "ftp://x/v1" }, "base_url"},
		{"base_url 无 host", func(m *ModelConfig) { m.BaseURL = "http://" }, "base_url"},
		{"format 非法", func(m *ModelConfig) { m.Format = "gemini" }, "format"},
		{"max_output 等于 context_window", func(m *ModelConfig) { m.MaxOutputTokens = 32768 }, "context_window"},
		{"context_window 为负", func(m *ModelConfig) { m.ContextWindow = -1 }, "负数"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := sampleModel("v")
			c.mut(&m)
			err := r.Add(m)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Add 应报含 %q 的错误, got %v", c.want, err)
			}
			if len(r.List()) != 0 {
				t.Fatal("校验失败的 Add 不应留下状态")
			}
		})
	}
}

func TestFormatNormalize(t *testing.T) {
	r, _ := newTestRegistry(t)
	m := sampleModel("fmt")
	m.Format = "" // 空 = openai，落盘归一化
	if err := r.Add(m); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, _ := r.Get("fmt")
	if got.Format != FormatOpenAI {
		t.Fatalf("空 format 应归一为 openai, got %q", got.Format)
	}
}

func TestRemoveRoleReferenced(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.Add(sampleModel("m1")); err != nil {
		t.Fatal(err)
	}
	if err := r.SetRole(RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	err := r.Remove("m1")
	if !errors.Is(err, ErrRoleInUse) {
		t.Fatalf("被角色引用时删除应报 ErrRoleInUse, got %v", err)
	}
	if !strings.Contains(err.Error(), RoleDefault) {
		t.Fatalf("错误应包含引用角色名: %v", err)
	}
	// 解绑后可删
	if err := r.SetRole(RoleDefault, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove("m1"); err != nil {
		t.Fatalf("解绑后删除: %v", err)
	}
}

func TestSetRole(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.Add(sampleModel("m1")); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(sampleModel("m2")); err != nil {
		t.Fatal(err)
	}

	if err := r.SetRole("chat", "m1"); !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("未知角色应报 ErrUnknownRole, got %v", err)
	}
	if err := r.SetRole(RoleVision, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("绑定不存在的模型应报 ErrNotFound, got %v", err)
	}

	disabled := sampleModel("m3")
	disabled.Enabled = false
	if err := r.Add(disabled); err != nil {
		t.Fatal(err)
	}
	if err := r.SetRole(RoleVision, "m3"); err == nil || !strings.Contains(err.Error(), "禁用") {
		t.Fatalf("禁用模型绑定角色应报错, got %v", err)
	}

	if err := r.SetRole(RoleVision, "m2"); err != nil {
		t.Fatalf("正常绑定: %v", err)
	}
	m, err := r.ModelForRole(RoleVision)
	if err != nil || m.ID != "m2" {
		t.Fatalf("ModelForRole: %+v err=%v", m, err)
	}

	// 解绑
	if err := r.SetRole(RoleVision, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ModelForRole(RoleVision); err == nil || !strings.Contains(err.Error(), "未绑定") {
		t.Fatalf("解绑后 ModelForRole 应报未绑定, got %v", err)
	}
	if _, err := r.ModelForRole("bogus"); !errors.Is(err, ErrUnknownRole) {
		t.Fatalf("未知角色应报 ErrUnknownRole, got %v", err)
	}
}

func TestRoleBindingsRoundTrip(t *testing.T) {
	r, path := newTestRegistry(t)
	if err := r.Add(sampleModel("m1")); err != nil {
		t.Fatal(err)
	}
	if err := r.SetRole(RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	r2, err := Load(path)
	if err != nil {
		t.Fatalf("重载: %v", err)
	}
	b := r2.RoleBindings()
	if b[RoleDefault] != "m1" || b[RoleVision] != "" {
		t.Fatalf("角色绑定 round-trip 不符: %+v", b)
	}
}

func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-dir", "models.json")
	r, err := Load(path)
	if err != nil {
		t.Fatalf("文件不存在应返回空注册表: %v", err)
	}
	if len(r.List()) != 0 {
		t.Fatal("应为空注册表")
	}
}

func TestLoadCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("坏 JSON 应报错, got %v", err)
	}
}

func TestLoadBadVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"models":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "版本") {
		t.Fatalf("版本不符应报错, got %v", err)
	}
}

func TestLoadRoleDangling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	body := `{"version":1,"models":[{"id":"m","base_url":"http://x/v1","model":"m","enabled":true}],"roles":{"default":"ghost"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("悬空角色引用应报错并指出模型 id, got %v", err)
	}
}

func TestLoadDuplicateID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	body := `{"version":1,"models":[{"id":"m","base_url":"http://x/v1","model":"m"},{"id":"m","base_url":"http://x/v1","model":"m"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("重复 id 应报错, got %v", err)
	}
}

func TestAtomicWriteNoLeftovers(t *testing.T) {
	r, path := newTestRegistry(t)
	for i := 0; i < 5; i++ {
		if err := r.Add(sampleModel(fmt.Sprintf("m%d", i))); err != nil {
			t.Fatalf("Add m%d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}
	// 落盘内容是带版本字段的合法 JSON
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f registryFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("落盘内容不是合法 JSON: %v", err)
	}
	if f.Version != fileVersion || len(f.Models) != 5 {
		t.Fatalf("落盘内容不符: version=%d models=%d", f.Version, len(f.Models))
	}
}
