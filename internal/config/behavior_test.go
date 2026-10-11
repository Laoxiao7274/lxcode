package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBehaviorDefaultOnMissing(t *testing.T) {
	b := LoadBehavior(filepath.Join(t.TempDir(), "behavior.json"))
	if b.TodoPrompt != TodoPromptStrict {
		t.Fatalf("文件缺失应回落默认 strict，got %q", b.TodoPrompt)
	}
}

func TestBehaviorRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "behavior.json")
	if err := SaveBehavior(path, Behavior{TodoPrompt: TodoPromptNormal}); err != nil {
		t.Fatalf("SaveBehavior: %v", err)
	}
	if b := LoadBehavior(path); b.TodoPrompt != TodoPromptNormal {
		t.Fatalf("往返不一致：want normal, got %q", b.TodoPrompt)
	}
	if err := SaveBehavior(path, Behavior{TodoPrompt: TodoPromptOff}); err != nil {
		t.Fatalf("SaveBehavior: %v", err)
	}
	if b := LoadBehavior(path); b.TodoPrompt != TodoPromptOff {
		t.Fatalf("往返不一致：want off, got %q", b.TodoPrompt)
	}
}

func TestBehaviorCorruptFallsBackToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "behavior.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b := LoadBehavior(path); b.TodoPrompt != TodoPromptStrict {
		t.Fatalf("损坏文件应回落默认 strict，got %q", b.TodoPrompt)
	}
}

func TestBehaviorInvalidFieldFallsBackToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "behavior.json")
	raw, _ := json.Marshal(map[string]string{"todoPrompt": "aggressive"})
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if b := LoadBehavior(path); b.TodoPrompt != TodoPromptStrict {
		t.Fatalf("非法档位应回落默认 strict，got %q", b.TodoPrompt)
	}
}

func TestSaveBehaviorNormalizesEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "behavior.json")
	if err := SaveBehavior(path, Behavior{}); err != nil {
		t.Fatalf("SaveBehavior: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var b Behavior
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	if b.TodoPrompt != TodoPromptStrict {
		t.Fatalf("空值落盘应归一为 strict，got %q", b.TodoPrompt)
	}
}

// 热加载语义：读侧每次现读文件——外部（设置页 set 或手改）改完磁盘，
// 下一次 LoadBehavior（= 下一轮 chat.send）就是新值，不需要重启或重开会话。
func TestBehaviorHotReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "behavior.json")
	if err := SaveBehavior(path, Behavior{TodoPrompt: TodoPromptStrict}); err != nil {
		t.Fatal(err)
	}
	if b := LoadBehavior(path); b.TodoPrompt != TodoPromptStrict {
		t.Fatalf("初始应为 strict，got %q", b.TodoPrompt)
	}
	// 模拟手改磁盘（不经 SaveBehavior——reloadLoop 检测的就是这种）
	if err := os.WriteFile(path, []byte(`{"todoPrompt":"off"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if b := LoadBehavior(path); b.TodoPrompt != TodoPromptOff {
		t.Fatalf("热加载后应为 off，got %q", b.TodoPrompt)
	}
}
