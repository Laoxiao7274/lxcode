// approval_persist_test.go —— 审批档持久化的内核侧契约：
//   - SetApproval 走 Persistence 接缝落库（规范化的值，不是原样入参）；
//   - AttachTo 恢复落库值进内存（LiveApproval 读得到）；
//   - 空列 = 没落过，不编档位（LiveApproval 回落 confirm 缺省）；
//   - 纯内存模式（st=nil）落库是 no-op，改档照常生效。
package agent

import (
	"errors"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// approvalStub 记录 SaveApproval 的调用（其余 Persistence 方法内嵌 nil——
// 本文件只走审批档相关路径，未预期调用 panic 即失败）。
type approvalStub struct {
	Persistence
	saved    map[string]string
	loadMode string // ApprovalOf 返回值（空串 = 没落过）
}

func (p *approvalStub) Load(string) ([]llm.Message, error) { return nil, nil }
func (p *approvalStub) WorkspaceOf(string) (string, error) { return "", nil }
func (p *approvalStub) ProjectByID(string) (sessiondata.ProjectMeta, bool, error) {
	return sessiondata.ProjectMeta{}, false, nil
}
func (p *approvalStub) ContextUsageOf(string) (sessiondata.ContextUsage, bool, error) {
	return sessiondata.ContextUsage{}, false, nil
}
func (p *approvalStub) SaveContextUsage(string, sessiondata.ContextUsage) error { return nil }
func (p *approvalStub) ApprovalOf(string) (string, error)                       { return p.loadMode, nil }
func (p *approvalStub) SaveApproval(id, approval string) error {
	if p.saved == nil {
		p.saved = map[string]string{}
	}
	p.saved[id] = approval
	return nil
}

func TestSetApprovalPersistsViaSeam(t *testing.T) {
	p := &approvalStub{}
	s := New(nil, tools.New(), nil)
	s.st = p
	s.id = "sess-1"
	if got := s.SetApproval("strict"); got != "strict" {
		t.Fatalf("SetApproval 应返回规范化档位，got %q", got)
	}
	if p.saved["sess-1"] != "strict" {
		t.Fatalf("改档应落库，saved=%v", p.saved)
	}
	// 空串 = confirm：落库的是**规范化后**的值（回读侧不用再猜）。
	s.SetApproval("")
	if p.saved["sess-1"] != string(tools.ApprovalConfirm) {
		t.Fatalf("空串应规范化成 confirm 落库，saved=%v", p.saved)
	}
}

func TestSetApprovalPersistFailureIsNotFatal(t *testing.T) {
	p := &failingApprovalStub{}
	s := New(nil, tools.New(), nil)
	s.st = p
	s.id = "sess-1"
	if got := s.SetApproval("strict"); got != "strict" {
		t.Fatalf("落库失败不该让改档失败，got %q", got)
	}
	if got := s.LiveApproval(); got != "strict" {
		t.Fatalf("内存态应已生效，got %q", got)
	}
}

type failingApprovalStub struct{ approvalStub }

func (p *failingApprovalStub) SaveApproval(string, string) error { return errors.New("disk offline") }

func TestAttachToRestoresApproval(t *testing.T) {
	p := &approvalStub{loadMode: "strict"}
	s := New(nil, tools.New(), nil)
	if err := s.AttachTo(p, "sess-1"); err != nil {
		t.Fatal(err)
	}
	if got := s.LiveApproval(); got != "strict" {
		t.Fatalf("附着后应恢复落库的审批档，got %q", got)
	}
}

func TestAttachToEmptyColumnKeepsDefault(t *testing.T) {
	// 空列 = 没落过：不编档位——LiveApproval 按 confirm 缺省语义回落，
	// behavior 默认档的回落是 server 侧 newRuntime 的职责（agent 不读配置）。
	p := &approvalStub{}
	s := New(nil, tools.New(), nil)
	if err := s.AttachTo(p, "sess-1"); err != nil {
		t.Fatal(err)
	}
	if got := s.LiveApproval(); got != string(tools.ApprovalConfirm) {
		t.Fatalf("空列应回落 confirm 缺省，got %q", got)
	}
}

func TestSetApprovalWithoutPersistenceIsNoOp(t *testing.T) {
	s := New(nil, tools.New(), nil)
	if got := s.SetApproval("strict"); got != "strict" {
		t.Fatalf("纯内存模式改档应生效，got %q", got)
	}
	if got := s.LiveApproval(); got != "strict" {
		t.Fatalf("纯内存模式 LiveApproval 应生效，got %q", got)
	}
}
