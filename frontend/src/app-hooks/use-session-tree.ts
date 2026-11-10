// 会话树面板的数据域：拉 chat.tree、维护「加载全部」态、执行切叶（供跳转/回最新）。
//
// **user-only 纯导航改版**（2026-12 用户拍板）：过滤/折叠是旧模型的 UI（assistant
// 行需要过滤与折叠才看得下去）——树上只剩 user 行之后它们失去了对象，整体删除；
// 面板只剩「跳转 + 截断加载」，切叶只服务跳转与「回到最新」。
//
// 保留的三条纪律：
//  ① **老后端没有 chat.tree → tree 保持 null，面板回落线性轮次视图**（不报错、不空屏
//     ——AGENTS.md §5 坑 11「老后端 + 新前端不许炸」）；
//  ② `chat.leafChanged` 到达时**重拉**（leaf 是共享状态，可能是别的客户端动的）——
//     事件是唯一的触发源，本地不猜"该怎么变"；
//  ③ 按会话缓存树（2026-12 会话切换优化批 1）：切回已拉过的会话直接用缓存（渲染
//     立即可用），后台照常向事件失效点重拉。缓存键带连接代数（换后端 = 旧会话的
//     树整体作废，与 WSAgent.loadedSessions 的 onHelloReset 清账同一条纪律）。
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { AgentEvent, AgentSource, ChatTreeResult, FileRestoreResult } from "../shared/types";
import { flattenTree, type TreeRow } from "../components/panels/tree-model";

/** 首屏请求的节点上限（大会话少传一半以上的骨架，面板出得快）。
 *
 *  为什么首屏不是后端缺省的 2000：`chat.tree` 要扫全表算骨架与分支摘要，超大会话
 *  （几千条消息）首屏等一个整骨架会让右栏空着好几秒。先要最近 400 个（后端按 seq
 *  从大到小取最近 N 个，truncated 时面板顶部显示「更早的 N 条消息未加载」+「加载全部」
 *  按钮）。
 *
 *  「加载全部」传 `max_nodes: 0` = 让后端用它自己的缺省上限（2000，见
 *  protocol.ChatTreeParams）——**不是**无上限，后端没有那个选项。 */
const TREE_FIRST_NODES = 400;

/** 树缓存的失效事件源（与订阅 switch 同源——缓存与重拉共用一份枚举，漏一类
 *  就是「缓存里留着旧树」的静默漂移）。 */
const TREE_BUSTER_EVENTS: ReadonlySet<AgentEvent["type"]> = new Set([
  "leafChanged", "userMessage", "done", "sessionChanged",
]);

/** 每会话树缓存（连接代数入键——换后端 = 旧代数条目自然落空，不清也不串账）。
 *
 *  语义（可单测，createTreeCache 直测；hook 的接线由源码钉子钉住）：
 *  - `get(sid)`：命中返回该会话最近一次成功拉取的树（**不可变共享**——树结果
 *    归约后不再原地改，React 状态里也是同一引用）；
 *  - `set(sid, tree)`：成功拉取覆盖（失败不写——老后端 chat.tree 失败回落线性
 *    视图的纪律不变，缓存里不存失败）；
 *  - `bust(sid)`：四类失效事件（leafChanged / userMessage / done / sessionChanged）
 *    说树变了，缓存里那份就是旧的——清掉条目并立即重拉（宁空勿旧）；
 *  - `bumpGeneration()`：connectionChanged true（换后端握手成功）时代数 +1，
 *    所有旧代数条目整体作废。
 *
 *  为什么是函数式纯对象而不是 Map 上的命令式方法：与 workspace-tabs 的
 *  recordChildLink 同一条纪律——每次变更返回新对象，读方（React 渲染）拿到的
 *  引用恒有效。 */
export interface TreeCache {
  /** 命中该会话的缓存树（null = 没有有效条目——未拉过/被失效事件清掉/旧代数）。 */
  get(sessionId: string): ChatTreeResult | null;
  /** 成功拉取覆盖缓存（返回同一份 cache——无变化时不新建）。 */
  set(sessionId: string, tree: ChatTreeResult): TreeCache;
  /** 失效事件：清掉该会话的条目（幂等——没有条目返回同一份 cache）。 */
  bust(sessionId: string): TreeCache;
  /** 换后端：代数 +1，旧代数条目全部落空（map 保留，GC 可回收）。 */
  bumpGeneration(): TreeCache;
}

export function createTreeCache(): TreeCache {
  let gen = 0;
  let map: Map<string, ChatTreeResult> = new Map();
  const cache: TreeCache = {
    get(sessionId) {
      if (!sessionId) return null;
      return map.get(`${gen}:${sessionId}`) ?? null;
    },
    set(sessionId, tree) {
      const next = new Map(map);
      next.set(`${gen}:${sessionId}`, tree);
      map = next;
      return cache;
    },
    bust(sessionId) {
      if (!sessionId || !map.has(`${gen}:${sessionId}`)) return cache;
      const next = new Map(map);
      next.delete(`${gen}:${sessionId}`);
      map = next;
      return cache;
    },
    bumpGeneration() {
      gen += 1;
      return cache;
    },
  };
  return cache;
}

