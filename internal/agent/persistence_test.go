package agent

import (
	"errors"
	"reflect"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// 仅实现测试用例消费的方法；未预期调用会直接失败，避免无意义的假成功。
type persistenceStub struct {
	Persistence
	latestErr    error
	workspaceErr error
	projectErr   error
	dir          string
}

func (p *persistenceStub) Latest() (string, []llm.Message, error) {
	return "saved", []llm.Message{{Role: "user", Content: "saved"}}, p.latestErr
}
func (p *persistenceStub) Load(string) ([]llm.Message, error) {
	return []llm.Message{{Role: "user", Content: "saved"}}, nil
}
func (p *persistenceStub) WorkspaceOf(string) (string, error) { return "project", p.workspaceErr }
func (p *persistenceStub) ProjectByID(string) (sessiondata.ProjectMeta, bool, error) {
	return sessiondata.ProjectMeta{ID: "project", Path: p.dir}, true, p.projectErr
}

func TestSwitchFailuresPreserveWholeSession(t *testing.T) {
	for _, kind := range []string{"lookup", "workspace", "missingDirectory"} {
		t.Run(kind, func(t *testing.T) {
			p := &persistenceStub{dir: t.TempDir()}
			switch kind {
			case "lookup":
				p.projectErr = errors.New("database offline")
			case "workspace":
				p.workspaceErr = errors.New("workspace unavailable")
			case "missingDirectory":
				p.dir = t.TempDir() + "/missing"
			}
			s := New(nil, tools.New(), nil)
			s.st = p
			s.id = "original"
			s.history = []llm.Message{{Role: "user", Content: "original"}}
			s.workDir = "original-dir"
			s.pendingWorkspace = "original-project"
			s.todos = []tools.TodoItem{{Content: "original todo", Status: "active"}}
			before := s.History()
			if err := s.SwitchTo("saved"); err == nil {
				t.Fatal("expected failed restore")
			}
			if !reflect.DeepEqual(before, s.History()) || s.WorkDir() != "original-dir" || s.pendingWorkspace != "original-project" {
				t.Fatal("failed restore changed session")
			}
			if kind != "workspace" {
				if _, err := s.SwitchNew("project"); err == nil {
					t.Fatal("expected failed new")
				}
				if !reflect.DeepEqual(before, s.History()) || s.WorkDir() != "original-dir" {
					t.Fatal("failed new changed session")
				}
			}
		})
	}
}

func TestPersistenceAttachFailureCanRetry(t *testing.T) {
	p := &persistenceStub{dir: t.TempDir(), latestErr: errors.New("offline")}
	s := New(nil, tools.New(), nil)
	if err := s.EnablePersistence(p); err == nil {
		t.Fatal("expected failure")
	}
	if s.st != nil || s.SessionID() != "" {
		t.Fatal("failed attach published state")
	}
	p.latestErr = nil
	if err := s.EnablePersistence(p); err != nil {
		t.Fatal(err)
	}
	if s.SessionID() != "saved" || s.WorkDir() != p.dir {
		t.Fatal("retry did not restore")
	}
}

func TestSwitchClearsTodoAndGuardsBusy(t *testing.T) {
	p := &persistenceStub{dir: t.TempDir()}
	s := New(nil, tools.New(), nil)
	s.st = p
	s.todos = []tools.TodoItem{{Content: "old", Status: "active"}}
	s.busy = true
	if _, err := s.SwitchNew("project"); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy new: %v", err)
	}
	if err := s.SwitchTo("saved"); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy resume: %v", err)
	}
	s.busy = false
	if err := s.SwitchTo("saved"); err != nil {
		t.Fatal(err)
	}
	if len(s.History().Todos) != 0 {
		t.Fatal("resume leaked previous todos")
	}
}
