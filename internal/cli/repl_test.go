package cli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/moyunteng/lxcode/internal/protocol"
)

type cliCall struct {
	method string
	params any
}

type fakeBackend struct {
	calls   []cliCall
	replies map[string]any
	events  chan protocol.Response
}

func (f *fakeBackend) Call(_ context.Context, method string, params any, result any) error {
	f.calls = append(f.calls, cliCall{method: method, params: params})
	if result == nil {
		return nil
	}
	data, err := json.Marshal(f.replies[method])
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}

func (f *fakeBackend) Events() <-chan protocol.Response { return f.events }
func (f *fakeBackend) Close()                           {}

func TestOpenInitialSessionResumesLatestNonArchived(t *testing.T) {
	be := &fakeBackend{
		replies: map[string]any{
			protocol.MethodSessionList: []protocol.SessionMeta{
				{ID: "archived", Messages: 8, Archived: true},
				{ID: "latest", Messages: 3},
				{ID: "older", Messages: 2},
			},
		},
		events: make(chan protocol.Response),
	}
	r := New(be)
	if err := r.openInitialSession(); err != nil {
		t.Fatal(err)
	}
	if got := r.currentSessionID(); got != "latest" {
		t.Fatalf("selected session = %q, want latest", got)
	}
	if got := be.calls[1]; got.method != protocol.MethodSessionResume || got.params != (protocol.SessionResumeParams{ID: "latest"}) {
		t.Fatalf("resume call = %#v", got)
	}
}

func TestNewSessionSwitchesFocusWhilePreviousSessionBusy(t *testing.T) {
	be := &fakeBackend{
		replies: map[string]any{
			protocol.MethodSessionNew:  protocol.SessionResult{SessionID: "new-session"},
			protocol.MethodChatHistory: protocol.ChatHistoryResult{SessionID: "new-session"},
		},
		events: make(chan protocol.Response),
	}
	r := New(be)
	r.session = "old-session"
	r.busy = true
	if quit := r.runCommand("/new"); quit {
		t.Fatal("/new unexpectedly quit")
	}
	if got := r.currentSessionID(); got != "new-session" {
		t.Fatalf("focused session = %q, want new-session", got)
	}
	if r.isBusy() {
		t.Fatal("new focused session inherited previous session busy state")
	}
	if got := be.calls[0]; got.method != protocol.MethodSessionNew {
		t.Fatalf("first call = %#v", got)
	}
	if got := be.calls[1]; got.method != protocol.MethodChatHistory || got.params != (protocol.ChatHistoryParams{SessionID: "new-session"}) {
		t.Fatalf("history call = %#v", got)
	}
}

func TestChatOperationsCarryFocusedSession(t *testing.T) {
	be := &fakeBackend{
		replies: map[string]any{
			protocol.MethodChatHistory: protocol.ChatHistoryResult{SessionID: "s1"},
		},
		events: make(chan protocol.Response),
	}
	r := New(be)
	r.session = "s1"
	if err := r.send("hello"); err != nil {
		t.Fatal(err)
	}
	r.confirm("confirm-1", true)
	if err := r.syncHistory(); err != nil {
		t.Fatal(err)
	}
	if got := be.calls[0].params; got != (protocol.ChatSendParams{SessionID: "s1", Text: "hello"}) {
		t.Fatalf("send params = %#v", got)
	}
	if got := be.calls[1].params; got != (protocol.ToolConfirmParams{SessionID: "s1", ID: "confirm-1", Allow: true}) {
		t.Fatalf("confirm params = %#v", got)
	}
	if got := be.calls[2].params; got != (protocol.ChatHistoryParams{SessionID: "s1"}) {
		t.Fatalf("history params = %#v", got)
	}
}

func TestRenderIgnoresBusyEventsForOtherSessions(t *testing.T) {
	r := New(&fakeBackend{events: make(chan protocol.Response)})
	r.session = "s1"
	r.render(protocol.Response{Method: protocol.EventBusy, Params: json.RawMessage(`{"session_id":"s2","busy":true}`)})
	if r.busy {
		t.Fatal("other session busy event changed current session")
	}
	r.render(protocol.Response{Method: protocol.EventBusy, Params: json.RawMessage(`{"session_id":"s1","busy":true}`)})
	if !r.busy {
		t.Fatal("current session busy event was ignored")
	}
}

func TestDispatchEventsRouteByOwnerSession(t *testing.T) {
	start := protocol.Response{Method: protocol.EventDispatchStart, Params: json.RawMessage(`{"owner_session_id":"parent","session_id":"child"}`)}
	if got, scoped := eventOwnerSession(start); !scoped || got != "parent" {
		t.Fatalf("dispatch start owner = %q, scoped=%v", got, scoped)
	}
	childEvent := protocol.Response{Method: protocol.EventDelta, Params: json.RawMessage(`{"session_id":"child"}`)}
	if got, scoped := eventOwnerSession(childEvent); !scoped || got != "child" {
		t.Fatalf("child delta owner = %q, scoped=%v", got, scoped)
	}
}
