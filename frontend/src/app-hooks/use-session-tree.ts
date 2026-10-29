// 会话树面板的数据域（docs/session-tree.md §D.2）：拉 chat.tree、维护过滤/折叠状态、
// 执行「切到此」「回到最新」。
//
// 三条纪律：
//  ① **老后端没有 chat.tree → tree 保持 null，面板回落线性轮次视图**（不报错、不空屏
//     ——AGENTS.md §5 坑 11「老后端 + 新前端不许炸」）；
//  ② 折叠/过滤态是**纯 UI 偏好**：放局部 state、按会话记、不落库（它们不影响任何
//     数据事实，进了 UIState 就要跟着历史归约走，白白多一处可能漂移的状态）；
//  ③ `chat.leafChanged` 到达时**重拉**（leaf 是共享状态，可能是别的客户端动的）——
//     事件是唯一的触发源，本地不猜"该怎么变"。
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { AgentEvent, AgentSource, ChatTreeResult, FileRestoreResult } from "../shared/types";
import { flattenTree, latestSeq, type TreeFilter, type TreeRow, type TreeViewState } from "../components/panels/tree-model";

/** 每会话的树视图偏好（过滤 + 折叠/展开集合）——切走再切回保持，不落库。 */
interface ViewState {
  filter: TreeFilter;
  collapsed: Set<number>;
  expanded: Set<number>;
}
const DEFAULT_VIEW: ViewState = { filter: "all", collapsed: new Set(), expanded: new Set() };

/** 首屏请求的节点上限（大会话少传一半以上的骨架，面板出得快）。
 *
 *  为什么首屏不是后端缺省的 2000：`chat.tree` 要扫全表算骨架与分支摘要，超大会话
 *  （几千条消息）首屏等一个整骨架会让右栏空着好几秒。先要最近 400 个（后端按 seq
 *  从大到小取最近 N 个并**补齐祖先**，所以近处结构一定完整），truncated 时面板顶部
 *  显示「更早的 N 个节点未加载」+「加载全部」按钮。
 *
 *  「加载全部」传 `max_nodes: 0` = 让后端用它自己的缺省上限（2000，见
 *  protocol.ChatTreeParams）——**不是**无上限，后端没有那个选项。 */
const TREE_FIRST_NODES = 400;

export interface SessionTreeApi {
  /** null = 还没有树（老后端 / 加载中 / 空会话）——面板据此回落线性视图。 */
  tree: ChatTreeResult | null;
  /** 上一次拉取失败的原因（人话；null = 正常）。面板显示成一行提示而不是空屏。 */
  error: string | null;
  loading: boolean;
  view: TreeViewState;
  setFilter: (filter: TreeFilter) => void;
  /** 折叠/展开一行（on_path 行改 collapsed，off_path 行改 expanded）。 */
  toggle: (seq: number, onPath: boolean) => void;
  /** 重新拉树（`loadAll` = 传 max_nodes=0 让后端用缺省上限；后端没有"无上限"）。 */
  refresh: (opts?: { loadAll?: boolean }) => Promise<void>;
  /** 切到此（chat.setLeaf）：该节点留在路径上。返回后端应答（含文件还原结果）。 */
  setLeaf: (seq: number, files?: boolean) => Promise<{ leafSeq: number; removed: number; files?: FileRestoreResult }>;
  /** 「回到最新」的落点 = 树里最大的 seq（**绝不传 0**——0 是空路径）。 */
  latest: number;
  /** 是否已加载全部节点（truncated=false 或点过「加载全部」）。 */
  loadedAll: boolean;
  /** 面板要画的行（树 + 视图态 → 行）。**只在这里算一次**——App 的 renderView 被
   *  「冻结渲染」缓存，在那里 useMemo 会把行冻在上一次的值上（切分支之后树不刷新）。 */
  rows: TreeRow[];
}

