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
	// ownerID/ownerSeq 是**快照归属**（docs/session-tree.md §C.2.1）：回滚发起方
	// （顶层会话 id）与「这次派发属于父会话哪一轮」（父会话那条 dispatch 消息的 seq）。
	// 与子会话行同一条 INSERT 落库，避免「子会话建好了但归属没写上」的中间态。
	CreateChild(parentID, agentID, dispatchID, ownerID string, ownerSeq int64) (string, error)
	// SetSnapshotOwner 落子会话的快照归属（续跑时按本次派发重写 owner_seq——
	// §C.5.1 边界①：同一个子会话在第 45 轮续跑时改的文件必须归到 45 而不是 42）。
	SetSnapshotOwner(sessionID, ownerID string, ownerSeq int64) error
	// SnapshotOwner 读子会话的快照归属默认值。返回 ("", 0) = 老会话/顶层会话
	// （调用方回落「自己就是 owner」）。
	SnapshotOwner(sessionID string) (ownerID string, ownerSeq int64, err error)
	// SnapshotWrite 落一行文件快照（写工具执行成功之后调用）。
	SnapshotWrite(rec sessiondata.FileSnapshot) error
	// PutBlob 写一个内容寻址的 blob，返回 hash（已存在则跳过——天然幂等）。
	PutBlob(data []byte) (hash string, err error)
	// GetBlob 读 blob 并校验内容 hash（防磁盘损坏/人为改名）。
	GetBlob(hash string) ([]byte, error)
	// SnapshotsForRestore 收集一次回滚要处理的全部快照（跨会话：父 + 全部子孙，
	// 按 owner_session_id/owner_seq 一次索引查询）。inclusive = owner_seq >= cut。
	SnapshotsForRestore(ownerID string, cutSeq int64, inclusive bool) ([]sessiondata.FileSnapshot, error)
	// SnapshotCountsByOwnerSeq 统计每一轮可还原的文件改动数（含子会话），
	// chat.tree 的节点徽标用。
	SnapshotCountsByOwnerSeq(ownerID string) (map[int64][2]int, error)
	// SaveRestoreIntent / LoadRestoreIntents / ClearRestoreIntent 是崩溃自愈的
	// 意图日志（§C.5：文件已还原但 leaf 未动时，下次启动自动补做）。
	SaveRestoreIntent(it sessiondata.SnapshotIntent) error
	LoadRestoreIntents() ([]sessiondata.SnapshotIntent, error)
	ClearRestoreIntent(sessionID string) error
	// AppendMsg 追加一条消息，返回实现分配的序号（seq 归实现所有——agent 不见
	// SQL，但要把这个号原样带给前端当撤回锚点，所以由实现回传）。
	AppendMsg(string, llm.Message) (int64, error)
	// AppendCheckpoint 追加一条压缩检查点：它替换（影子）当前历史里从第 skip 条
	// 起的 count 条。调用方按"历史下标"说话——seq 归实现所有（agent 不见 seq），
	// 实现负责把它解析成库内序号区间并记录，回放时跳过被影子的行、并把检查点放回
	// 被影子段原本占据的位置（主会话 skip=0 即前缀，子会话 skip=1 以保护头部任务）。
	// 同样回传检查点自己的序号（它也要进内存历史，见 AppendMsg 的理由）。
	AppendCheckpoint(sessionID string, m llm.Message, skip, count int) (int64, error)
	// SetLeaf 把叶指针移到 seq（0 = 空路径），返回 (离开路径的条数, 新叶)。
	// **只动指针，一行不删**——旧分支完整保留，切回去就回来。
	SetLeaf(sessionID string, seq int64) (removed int, leaf int64, err error)
	// RewindTo 把叶移到 **anchor 的父节点**（anchor 自己也离开路径），返回
	// (离开路径的条数, 新叶)。幂等：anchor 不在当前路径上 → (0, 当前叶, nil)。
	// 与 SetLeaf 的分工就是「落点差一格」——两个具名方法，不在调用处各算一遍。
	RewindTo(sessionID string, anchor int64) (removed int, leaf int64, err error)
	// LeafSeq 读当前叶。0 = 空路径（用户回到第一条消息之前）——**不是「未知」**。
	LeafSeq(sessionID string) (int64, error)
	// TreeRows 读会话的树骨架（轻量投影：不含正文全文，只取前 120 字符做摘要）。
	// 树面板不能走 Load/readRows（那是全列读含 KB 级正文）。
	TreeRows(sessionID string) ([]sessiondata.TreeRow, error)
	// OwnerSessionID 读会话的**时间线/快照归属**（顶层会话 id）。
	// 子会话的写要记到顶层会话的账上（回滚切段用父会话的 seq），而 agent 层
	// 不该自己沿 parent_id 上溯（那是存储细节）。
	OwnerSessionID(sessionID string) (string, error)
	// SaveContextUsage 落库一次上下文占用测量（只有**主轮**写它——子上下文有自己
	// 的窗口）。形状用 sessiondata.ContextUsage：store 与 agent 共享同一份定义
	//（agent 侧是别名），否则 store 就得 import agent，违反分层规则。
	//
	// 失败不该打断一轮（占用是展示信息，不是业务不变量）：调用方只记日志。
	SaveContextUsage(sessionID string, u sessiondata.ContextUsage) error
	// ContextUsageOf 读回会话最近一次主轮落库的占用测量。ok=false = 库里没有
	//（老会话/从没跑过主轮）——调用方据此**回落估算**，而不是把它当成 0（当成 0
	// 就是"编一个数"：空历史显示 0% 比显示中性态更坏）。
	ContextUsageOf(sessionID string) (sessiondata.ContextUsage, bool, error)
	// SaveApproval 落库会话当前的审批档（chat.approval 改档 / 新会话按 behavior
	// 默认档初始化时写）。失败不该打断改档本身（内存才是运行真源，最坏情况是
	// 重启后回落默认档）：调用方只记日志。
	SaveApproval(sessionID, approval string) error
	// ApprovalOf 读回会话落库的审批档。空串 = 没落过（老会话/从没改过档）——
	// 调用方据此回落 behavior 默认档，而不是当成 confirm 写死。
	ApprovalOf(sessionID string) (string, error)
	// SaveSessionModel 落库会话**选中的模型**（chat.model 切模型 / 子会话继承父会话）。
	// 为什么落库：模型选择是会话的状态——用户在会话 A 里切到模型 X 后重启，打开 A
	// 还得是 X（原先选择只能改全局 default 角色，一个会话切模型所有会话跟着变）。
	// 失败不该打断切模型本身（内存才是运行真源，最坏情况是重启后回落默认）：
	// 调用方只记日志。
	SaveSessionModel(sessionID, model string) error
	// SessionModelOf 读回会话落库的模型选择。空串 = 没选过（老会话/从没切过）——
	// 调用方据此回落 Agent 绑定与 default 角色，而不是把它当成"没模型可用"。
	SessionModelOf(sessionID string) (string, error)
	// SaveTodos 落库会话当前的任务清单快照（todo 契约）。失败不该打断清单更新
	//（内存才是运行真源，最坏情况是重启后清单丢失）：调用方只记日志。
	SaveTodos(sessionID string, items []sessiondata.TodoItem) error
	// TodosOf 读回会话落库的任务清单快照。ok=false = 库里没有（老会话/从没建过）——
	// 调用方按"无清单"处理，不编一个空清单。
	TodosOf(sessionID string) ([]sessiondata.TodoItem, bool, error)
	Load(string) ([]llm.Message, error)
	Latest() (string, []llm.Message, error)
	List() ([]sessiondata.SessionMeta, error)
	SessionWorkspace(string, string) error
	WorkspaceOf(string) (string, error)
	// ProjectByID 读项目元数据。
	ProjectByID(string) (sessiondata.ProjectMeta, bool, error)
	// SaveSharedAttachment 把一个文件登记进会话附件目录（share_file 共享通道）：
	// 落盘到 `<attachments>/<session-id>/<随机id>.<ext>` 并返回**展示元数据**
	//（相对 attachments 根的路径 + 净化后的名字 + 大小 + 分类）。agent 层不见
	// 磁盘布局——路径形态、随机文件名、大小上限都归实现所有（与 AppendMsg 的
	// "seq 归实现所有"同一条接缝纪律）。
	SaveSharedAttachment(sessionID, name string, data []byte) (llm.AttachmentRef, error)
	// Search 按查询参数检索历史消息，返回 (命中, 总命中数, 错误)。
	Search(sessiondata.SearchQuery) ([]sessiondata.SearchHit, int, error)
}
