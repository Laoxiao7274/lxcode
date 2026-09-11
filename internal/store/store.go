// Package store 实现会话的磁盘持久化：每会话一个 JSONL 文件，append-only。
//
// 为什么是 JSONL 而不是 SQLite：纯 Go 交叉编译不需要 CGO（SQLite 要 CGO 或
// modernc 纯 Go 实现——后者给二进制加 ~10MB 和一个重依赖）；单用户的会话量级
// （几百个文件、每个几 MB）用纯文本足够，且人类可读（cat 一下就能排障）、
// 可整目录备份。语义记忆/向量检索是另一层的需求，不在这层解决。
//
// 崩溃安全：append-only 天然容忍"最后一行写了一半"——加载时丢掉解析失败的
// 尾行并记日志，服务不挂。
package store

import (
	"bufio"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/moyunteng/lxcode/internal/llm"
)

// storeVersion 是会话文件格式版本；改行结构时递增。
const storeVersion = 1

// record 是 JSONL 的一行：meta（会话头）或 msg（一条消息）。
type record struct {
	V         int          `json:"v"`
	Type      string       `json:"type"` // "meta" | "msg"
	ID        string       `json:"id,omitempty"`
	CreatedAt string       `json:"created_at,omitempty"`
	Message   *llm.Message `json:"message,omitempty"`
}

// SessionMeta 是会话列表的条目（resume 选择器的数据源）。
type SessionMeta struct {
	ID        string
	Title     string // 第一条 user 消息截断（空会话为占位）
	UpdatedAt string // 最后修改时间（文件 mtime）
	Messages  int
}

// Store 管理一个目录下的所有会话文件。
type Store struct {
	dir string
	mu  sync.Mutex // 文件创建/枚举互斥（追加写由各会话自己的句柄串行）
}

// Open 打开（必要时创建）会话目录。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建会话目录 %s 失败: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Dir 返回会话目录（CLI 提示用）。
func (s *Store) Dir() string { return s.dir }

// newSessionID 生成会话 id：时间戳前缀（文件名天然按时间排序）+ 随机后缀防碰撞。
func newSessionID() string {
	return time.Now().Format("20060102-150405") + "-" + randomSuffix()
}

func randomSuffix() string {
	b := make([]byte, 2)
	if _, err := crand.Read(b); err != nil {
		return "0000" // 退化路径：crypto 失败极罕见，时间戳本身已近唯一
	}
	return fmt.Sprintf("%x", b)
}

// Create 开一个新会话文件并写入 meta 行，返回追加句柄。
// 调用方负责 Close（agent 在切换/退出时关闭）。
func (s *Store) Create() (id string, f *os.File, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = newSessionID()
	path := filepath.Join(s.dir, id+".jsonl")
	f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", nil, fmt.Errorf("创建会话文件 %s 失败: %w", path, err)
	}
	meta := record{V: storeVersion, Type: "meta", ID: id, CreatedAt: time.Now().Format(time.RFC3339)}
	if err := writeRecord(f, meta); err != nil {
		f.Close()
		return "", nil, err
	}
	return id, f, nil
}

// AppendMsg 把一条消息追加到已打开的会话文件。
func (s *Store) AppendMsg(f *os.File, m llm.Message) error {
	return writeRecord(f, record{V: storeVersion, Type: "msg", Message: &m})
}

func writeRecord(f *os.File, r record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("序列化会话记录失败: %w", err)
	}
	// 单次 write 追加一行：对 O_APPEND 的单次 write 原子性足够
	// （会话日志容忍断电丢最后一行，加载时丢坏行兜底）
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("写会话文件失败: %w", err)
	}
	return nil
}

// OpenForAppend 打开既有会话文件继续追加（resume 后续写）。
func (s *Store) OpenForAppend(id string) (*os.File, error) {
	path := filepath.Join(s.dir, id+".jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开会话文件 %s 失败: %w", path, err)
	}
	return f, nil
}

// Load 读出会话的全部消息。最后一行解析失败（断电写了一半）时丢弃并记日志；
// 中间的坏行也丢弃——会话日志不是账本，坏一行不该让整个会话不可用。
func (s *Store) Load(id string) (msgs []llm.Message, err error) {
	path := filepath.Join(s.dir, id+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开会话文件失败: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024) // 单行上限 4MB（工具结果入历史前已截到 4KB，这里留余量）
	bad := 0
	for sc.Scan() {
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			bad++
			continue
		}
		switch r.Type {
		case "meta":
			continue // 会话头：不是消息，也不是坏行
		case "msg":
			if r.Message != nil {
				msgs = append(msgs, *r.Message)
			} else {
				bad++
			}
		default:
			bad++
		}
	}
	if bad > 0 {
		// 记日志但不失败：会话日志的可用性优先于完整性
		fmt.Fprintf(os.Stderr, "[store] 会话 %s 有 %d 行损坏（已跳过）\n", id, bad)
	}
	return msgs, nil
}

