package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAssistantNormalizedTrimsAndDropsEmptySkills(t *testing.T) {
	a := Assistant{Name: " 小灵 ", Persona: "  轻松幽默  ", Workflow: " research-first ",
		Skills: []string{"gsap", " ", "frontend-design", ""},
	}.Normalized()
	if a.Name != "小灵" || a.Persona != "轻松幽默" || a.Workflow != "research-first" {
		t.Fatalf("字符串字段应去首尾空白: %+v", a)
	}
	if len(a.Skills) != 2 || a.Skills[0] != "gsap" || a.Skills[1] != "frontend-design" {
		t.Fatalf("空技能项应被丢掉: %v", a.Skills)
	}
}

func TestAssistantNormalizedAllEmptySkillsNil(t *testing.T) {
	a := Assistant{Skills: []string{"", "  "}}.Normalized()
	if a.Skills != nil {
		t.Fatalf("全空技能应落 nil（磁盘文件不带空数组）: %v", a.Skills)
	}
}

func TestAssistantNormalizedMCPServers(t *testing.T) {
	a := Assistant{MCPServers: []string{" exa ", "", "filesystem"}}.Normalized()
	if len(a.MCPServers) != 2 || a.MCPServers[0] != "exa" || a.MCPServers[1] != "filesystem" {
		t.Fatalf("mcpServers 应逐项去空白并丢空项: %v", a.MCPServers)
	}
	if a2 := (Assistant{MCPServers: []string{"", "  "}}.Normalized()); a2.MCPServers != nil {
		t.Fatalf("全空 mcpServers 应落 nil: %v", a2.MCPServers)
	}
}

func TestAssistantNormalizedTools(t *testing.T) {
	a := Assistant{Tools: []string{" my_query ", "", "repo-stats"}}.Normalized()
	if len(a.Tools) != 2 || a.Tools[0] != "my_query" || a.Tools[1] != "repo-stats" {
		t.Fatalf("tools 应逐项去空白并丢空项: %v", a.Tools)
	}
	if a2 := (Assistant{Tools: []string{"", "  "}}.Normalized()); a2.Tools != nil {
		t.Fatalf("全空 tools 应落 nil: %v", a2.Tools)
	}
}

func TestAssistantToolsRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assistant.json")
	in := Assistant{Name: "小灵", Tools: []string{"my_query", "repo-stats"}}
	if err := SaveAssistant(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out := LoadAssistant(path)
	if len(out.Tools) != 2 || out.Tools[0] != "my_query" || out.Tools[1] != "repo-stats" {
		t.Fatalf("tools 往返不一致: %+v", out)
	}
	// 键名落盘为 tools（omitempty——未绑定的存量文件不出现该键）
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, has := raw["tools"]; !has {
		t.Fatalf("tools 键未落盘: %s", data)
	}
	if err := SaveAssistant(path, Assistant{Name: "小灵"}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = map[string]any{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, has := raw["tools"]; has {
		t.Fatalf("空 tools 不应落盘键: %s", data)
	}
}

func TestAssistantMCPServersRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assistant.json")
	in := Assistant{Name: "小灵", MCPServers: []string{"exa", "filesystem"}}
	if err := SaveAssistant(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out := LoadAssistant(path)
	if len(out.MCPServers) != 2 || out.MCPServers[0] != "exa" || out.MCPServers[1] != "filesystem" {
		t.Fatalf("mcpServers 往返不一致: %+v", out)
	}
	// 键名落盘为 mcpServers（omitempty——不绑定的存量文件不出现该键）
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, has := raw["mcpServers"]; !has {
		t.Fatalf("mcpServers 键未落盘: %s", data)
	}
	if err := SaveAssistant(path, Assistant{Name: "小灵"}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = map[string]any{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, has := raw["mcpServers"]; has {
		t.Fatalf("空 mcpServers 不应落盘键: %s", data)
	}
}

func assistantZero(a Assistant) bool {
	return a.Name == "" && a.Persona == "" && a.Workflow == "" && len(a.Skills) == 0 && len(a.MCPServers) == 0 && len(a.Tools) == 0
}

func TestAssistantLoadMissingFile(t *testing.T) {
	a := LoadAssistant(filepath.Join(t.TempDir(), "assistant.json"))
	if !assistantZero(a) {
		t.Fatalf("缺失文件应回落零值: %+v", a)
	}
}

func TestAssistantLoadBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assistant.json")
	if err := os.WriteFile(path, []byte("{不是 JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !assistantZero(LoadAssistant(path)) {
		t.Fatalf("坏文件应回落零值: %+v", LoadAssistant(path))
	}
}

func TestAssistantSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assistant.json")
	in := Assistant{Name: "小灵", Persona: "轻松幽默，回答简洁", Skills: []string{"gsap", "frontend-design"}, Workflow: "research-first"}
	if err := SaveAssistant(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out := LoadAssistant(path)
	if out.Name != in.Name || out.Persona != in.Persona || out.Workflow != in.Workflow ||
		len(out.Skills) != len(in.Skills) || (len(in.Skills) > 0 && out.Skills[0] != in.Skills[0]) {
		t.Fatalf("往返不一致: in %+v out %+v", in, out)
	}
}

func TestAssistantSaveNormalizesOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assistant.json")
	if err := SaveAssistant(path, Assistant{Name: " 小灵 ", Skills: []string{"gsap", " "}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["name"] != "小灵" {
		t.Fatalf("落盘应已归一: %s", data)
	}
	if _, has := raw["workflow"]; has {
		t.Fatalf("空字段不应落盘键: %s", data)
	}
}
