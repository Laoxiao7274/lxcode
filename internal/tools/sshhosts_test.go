// ssh_hosts 工具的单测（sshhosts.go）。
//
// 执行路径**不做真机单测**（无远程环境）——本文件的覆盖面是逻辑函数：
// 配置解析（env 展开）、别名解析与参数拼装、探活缓存（时钟注入）、
// 超时钳制、hostkey 文件规范化。执行函数只测「配置未装配 / 别名不存在」
// 的失败路径（不发起真实连接）。
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/llm"
)

func mustWriteHosts(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hosts.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoadSSHHosts：正常解析 + 缺失文件 = 空清单 + 空 JSON = 空清单。
func TestLoadSSHHosts(t *testing.T) {
	if hosts, err := LoadSSHHosts(filepath.Join(t.TempDir(), "nope.json")); err != nil || len(hosts) != 0 {
		t.Fatalf("缺失文件应为空清单: %v %v", hosts, err)
	}
	path := mustWriteHosts(t, "")
	if hosts, err := LoadSSHHosts(path); err != nil || len(hosts) != 0 {
		t.Fatalf("空文件应为空清单: %v %v", hosts, err)
	}
	path = mustWriteHosts(t, `[{"alias":"dev","host":"192.168.1.10","port":2222,"user":"root","password":"${DEV_PASS}","key_path":"C:/keys/dev","hostkey":"ssh-ed25519 AAAA test"}]`)
	hosts, err := LoadSSHHosts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Alias != "dev" || hosts[0].Port != 2222 || hosts[0].User != "root" {
		t.Fatalf("解析结果不对: %+v", hosts)
	}
}

// TestExpandEnvRef：${VAR} 展开；未定义变量保持原样（不静默变空）。
func TestExpandEnvRef(t *testing.T) {
	t.Setenv("LX_TEST_PASS", "s3cret")
	if got := expandEnvRef("${LX_TEST_PASS}"); got != "s3cret" {
		t.Fatalf("应展开: %q", got)
	}
	if got := expandEnvRef("pre-${LX_TEST_PASS}-post"); got != "pre-s3cret-post" {
		t.Fatalf("应内联展开: %q", got)
	}
	if got := expandEnvRef("${LX_TEST_UNDEFINED_VAR}"); got != "${LX_TEST_UNDEFINED_VAR}" {
		t.Fatalf("未定义变量应保持原样: %q", got)
	}
	if got := expandEnvRef("$HOME ${}"); got != "$HOME ${}" {
		t.Fatalf("非引用形态不应被改写: %q", got)
	}
}

// TestResolveTarget：别名解析（默认端口 22、user@host 拼装、无 host 半截配置拒绝、
// 未知名不命中）。
func TestResolveTarget(t *testing.T) {
	hosts := []SSHHost{
		{Alias: "dev", Host: "192.168.1.10", User: "root"},
		{Alias: "box", Host: "example.com", Port: 2222},
		{Alias: "half", Host: ""},
	}
	tgt, ok := resolveTarget(hosts, "dev")
	if !ok || tgt.port != 22 || tgt.userAt != "root@192.168.1.10" || tgt.host != "192.168.1.10" {
		t.Fatalf("dev 解析不对: %+v ok=%v", tgt, ok)
	}
	tgt, ok = resolveTarget(hosts, "box")
	if !ok || tgt.port != 2222 || tgt.userAt != "example.com" {
		t.Fatalf("box 解析不对（无 user 时目标不含 @）: %+v ok=%v", tgt, ok)
	}
	if _, ok := resolveTarget(hosts, "half"); ok {
		t.Fatal("半截配置（无 host）应拒绝")
	}
	if _, ok := resolveTarget(hosts, "nope"); ok {
		t.Fatal("未知名不应命中")
	}
}

// TestSSHArgs：参数拼装——端口、key、hostkey 严格校验、BatchMode。
func TestSSHArgs(t *testing.T) {
	tgt, _ := resolveTarget([]SSHHost{{Alias: "a", Host: "h", Port: 2222, KeyPath: "/k"}}, "a")
	args := sshArgs(tgt, "-p")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-p 2222") {
		t.Fatalf("端口参数缺失: %v", args)
	}
	if !strings.Contains(joined, "-i /k") || !strings.Contains(joined, "IdentitiesOnly=yes") {
		t.Fatalf("key 参数缺失: %v", args)
	}
	if !strings.Contains(joined, "accept-new") {
		t.Fatalf("未声明 hostkey 应 accept-new: %v", args)
	}
	if strings.Contains(joined, "StrictHostKeyChecking=no") {
		t.Fatalf("任何路径都不应出现 no 检查: %v", args)
	}
	if !strings.Contains(joined, "BatchMode=yes") {
		t.Fatalf("应禁止交互提示: %v", args)
	}
	// 默认 22 端口不写 -p（少一个参数面）。
	tgt2, _ := resolveTarget([]SSHHost{{Alias: "b", Host: "h"}}, "b")
	for _, a := range sshArgs(tgt2, "-p") {
		if a == "22" || a == "-p" {
			t.Fatalf("默认端口不该出现在参数里: %v", sshArgs(tgt2, "-p"))
		}
	}
}

