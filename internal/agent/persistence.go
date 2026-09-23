package agent

import (
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// Persistence 是运行时消费的最小持久化契约；实现可为 SQLite 或内存替身。
// 不暴露 SQL、连接、事务句柄或 store 实现类型。
type Persistence interface {
	Create() (string, error)
	// CreateChild 开一个子会话（派发给子 Agent 的任务 = 一个独立会话）：parentID
	// 指回派发方，agentID 记住它是哪个 Agent（续跑时按同一套四层组合组装），
	// dispatchID 是哪次调度开的（对账/回放用）。项目归属（workspace）由实现
	// 从父会话继承——agent 层不需要记住 workspace id。
	CreateChild(parentID, agentID, dispatchID string) (string, error)
	AppendMsg(string, llm.Message) error
	// AppendCheckpoint 追加一条压缩检查点：它替换（影子）紧邻其前的 shadowed
	// 条历史。调用方按"历史条数"说话——seq 归实现所有（agent 不见 seq），
	// 实现负责把它解析成库内序号区间并记录，回放时跳过被影子的行。
	AppendCheckpoint(sessionID string, m llm.Message, shadowed int) error
	Load(string) ([]llm.Message, error)
	Latest() (string, []llm.Message, error)
	List() ([]sessiondata.SessionMeta, error)
	SessionWorkspace(string, string) error
	WorkspaceOf(string) (string, error)
	ProjectByID(string) (sessiondata.ProjectMeta, bool, error)
	Search(string, int) ([]sessiondata.SearchHit, error)
}