// List 列出全部会话（按更新时间倒序），供 resume 选择器使用。
// 会话量级小（本地几百个文件），逐文件全读是可接受的简单实现。
func (s *Store) List() ([]SessionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []SessionMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		meta := readMeta(filepath.Join(s.dir, e.Name()), id)
		if meta == nil {
			continue // 非会话文件（无 meta 行）——跳过
		}
		out = append(out, *meta)
	}
	// 按更新时间倒序：最近的排最前
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].UpdatedAt > out[j-1].UpdatedAt; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// readMeta 读单个会话文件的元信息（标题取第一条 user 消息，更新时间取 mtime）。
// 返回 nil 表示这不是一个有效会话文件（读失败或无 meta 行）。
func readMeta(path, id string) *SessionMeta {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil
	}
	meta := &SessionMeta{
		ID:        id,
		UpdatedAt: st.ModTime().Format("2006-01-02 15:04"),
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	hasMeta := false
	for sc.Scan() {
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue // 坏行：计数但继续
		}
		switch r.Type {
		case "meta":
			if r.ID == id && r.V == storeVersion {
				hasMeta = true
			}
		case "msg":
			meta.Messages++
			if r.Message != nil && r.Message.Role == "user" && meta.Title == "" {
				meta.Title = clipTitle(r.Message.Content)
			}
		}
	}
	if !hasMeta {
		return nil
	}
	if meta.Title == "" {
		meta.Title = "（空会话）"
	}
	return meta
}

// clipTitle 截标题：一行以内、40 个字符封顶（列表预览用）。
func clipTitle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// Latest 找最近的会话（按 mtime——同一秒内创建的多个会话，文件名顺序不可靠），
// 返回 id 与消息。没有任何会话时返回 ("", nil, nil)——调用方据此走"全新开始"。
func (s *Store) Latest() (string, []llm.Message, error) {
	s.mu.Lock()
	entries, err := os.ReadDir(s.dir)
	s.mu.Unlock()
	if err != nil {
		return "", nil, err
	}
	var bestID string
	var bestMod time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if bestID == "" || info.ModTime().After(bestMod) {
			id := strings.TrimSuffix(e.Name(), ".jsonl")
			if readMeta(filepath.Join(s.dir, e.Name()), id) == nil {
				continue // 非会话文件（无 meta 行）
			}
			bestID, bestMod = id, info.ModTime()
		}
	}
	if bestID == "" {
		return "", nil, nil
	}
	msgs, err := s.Load(bestID)
	if err != nil {
		return "", nil, err
	}
	return bestID, msgs, nil
}

// SearchHit 是一条会话搜索命中。
type SearchHit struct {
	SessionID string // 来源会话
	Index     int    // 会话内第几条消息（1 起）
	Role      string // user | assistant | tool
	Content   string // 截断展示
}

// searchClip 是命中内容的展示截断长度——给模型看的是"判断相关性
// 的片段"，需要完整内容时模型应引导用户 resume 该会话。
const searchClip = 120

// Search 在全部会话的消息内容里按正则搜索，按会话更新时间从近到远遍历
// （用户说"上次"多半指最近的会话）。max 为命中上限。
func (s *Store) Search(pattern string, max int) ([]SearchHit, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("模式不是合法正则: %w", err)
	}
	if max <= 0 {
		max = 30
	}
	if max > 100 {
		max = 100
	}
	// List 已按更新时间倒序
	metas, err := s.List()
	if err != nil {
		return nil, err
	}
	var hits []SearchHit
	for _, meta := range metas {
		if len(hits) >= max {
			break
		}
		msgs, err := s.Load(meta.ID)
		if err != nil {
			continue // 单个会话读失败不挡整体
		}
		for i, m := range msgs {
			if len(hits) >= max {
				break
			}
			if !re.MatchString(m.Content) {
				continue
			}
			hits = append(hits, SearchHit{
				SessionID: meta.ID,
				Index:     i + 1,
				Role:      m.Role,
				Content:   clipStr(m.Content, searchClip),
			})
		}
	}
	return hits, nil
}

// clipStr 截断到 n 个字符（rune 安全），尾部加省略号。
func clipStr(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ") // 命中展示压成单行，多行消息不至于撑爆
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// FormatSearchHits 把命中渲染成给模型的文本（session_search 工具的输出）。
func FormatSearchHits(hits []SearchHit, total int) string {
	if len(hits) == 0 {
		return "没有匹配的历史消息。"
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "[%s #%d %s] %s\n", h.SessionID, h.Index, h.Role, h.Content)
	}
	if total > len(hits) {
		fmt.Fprintf(&b, "\n…（共 %d 条命中，显示前 %d 条：缩小 pattern 或提高 max）", total, len(hits))
	}
	return b.String()
}