export interface SessionTreeApi {
  /** null = 还没有树（老后端 / 加载中 / 空会话）——面板据此回落线性视图。 */
  tree: ChatTreeResult | null;
  /** 上一次拉取失败的原因（人话；null = 正常）。面板显示成一行提示而不是空屏。 */
  error: string | null;
  loading: boolean;
  /** 重新拉树（`loadAll` = 传 max_nodes=0 让后端用缺省上限；后端没有"无上限"）。 */
  refresh: (opts?: { loadAll?: boolean }) => Promise<void>;
  /** 切到此（chat.setLeaf）：该节点留在路径上。返回后端应答（含文件还原结果）。 */
  setLeaf: (seq: number, files?: boolean) => Promise<{ leafSeq: number; removed: number; files?: FileRestoreResult }>;
  /** 是否已加载全部节点（truncated=false 或点过「加载全部」）。 */
  loadedAll: boolean;
  /** 面板要画的行（树 → 行）。**只在这里算一次**——App 的 renderView 被「冻结渲染」
   *  缓存，在那里 useMemo 会把行冻在上一次的值上（切分支之后树不刷新）。 */
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
  /** 每会话的树缓存（语义见 createTreeCache——命中直接用，消灭「空右栏 +
   *  全表扫描往返」；实例随 hook 挂一次）。 */
  const cacheRef = useRef<TreeCache | null>(null);
  if (cacheRef.current === null) cacheRef.current = createTreeCache();
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
      // 成功拉取覆盖缓存（失败不写——见 TreeCache 注释）。
      cacheRef.current!.set(sid, next);
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
      // 最新的"，而不是看到面板突然换了形态）。缓存条目同样保留（同一条纪律）。
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (token === reqRef.current) setLoading(false);
    }
  }, [source, sessionId]);

  // 切会话 / 首次挂载：先查缓存——命中直接用（不再 setTree(null) + 空右栏等一个
  // 全表扫描往返）；未命中 = 旧行为（清空 + 拉）。缓存条目只在成功拉取时写入、
  // 只被四类失效事件清掉（下面订阅）——切回时命中的一定是"该会话最后一次成功拉取"。
  useEffect(() => {
    setError(null);
    setLoadedAll(false);
    const cached = cacheRef.current!.get(sessionId);
    if (cached) {
      setTree(cached);
    } else {
      setTree(null);
      void fetchTree();
    }
  }, [fetchTree]);

  // 树会变的四个时机（**只认本会话**——别的会话的事件与这块面板无关）：
  //   ① chat.leafChanged：leaf 是共享状态，可能别的客户端动的；
  //   ② chat.userMessage（主会话）：新消息落库 → 长出一个新节点，leaf 也跟着走；
  //   ③ chat.done（主轮）：一轮结束，assistant/tool 节点补齐（徽标稳定下来）；
  //   ④ session.changed：元数据变化（含新消息落库后的刷新）。
  // 子轮的事件（带 dispatchId）不触发——它们改的是子会话，主会话的树没变。
  // **缓存失效**：这四类也清掉该会话的缓存条目——事件说树变了，缓存里那份就是旧的
  //（失效枚举 = TREE_BUSTER_EVENTS，与这里的 switch 同源——漏一类就是静默漂移）。
  useEffect(() => {
    return source.subscribe((ev: AgentEvent) => {
      if (!TREE_BUSTER_EVENTS.has(ev.type)) return;
      // 事件归属的会话 = 缓存失效的键：leafChanged/userMessage/done 带 sessionId，
      // sessionChanged 带 id（两个键名不同，在各自分支处删除）。
      if (ev.type === "leafChanged" || ev.type === "userMessage" || ev.type === "done") {
        cacheRef.current!.bust(ev.sessionId);
      }
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
        cacheRef.current!.bust(ev.id);
        if (ev.id === sessionRef.current) void fetchTree();
      }
    });
  }, [source, fetchTree]);

  // 换后端 = 旧连接的树缓存整体作废：连接代数 +1（bumpGeneration——旧代数条目
  // 自然落空；新会话的树进新代数的键）。时机 = connectionChanged true（新连接
  // open 那一刻——与 WSAgent.loadedSessions 的 onHelloReset 同一类「换后端清账」）。
  useEffect(() => {
    return source.subscribe((ev: AgentEvent) => {
      if (ev.type === "connectionChanged" && ev.connected) cacheRef.current!.bumpGeneration();
    });
  }, [source]);

  const setLeaf = useCallback(async (seq: number, files?: boolean) => {
    const r = await source.setLeaf(sessionId, seq, files);
    // 切叶之后树的分支结构/on_path 全变了：重拉一次（setLeaf 内部已重拉历史，
    // 但树不在 chat.history 里——它是独立载荷）。
    await fetchTree({ loadAll: loadedAll });
    return r;
  }, [source, sessionId, fetchTree, loadedAll]);

  const rows = useMemo(() => flattenTree(tree), [tree]);

  return { tree, error, loading, refresh: fetchTree, setLeaf, loadedAll, rows };
}
