package agent

import (
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// Persistence 是运行时消费的最小持久化契约；实现可为 SQLite 或内存替身。
// 不暴露 SQL、连接、事务句柄或 store 实现类型。
type Persistence interface {
	Create() (string, error)
	AppendMsg(string, llm.Message) error
	Load(string) ([]llm.Message, error)
	Latest() (string, []llm.Message, error)
	List() ([]sessiondata.SessionMeta, error)
	SessionWorkspace(string, string) error
	WorkspaceOf(string) (string, error)
	ProjectByID(string) (sessiondata.ProjectMeta, bool, error)
	Search(string, int) ([]sessiondata.SearchHit, error)
}