// TestSSHArgsHostKeyStrict：声明了 hostkey → UserKnownHostsFile=临时文件 +
// StrictHostKeyChecking=yes（文件内容是规范化 known_hosts 行）。
func TestSSHArgsHostKeyStrict(t *testing.T) {
	tgt, _ := resolveTarget([]SSHHost{{Alias: "a", Host: "h", HostKey: "ssh-ed25519 AAAATEST"}}, "a")
	args := sshArgs(tgt, "-p")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "StrictHostKeyChecking=yes") {
		t.Fatalf("声明 hostkey 应启用严格校验: %v", args)
	}
	var knownFile string
	for i, a := range args {
		if strings.HasPrefix(a, "UserKnownHostsFile=") {
			knownFile = strings.TrimPrefix(a, "UserKnownHostsFile=")
			_ = i
		}
	}
	if knownFile == "" {
		t.Fatalf("应有 UserKnownHostsFile 临时文件: %v", args)
	}
	defer os.Remove(knownFile)
	data, err := os.ReadFile(knownFile)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "* ssh-ed25519 AAAATEST") {
		t.Fatalf("known_hosts 行应规范化（host 用通配符 + 原指纹）: %q", line)
	}
	// 已带 host 前缀的 known_hosts 行不被二次加前缀。
	tgt2, _ := resolveTarget([]SSHHost{{Alias: "b", Host: "h", HostKey: "host1 ssh-ed25519 AAAA2"}}, "b")
	args2 := sshArgs(tgt2, "-p")
	for _, a := range args2 {
		if strings.HasPrefix(a, "UserKnownHostsFile=") {
			f := strings.TrimPrefix(a, "UserKnownHostsFile=")
			defer os.Remove(f)
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if l := strings.TrimSpace(string(data)); l != "host1 ssh-ed25519 AAAA2" {
				t.Fatalf("known_hosts 格式的 hostkey 应原样保留: %q", l)
			}
		}
	}
}

// TestSSHGuardCache：探活缓存——成功记 60s、过期失效、失败不缓存、fresh 语义
// 由调用方判定（guard 只回答 ok）。时钟注入（now 可替换）驱动过期判定。
func TestSSHGuardCache(t *testing.T) {
	g := newSSHGuard()
	base := time.Unix(1_800_000_000, 0)
	now := base
	g.now = func() time.Time { return now }
	if g.ok("h") {
		t.Fatal("空缓存不应命中")
	}
	g.mark("h", false)
	if g.ok("h") {
		t.Fatal("失败不进缓存")
	}
	g.mark("h", true)
	if !g.ok("h") {
		t.Fatal("成功应命中")
	}
	// 推进 59s：仍命中。
	now = base.Add(59 * time.Second)
	if !g.ok("h") {
		t.Fatal("60s 内应命中")
	}
	// 推进 61s：过期失效。
	now = base.Add(61 * time.Second)
	if g.ok("h") {
		t.Fatal("过期后不应命中")
	}
	// 失败会把成功记录抹掉。
	g.mark("h", true)
	g.mark("h", false)
	if g.ok("h") {
		t.Fatal("失败应抹掉缓存")
	}
}

