// 项目守则（项目根 AGENTS.md）的协议契约：只按项目 id 寻址，路径由服务端解析；
// 读不到是正常态（exists=false）不是错误；写入必须真的落盘。
package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/moyunteng/lxcode/internal/protocol"
)

func TestProjectInstructionsRoundtrip(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	dir := t.TempDir()
	resp := client.call(protocol.MethodProjectAdd, protocol.ProjectAddParams{Name: "docs-smoke", Path: dir})
	if resp.Error != nil {
		t.Fatalf("project.add 失败: %v", resp.Error)
	}
	var meta protocol.ProjectMeta
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatalf("解析 project.add 结果: %v", err)
	}

	decode := func(r *protocol.Response) protocol.ProjectInstructionsResult {
		t.Helper()
		if r.Error != nil {
			t.Fatalf("请求失败: %v", r.Error)
		}
		var out protocol.ProjectInstructionsResult
		raw, _ := json.Marshal(r.Result)
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("解析结果: %v", err)
		}
		return out
	}

	// 还没有守则文件：exists=false 且不报错（绝大多数项目没写过）
	got := decode(client.call(protocol.MethodProjectInstructionsGet, protocol.ProjectInstructionsParams{ProjectID: meta.ID}))
	if got.Exists {
		t.Fatalf("初始不该存在守则: %+v", got)
	}
	if got.Path != filepath.Join(dir, "AGENTS.md") {
		t.Fatalf("路径应由服务端解析为项目根下的 AGENTS.md: %s", got.Path)
	}

	// 写入 → 读回一致
	const body = "# 项目守则\n\n提交信息一律用中文。\n"
	saved := decode(client.call(protocol.MethodProjectInstructionsSave, protocol.ProjectInstructionsParams{ProjectID: meta.ID, Content: body}))
	if !saved.Exists || saved.Path != filepath.Join(dir, "AGENTS.md") {
		t.Fatalf("保存结果不符: %+v", saved)
	}
	got = decode(client.call(protocol.MethodProjectInstructionsGet, protocol.ProjectInstructionsParams{ProjectID: meta.ID}))
	if !got.Exists || got.Content != body {
		t.Fatalf("读回不一致: %+v", got)
	}

	// 真的落盘了（不是协议层回声）
	data, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("守则文件应真的存在: %v", err)
	}
	if string(data) != body {
		t.Fatalf("磁盘内容不符: %q", string(data))
	}

	// 覆盖写（已存在文件——Windows rename 语义的关键路径）
	const next = "第二版守则\n"
	decode(client.call(protocol.MethodProjectInstructionsSave, protocol.ProjectInstructionsParams{ProjectID: meta.ID, Content: next}))
	got = decode(client.call(protocol.MethodProjectInstructionsGet, protocol.ProjectInstructionsParams{ProjectID: meta.ID}))
	if got.Content != next {
		t.Fatalf("覆盖后读回不一致: %q", got.Content)
	}

	// 项目不存在 → 显式报错（不 panic、不凭空写文件）
	if r := client.call(protocol.MethodProjectInstructionsGet, protocol.ProjectInstructionsParams{ProjectID: "no-such-project"}); r.Error == nil {
		t.Fatal("项目不存在时读取应报错")
	}
	if r := client.call(protocol.MethodProjectInstructionsSave, protocol.ProjectInstructionsParams{ProjectID: "no-such-project", Content: "x"}); r.Error == nil {
		t.Fatal("项目不存在时保存应报错")
	}
	// 缺 project_id → 参数错误
	if r := client.call(protocol.MethodProjectInstructionsGet, protocol.ProjectInstructionsParams{}); r.Error == nil {
		t.Fatal("缺 project_id 应报参数错误")
	}
}
