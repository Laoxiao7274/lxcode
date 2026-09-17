// Package sessiondata 定义会话与存储共享的业务数据，不依赖数据库或传输层。
package sessiondata

// SessionMeta 是会话列表摘要。
type SessionMeta struct {
	ID        string
	Title     string
	UpdatedAt string
	Messages  int
	Archived  bool
	Workspace string
}

// ProjectMeta 是注册项目的身份与根目录。
type ProjectMeta struct {
	ID   string
	Name string
	Path string
}

// SearchHit 是历史消息检索结果。
type SearchHit struct {
	SessionID string
	Index     int
	Role      string
	Content   string
}