export function useSessionTree({ source, sessionId }: {
  source: AgentSource;
  sessionId: string;
}): SessionTreeApi {
  const [tree, setTree] = useState<ChatTreeResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadedAll, setLoadedAll] = useState(false);
  /** 每会话的视图偏好（按会话记：切走再切回保持过滤/折叠态）。 */
  const [views, setViews] = useState<Record<string, ViewState>>({});
  /** 请求序号：切会话/连续刷新时只认最后一次应答（旧应答不许覆盖新会话的树）。 */
  const reqRef = useRef(0);
  const sessionRef = useRef(sessionId);
  sessionRef.current = sessionId;

  const fetchTree = useCallback(async (opts?: { loadAll?: boolean }) => {
    const sid = sessionId;
    if (!sid) {
      setTree(null);
      return;
    }
    const token = ++reqRef.current;
    setLoading(true);
    try {
      // loadAll = 传 max_nodes=0（后端按缺省上限 2000 处理，没有真正的"无上限"）——
      // 语义是"不要这个客户端自己缩窗口"，与 protocol.ChatTreeParams 的注释一致。
      const next = await source.tree(sid, opts?.loadAll ? { maxNodes: 0 } : { maxNodes: TREE_FIRST_NODES });
      if (token !== reqRef.current || sessionRef.current !== sid) return;
      setTree(next);
      setError(null);
      if (opts?.loadAll) setLoadedAll(true);
    } catch (e) {
      if (token !== reqRef.current || sessionRef.current !== sid) return;
      // 老后端没有 chat.tree（或纯内存模式）→ 首次失败时 tree 仍是 null，面板回落
      // 线性视图（不报错、不空屏）。
      //
      // **刷新失败不清掉已有的树**：树还在（只是这次没刷上），把面板打回线性视图是
      // 无谓的信息损失；错误原因留在 error 里显示成一行提示（用户要知道"这份可能不是
      // 最新的"，而不是看到面板突然换了形态）。
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (token === reqRef.current) setLoading(false);
    }
  }, [source, sessionId]);

  // 切会话 / 首次挂载：拉一次树（并清掉上一次的 loadedAll——「加载全部」是每会话的）。
  useEffect(() => {
    setLoadedAll(false);
    setError(null);
    setTree(null);
    void fetchTree();
  }, [fetchTree]);

  // 树会变的四个时机（**只认本会话**——别的会话的事件与这块面板无关）：
  //   ① chat.leafChanged：leaf 是共享状态，可能别的客户端动的；
  //   ② chat.userMessage（主会话）：新消息落库 → 长出一个新节点，leaf 也跟着走；
  //   ③ chat.done（主轮）：一轮结束，assistant/tool 节点补齐（分支摘要稳定下来）；
  //   ④ session.changed：元数据变化（含新消息落库后的刷新）。
  // 子轮的事件（带 dispatchId）不触发——它们改的是子会话，主会话的树没变。
  useEffect(() => {
    return source.subscribe((ev: AgentEvent) => {
      if (ev.type === "leafChanged") {
        if (ev.sessionId === sessionRef.current) void fetchTree();
        return;
      }
      if (ev.type === "userMessage") {
        if (!ev.dispatchId && ev.sessionId === sessionRef.current) void fetchTree();
        return;
      }
      if (ev.type === "done") {
        if (!ev.dispatchId && ev.sessionId === sessionRef.current) void fetchTree();
        return;
      }
      if (ev.type === "sessionChanged") {
        if (ev.id === sessionRef.current) void fetchTree();
      }
    });
  }, [source, fetchTree]);

  const view = useMemo<TreeViewState>(() => {
    const v = views[sessionId] ?? DEFAULT_VIEW;
    return { filter: v.filter, collapsed: v.collapsed, expanded: v.expanded };
  }, [views, sessionId]);

  const setFilter = useCallback((filter: TreeFilter) => {
    setViews((cur) => ({ ...cur, [sessionId]: { ...(cur[sessionId] ?? DEFAULT_VIEW), filter } }));
  }, [sessionId]);

  const toggle = useCallback((seq: number, onPath: boolean) => {
    setViews((cur) => {
      const v = cur[sessionId] ?? DEFAULT_VIEW;
      // on_path 行改 collapsed（默认展开），off_path 行改 expanded（默认折叠）——
      // 两个集合的默认值相反，混用一个集合会让「切分支之后默认态反转」变成静默
      // 的行为改变（见 tree-model.ts 的 TreeViewState 注释）。
      const collapsed = new Set(v.collapsed);
      const expanded = new Set(v.expanded);
      const target = onPath ? collapsed : expanded;
      if (target.has(seq)) target.delete(seq);
      else target.add(seq);
      return { ...cur, [sessionId]: { filter: v.filter, collapsed, expanded } };
    });
  }, [sessionId]);

  const setLeaf = useCallback(async (seq: number, files?: boolean) => {
    const r = await source.setLeaf(sessionId, seq, files);
    // 切叶之后树的分支结构/on_path 全变了：重拉一次（setLeaf 内部已重拉历史，
    // 但树不在 chat.history 里——它是独立载荷）。
    await fetchTree({ loadAll: loadedAll });
    return r;
  }, [source, sessionId, fetchTree, loadedAll]);

  const latest = useMemo(() => latestSeq(tree), [tree]);
  const rows = useMemo(() => flattenTree(tree, view), [tree, view]);

  return { tree, error, loading, view, setFilter, toggle, refresh: fetchTree, setLeaf, latest, loadedAll, rows };
}
