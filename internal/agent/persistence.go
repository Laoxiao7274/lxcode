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
	// AppendMsg 追加一条消息，返回实现分配的序号（seq 归实现所有——agent 不见
	// SQL，但要把这个号原样带给前端当撤回锚点，所以由实现回传）。
	AppendMsg(string, llm.Message) (int64, error)
	// AppendCheckpoint 追加一条压缩检查点：它替换（影子）当前历史里从第 skip 条
	// 起的 count 条。调用方按"历史下标"说话——seq 归实现所有（agent 不见 seq），
	// 实现负责把它解析成库内序号区间并记录，回放时跳过被影子的行、并把检查点放回
	// 被影子段原本占据的位置（主会话 skip=0 即前缀，子会话 skip=1 以保护头部任务）。
	// 同样回传检查点自己的序号（它也要进内存历史，见 AppendMsg 的理由）。
	AppendCheckpoint(sessionID string, m llm.Message, skip, count int) (int64, error)
	// Rewind 撤回：把 seq 那条消息**及其之后的全部历史**删掉，返回从当前历史里
	// 移除的条数；锚点已不在历史里时返回 0（幂等，不是错误）。
	// 检查点的影子集合要跟着收缩（引用已删行的项滤掉、滤空则整条删掉）——
	// 否则被删的原文会在回放时"复活"，内存与库分叉。
	Rewind(sessionID string, seq int64) (int, error)
	Load(string) ([]llm.Message, error)
	Latest() (string, []llm.Message, error)
	List() ([]sessiondata.SessionMeta, error)
	SessionWorkspace(string, string) error
	WorkspaceOf(string) (string, error)
	ProjectByID(string) (sessiondata.ProjectMeta, bool, error)
	// Search 按查询参数检索历史消息，返回 (命中, 总命中数, 错误)。
	Search(sessiondata.SearchQuery) ([]sessiondata.SearchHit, int, error)
}
