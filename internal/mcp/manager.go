package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Manager 管理一组 MCP 服务器连接，并对配置做**幂等对账**。
//
// Sync(specs) 是唯一入口：调用方每次把「当前应该连哪些服务器」整份传进来，
// Manager 自己算出「要连的 / 要断的 / 保持的」：
//
//   - 新增（或配置变了）→ 连接 + 列举工具；
//   - 配置没变 → **保持原连接**（不重连：重连会打断正在进行的调用，
//     也会让子进程反复重启）；
//   - 从清单里消失（用户删了）→ 断开 + 丢弃工具；
//   - 停用的服务器 → 断开 + 丢弃工具（「能力挂起」）。
//
// 对账而不是增量命令的理由：配置是「当前状态」而不是「变更流」——增量接口
// 会在漏调/重复调时静默漂移，对账天然收敛（与工具注册表 SetDynamic 同款）。
type Manager struct {
	mu      sync.Mutex
	entries map[string]*entry
}

// entry 是一个服务器的运行时状态。
type entry struct {
	cfg     ServerConfig
	client  *Client
	tools   []ToolEntry
	status  Status
	fingerp string // 配置指纹：变了就重连
}

// NewManager 构造一个空的 Manager。
func NewManager() *Manager {
	return &Manager{entries: map[string]*entry{}}
}

// close 断开连接（幂等，nil-safe——连接失败的条目没有 client）。
func (e *entry) close() {
	if e.client != nil {
		e.client.Close()
	}
}

// Sync 按 specs 对账（幂等）。返回本次的对账错误（按服务器 id）——
// 单个服务器连不上不影响其余服务器（一个坏配置不该让整份 MCP 面消失）。
func (m *Manager) Sync(ctx context.Context, specs []ServerConfig) map[string]error {
	m.mu.Lock()
	defer m.mu.Unlock()

	errs := map[string]error{}
	want := make(map[string]ServerConfig, len(specs))
	for _, s := range specs {
		if strings.TrimSpace(s.ID) == "" {
			continue
		}
		want[s.ID] = s
	}

	// 先断掉「不再需要」「配置变了」以及**上次没连上的**。
	//
	// 失败条目必须重试：connect 失败时 client 为 nil，若按「指纹相同就保持」
	// 处理，一个启动时连不上的服务器会**永远**不再尝试（用户修好命令后重连
	// 也没用）——那是最难排查的一类问题（界面一直显示失败，而配置明明对）。
	for id, e := range m.entries {
		next, keep := want[id]
		if !keep || e.client == nil || fingerprint(next) != e.fingerp {
			e.close()
			delete(m.entries, id)
		}
	}

	// 再连「新增的」与「刚被断掉的」。
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Strings(ids) // 稳定顺序：状态与工具顺序不随 map 迭代漂移
	for _, id := range ids {
		if _, ok := m.entries[id]; ok {
			continue
		}
		e := m.connect(ctx, want[id])
		m.entries[id] = e
		if e.status.State != StateConnected {
			errs[id] = fmt.Errorf("%s", e.status.LastError)
		}
	}
	return errs
}

// connect 连一个服务器并列举工具（失败不返回 error，而是记进状态——
// 一个服务器连不上是常态，调用方要的是「其余照常可用 + 界面看得见原因」）。
func (m *Manager) connect(ctx context.Context, cfg ServerConfig) *entry {
	e := &entry{cfg: cfg, fingerp: fingerprint(cfg), status: Status{State: StateError}}
	client, err := Dial(ctx, cfg)
	if err != nil {
		e.status.LastError = err.Error()
		return e
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		e.status.LastError = err.Error()
		e.status.Stderr = client.StderrTail()
		return e
	}
	e.client = client
	e.tools = tools
	e.status = Status{
		State:     StateConnected,
		ToolCount: len(tools),
		Stderr:    client.StderrTail(),
	}
	return e
}

// Tools 返回全部服务器已列举到的工具（按服务器 id、再按工具名排序——稳定）。
func (m *Manager) Tools() []ToolEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.entries))
	for id := range m.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []ToolEntry
	for _, id := range ids {
		out = append(out, m.entries[id].tools...)
	}
	return out
}

// Statuses 返回各服务器的状态（按 id 排序，界面用）。
func (m *Manager) Statuses() map[string]Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]Status, len(m.entries))
	for id, e := range m.entries {
		st := e.status
		// 连接后再取一次 stderr 尾部（进程可能刚打了日志）。
		if e.client != nil {
			st.Stderr = e.client.StderrTail()
		}
		out[id] = st
	}
	return out
}

// Call 调用某个服务器的工具（exposedName 是暴露给模型的名字）。
func (m *Manager) Call(ctx context.Context, exposedName string, args json.RawMessage) (CallResult, error) {
	m.mu.Lock()
	var target *entry
	for _, e := range m.entries {
		for _, t := range e.tools {
			if t.Name == exposedName {
				target = e
				break
			}
		}
		if target != nil {
			break
		}
	}
	m.mu.Unlock()
	if target == nil {
		return CallResult{}, fmt.Errorf("未知 MCP 工具 %q（服务器可能未连接或已停用）", exposedName)
	}
	return target.client.CallTool(ctx, exposedName, args)
}

// Close 断开全部连接（幂等）。
func (m *Manager) Close() {
	m.mu.Lock()
	entries := m.entries
	m.entries = map[string]*entry{}
	m.mu.Unlock()
	for _, e := range entries {
		e.close()
	}
}

// fingerprint 是配置指纹：内容变了才重连（键顺序不该导致重连）。
func fingerprint(cfg ServerConfig) string {
	keys := make([]string, 0, len(cfg.Env))
	for k := range cfg.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(cfg.Transport)
	b.WriteString("\x00")
	b.WriteString(cfg.Command)
	b.WriteString("\x00")
	b.WriteString(strings.Join(cfg.Args, "\x00"))
	b.WriteString("\x00")
	b.WriteString(cfg.URL)
	b.WriteString("\x00")
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(cfg.Env[k])
		b.WriteString("\x00")
	}
	return b.String()
}