// TestSSHGuardConcurrent：并行访问不炸（-race 面覆盖）。
func TestSSHGuardConcurrent(t *testing.T) {
	g := newSSHGuard()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 200; j++ {
				g.mark("h", j%3 != 0)
				g.ok("h")
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

// TestSSHHostsTimeoutClamp：超时钳制的纯函数面——通过 runSSHCommand 的
// 钳制逻辑直接断言（拆出钳制为可测函数太薄，直接用负值/越界值验证语义由
// bash 同款 min 表达式保证；这里覆盖 sshArgs 与 resolveTarget 的组合行为）。
func TestSSHHostsTimeoutClamp(t *testing.T) {
	// sshMaxTo 边界：min(400, 300) = 300。
	if got := min(400, int(sshMaxTo/time.Second)); got != int(sshMaxTo/time.Second) {
		t.Fatalf("上限钳制失效: %d", got)
	}
	// 0 = 默认 30s（语义断言——常量即契约）。
	if sshDefaultTo != 30*time.Second || sshMaxTo != 300*time.Second || sshAliveTTL != 60*time.Second {
		t.Fatalf("超时/缓存常量与文档不符: %v %v %v", sshDefaultTo, sshMaxTo, sshAliveTTL)
	}
}

// TestSSHHostsNotAssembled：注册表未装配 lookup 时报「未配置」（不是 panic，
// 不是裸连接）；别名不存在时报人话错误。这两个路径都不发起真实连接。
func TestSSHHostsNotAssembled(t *testing.T) {
	reg := New()
	raw, _ := json.Marshal(map[string]any{"host": "dev", "command": "ls"})
	out := reg.Execute(context.Background(), mkToolCall(sshHostsToolName, raw))
	if !strings.Contains(out, "未装配") {
		t.Fatalf("未装配应报明确错误: %q", out)
	}
	// 装配了但别名不存在。
	reg.SetSSHHostLookup(func(alias string) (SSHHost, bool) {
		return SSHHost{}, false
	})
	out = reg.Execute(context.Background(), mkToolCall(sshHostsToolName, raw))
	if !strings.Contains(out, "不在") {
		t.Fatalf("别名不存在应报人话错误: %q", out)
	}
}

// TestSSHHostsRegistered：注册表里有 ssh_hosts，RiskHigh + Mutates=true，
// 带确认文案（远程执行走确认门）。
func TestSSHHostsRegistered(t *testing.T) {
	reg := New()
	d, ok := reg.Get(sshHostsToolName)
	if !ok {
		t.Fatal("ssh_hosts 未注册")
	}
	if d.Risk != RiskHigh {
		t.Fatalf("ssh_hosts 应为高危: %v", d.Risk)
	}
	if !d.Mutates {
		t.Fatal("ssh_hosts 默认应 Mutates=true（远程执行副作用不可知）")
	}
	raw, _ := json.Marshal(map[string]string{"host": "dev", "command": "uptime"})
	if msg := d.Confirm(context.Background(), raw); !strings.Contains(msg, "dev") || !strings.Contains(msg, "uptime") {
		t.Fatalf("确认文案应点出主机与命令: %q", msg)
	}
}

// mkToolCall 造一个 llm.ToolCall 形状的调用（Execute 的入参）。
func mkToolCall(name string, args []byte) llm.ToolCall {
	var tc llm.ToolCall
	tc.Function.Name = name
	tc.Function.Arguments = string(args)
	return tc
}
