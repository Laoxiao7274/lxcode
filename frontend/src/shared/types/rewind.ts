// 压缩 / 回退 / 切叶 / 会话树（chat.compact、chat.rewind、chat.setLeaf、chat.tree 的 wire 形态）。

/** 手动压缩的结果（chat.compact 的应答）。 */
export interface CompactOutcome {
  /** false = 没有可压的收益（历史太短 / 摘要不缩水）——不是错误。 */
  compacted: boolean;
  /** 为什么没压（人话，直接显示）。 */
  reason?: string;
  before?: number;
  after?: number;
  shadowed?: number;
}

/** 文件还原结果（对齐 protocol.FileRestoreResult，chat.rewind / chat.setLeaf 共用）。
 *
 *  覆盖范围含**子会话**的改动（2026-12 拍板：父会话回滚要把子 Agent 的改动一起
 *  还原，子会话的对话历史不动）——每项带 session_id 标明是谁改的。
 *
 *  `conflicts` 非空 = **整批未执行**（fail-closed）：leaf 不动、文件不动，清单交给
 *  用户看。它不是错误码——冲突是正常业务结果（用户手改过文件）。 */
export interface FileRestoreResult {
  /** 按快照写回的文件。 */
  restored?: FileRestoreItem[];
  /** 回滚到「当时还不存在」→ 删掉的文件（agent 新建的）。 */
  deleted?: FileDeleteItem[];
  /** 磁盘现状与快照期望不符（用户手改/bash 改过）：整批拒绝。 */
  conflicts?: FileConflict[];
  /** 大文件/二进制：当时没抓快照，如实报「回滚不到」。 */
  skipped?: FileSkip[];
  /** 本次回滚涉及的子会话 id（父会话自己不算）。 */
  child_sessions?: string[];
}

/** 一项文件还原（写回快照内容）。 */
export interface FileRestoreItem {
  path: string;
  /** 取的是哪个快照的 before（**写它的那个会话**的 seq）。 */
  from_seq: number;
  /** 那次写属于父会话的哪一轮之后（缺省 = 0/未知）。 */
  from_owner_seq?: number;
  /** 写这个文件的那个会话（父会话自己 = 顶层 id；否则是子会话 id）。 */
  session_id?: string;
  bytes: number;
  /** 磁盘上本来没有，按快照写回。 */
  recreated?: boolean;
}

/** 一项「回滚 = 删掉这个文件」。 */
export interface FileDeleteItem {
  path: string;
  /** 谁新建的这个文件（多半是子会话）。 */
  session_id?: string;
}

/** 一项冲突（磁盘现状与快照期望不符）。 */
export interface FileConflict {
  path: string;
  expected_hash?: string;
  actual_hash?: string;
  /** 期望状态是**哪个会话**写的（冲突提示要说清「这是子 Agent 改的文件」）。 */
  session_id?: string;
  reason: string;
}

/** 一项「当时没抓快照，回滚不到」。 */
export interface FileSkip {
  path: string;
  reason: string;
}

/** 会话回退的结果（chat.rewind 的应答）。
 *
 *  **不用于渲染**——时间线的截断由 rewound 事件驱动（本地乐观 + 后端广播走
 *  同一条归约路径）。它只回答「后端真删了几条」，供提示与诊断用。
 *
 *  语义已随会话树改为「leaf 移到 parent(seq)」（一条不删、旧分支保留）：`removed`
 *  仍是「离开当前路径的条数」，与旧口径逐例相同（老客户端零影响）。 */
export interface RewindOutcome {
  /** 离开当前路径的消息条数（锚点那条 + 它之后的全部）。 */
  removed: number;
  /** 移动后的叶（0 = 空路径）。缺省 = 老后端没回这个键。
   *  **键名保持 wire 的 snake_case**：这个接口是 `chat.rewind` 应答的 wire 形态
   *  （protocol-golden 的 `_type` 钉住它），与 ChatHistoryParams 同一类"协议形态"
   *  类型；适配成 camelCase 会在两处各有一份、迟早漂移。 */
  leaf_seq?: number;
  /** true = 历史保留为分支（一条没删）——老客户端以为删了，其实还能在树里切回来。 */
  branched?: boolean;
  /** 本次带文件还原时的结果（含子会话）。缺省 = 没开 files。 */
  files?: FileRestoreResult;
}

/** chat.setLeaf 的结果：**不回 messages**（客户端重拉 chat.history——切 leaf 之后
 *  要重载的不止消息，还有树/统计/上下文/检查点下标，复制一份必然与 chat.history
 *  分叉）。 */
export interface SetLeafOutcome {
  /** 移动后的叶（0 = 空路径）。 */
  leafSeq: number;
  /** 离开当前路径的条数（与 chat.rewind 的 removed 同口径）。 */
  removed: number;
  /** 本次带文件还原时的结果（冲突时 conflicts 非空且 leaf 未变）。 */
  files?: FileRestoreResult;
}

/** 会话树里的一个节点（对齐 protocol.TreeNode）。`nodes` 按 seq 升序，前端自己按
 *  parent_seq 建树。**只回 user 节点**（2026-12 用户拍板）：一轮对话（user + 它背后
 *  的 assistant/tool）在树上是一行。 */
export interface TreeNode {
  seq: number;
  /** 0 = root（树上最近前驱 user 不存在）。 */
  parentSeq: number;
  /** 恒 "user"（字段保留：历史载荷兼容）。 */
  kind: string;
  /** 轮次序号（真实 user 消息从 1 起；与 SessionStats.turns 同口径）。 */
  turn?: number;
  /** ≤120 字符的单行摘要（不含正文全文）。 */
  summary?: string;
  /** 直接子节点数（>1 = 这个问题有多个分支）。 */
  children: number;
  /** 是否在当前 leaf 路径上（false = 废弃分支）。 */
  onPath: boolean;
  /** 父节点缺失 / 成环（畸形数据兜底：不丢节点、不抛异常）。 */
  orphan?: boolean;
  /** 该轮可还原的文件改动数（含子会话，快照 owner_seq 投影归轮）；0 时整键缺席。 */
  files?: number;
  /** 其中来自子会话的改动数（files 的子集）；0 时整键缺席。 */
  childFiles?: number;
  /** 恒缺席（字段保留兼容；user-only 树上无数据源）。 */
  external?: number;
}

/** 会话树骨架（chat.tree 的应答）。 */
export interface ChatTreeResult {
  /** 当前叶（0 = 空路径）。 */
  leafSeq: number;
  /** 按 seq 升序。 */
  nodes: TreeNode[];
  /** 全量节点数（截断时用于「还有 N 个」提示）。 */
  total: number;
  /** true = 只回了最近 max_nodes 个节点（**不静默截断**，面板要显示提示）。 */
  truncated?: boolean;
}

/** 归档的结果（session.archive 的应答）。 */
export interface ArchiveOutcome {
  /** 是否已归档（恒为 true——请求成功即已归档）。 */
  archived: boolean;
}
