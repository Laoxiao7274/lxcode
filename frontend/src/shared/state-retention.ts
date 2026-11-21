// 会话状态表的保留策略（OOM 三件套 S3）：sessionStates / assistantStates 两个
// 键值表原先**只增不删**（reduce.ts 的 reduceSessionStates 只写键、store.ts 的
// reset 只在换数据源时清表）——每条 state 携带完整 blocks 时间线（大块对象），
// 长会话 × 多会话的使用形态下整页内存只进不出。
//
// 策略：**LRU + 数量上限 + 时长上限**（对齐任务约束「LRU + 数量上限」的三参数
// 形态）——
//   - 数量：超过 SESSION_STATES_MAX 个整体删除最久未活跃的；
//   - 时长：超过 SESSION_STATES_TTL_MS 无活动（既没聚焦也没收到该会话事件）
//     的条目删除；
//   - 活跃时间戳 = 该会话最后一次**事件到达**（sessionFocused / 任何带
//     sessionId 的事件都会刷新，见 store.ts 的 handleAgentEvent）。
//
// **淘汰 = 骨架化而非整体删除**（2026-12 会话切换优化批 4）：被淘汰条目保留
// 一份轻量元数据（busy/sending/compacting/model/approval/todos + lightweight
// 标志），时间线丢弃——内存收益不变（大头是 blocks），但用户切回被淘汰会话时
// 不再看到「点开空会话」的空窗（EmptyState 闪帧）：侧栏元数据立即正确、时间线
// 直接进装载指示、后台拉 tail。骨架不进任何 keep 豁免；再次超时/超上限时骨架
// 整体移除（回到无 state 形态，重进仍走全量装载）。
//
// 淘汰的三条纪律：
//   ① **豁免优先**：keep 集合（当前活跃会话、打开的标签、AssistantWorkspace 的
//     常驻挂载层）与「忙着的会话」（busy/pending/streaming/sending——淘汰会让
//     在跑的会话失去增量流的落点，时间线从那一刻起是半截的）永不淘汰；
//   ② **配套清理一次做完**：childLinks 两张映射（childParents / dispatchChild）
//     里父子任一端被淘汰的条目一并清（workspace-tabs.ts 注释自认「只增不删」的
//     两张映射）；prevBusy / prevCompacting / sendQueues 的对应键清（per-session
//     键值表的卫生）；清不干净的下场是它们自己变成下一处只增不删的泄漏；
//   ③ **恢复走既有装载路径**：被淘汰的会话重进时——助手域 AssistantPage 的
//     boot（骨架 historyReady=false → assistant.history 重拉）、子会话视图的
//     装载 effect（childHistory）、主域走 resumeSession（store 淘汰时经 source
//     的 forgetLoadedSessions 把 loadedSessions 记忆一并清掉 → 全量装载）——
//     骨架态下各装载闸门照常触发（historyReady=false 就是它们的判据）。

/** 数量上限：states 表最多保留多少个会话的完整状态（含 blocks 时间线）。
 *  2026-12 会话切换优化批 4：24 → 48（用户会话多，此前会话频繁点开时 LRU
 *  过早把还能秒回的条目挤掉），且淘汰不再是整体删除而是**骨架化**（见
 *  skeletonStateOf——留下的骨架只占一份元数据，真正的 blocks 时间线才占内存），
 *  上限放宽的成本很低。取值仍是助手标签容量（SESSION_TAB_LIMIT=8）的 6 倍余量。 */
import type { UIState } from "./blocks";

export const SESSION_STATES_MAX = 48;

/** 时长上限：多久没有活动（聚焦/事件）的会话状态可淘汰。 */
export const SESSION_STATES_TTL_MS = 24 * 60 * 60 * 1000;

/** 一条 state 是否「忙着」——忙着的不淘汰（增量流需要落点，见文件头注释①）。 */
export function isBusyState(state: { busy?: boolean; pending?: unknown; streaming?: unknown; sending?: boolean } | undefined): boolean {
  if (!state) return false;
  return Boolean(state.busy || state.pending || state.streaming || state.sending);
}

/** 淘汰判定（纯函数，store 与测试共用一份）。
 *
 *  states / active 都是「会话 id → 条目」的键值表（active 是最后活跃毫秒时间
 *  戳）；keep 是豁免集合（当前活跃会话、打开的标签、挂载层——调用方拼装）。
 *  返回被淘汰的会话 id 列表（**不淘汰时为空数组**——调用方据此跳过配套清理，
 *  绝不产生无意义的引用变更）。
 *
 *  顺序：先按时长（超 TTL 的全部淘汰，忙着的除外），再按数量（仍超上限时从
 *  最久未活跃开始淘汰，豁免与忙着的除外）。 */
export function evictableSessionIds(
  states: Record<string, unknown>,
  active: Record<string, number>,
  opts: { now: number; keep?: ReadonlySet<string>; max?: number; ttlMs?: number },
): string[] {
  const keep = opts.keep ?? new Set<string>();
  const max = opts.max ?? SESSION_STATES_MAX;
  const ttlMs = opts.ttlMs ?? SESSION_STATES_TTL_MS;
  const busyIds = new Set(
    Object.entries(states)
      .filter(([, s]) => isBusyState(s as Parameters<typeof isBusyState>[0]))
      .map(([id]) => id),
  );
  const evictable = Object.keys(states).filter((id) => !keep.has(id) && !busyIds.has(id));
  const out: string[] = [];
  // 时长：超 TTL 的全部淘汰（豁免/忙着的除外——没有活跃记录的条目视为刚活跃，
  // 交给数量上限兜底，不猜时间）。
  for (const id of evictable) {
    const at = active[id];
    if (at !== undefined && opts.now - at > ttlMs) out.push(id);
  }
  const rest = evictable.filter((id) => !out.includes(id));
  // 数量：仍超上限时从最久未活跃开始淘汰（没有记录的视为最旧——它们连活跃都
  // 没刷新过一次，是冷条目）。上限是「可淘汰条目数」的口径：keep 豁免与忙着的
  // 条目不占额度（它们的多少不该决定别的会话被淘汰多少）。
  const over = rest.length - max;
  if (over > 0) {
    const byOldest = [...rest].sort((a, b) => (active[a] ?? 0) - (active[b] ?? 0));
    out.push(...byOldest.slice(0, over));
  }
  return out;
}

/** 淘汰后刷新活跃表（不可变——删掉被淘汰条目，其余原样）。 */
export function pruneActivity(
  active: Record<string, number>,
  evicted: readonly string[],
): Record<string, number> {
  if (evicted.length === 0) return active;
  const next = { ...active };
  for (const id of evicted) delete next[id];
  return next;
}

/** 刷新一条会话的活跃时间戳（不可变——不活跃返回原对象，避免无谓的重渲染）。
 *
 *  **时间戳单调化**（at 至少比该会话的上次活跃晚 1ms）：LRU 淘汰在毫秒粒度
 *  上比较，同一毫秒内的两次刷新比较不出先后——排序就只能靠键插入序（任意）。
 *  单调化保证「刚被这条事件活跃的会话」严格比表内其他条目新，不会被同毫秒
 *  平局挤出上限。 */
export function touchActivity(
  active: Record<string, number>,
  id: string,
  at: number,
): Record<string, number> {
  if (!id) return active;
  const monotonic = Math.max(at, (active[id] ?? 0) + 1);
  return active[id] === monotonic ? active : { ...active, [id]: monotonic };
}

/** 刷一批会话的活跃时间戳（keep 集合挂进来的入口——AssistantWorkspace 的
 *  常驻挂载层、App 的打开标签都以批为单位保留）。 */
export function touchActivityMany(
  active: Record<string, number>,
  ids: readonly string[],
  at: number,
): Record<string, number> {
  const changed = ids.filter((id) => id && active[id] !== at);
  if (changed.length === 0) return active;
  const next = { ...active };
  for (const id of changed) next[id] = at;
  return next;
}

/** 会话状态表淘汰后的配套清理（workspace-tabs.ts 的 ChildLinks 纯函数口径）。
 *
 *  两张映射（childParents：子→父；dispatchChild：dispatch id → 子会话 id）里
 *  「子会话被淘汰」或「父会话被淘汰」的条目一并清：子被淘汰 = 子视图重进时走
 *  childHistory 重新装载（回放扫描会重新记映射——recordChildLink 幂等）；父被
 *  淘汰 = 父重进时全量装载、回放扫描重建它派发的全部映射。清不干净的代价是
 *  映射自己变成只增不删的第二处泄漏。 */
export function pruneChildLinksFor(
  links: { childParents: Record<string, string>; dispatchChild: Record<string, string> },
  evicted: readonly string[],
): { childParents: Record<string, string>; dispatchChild: Record<string, string> } {
  if (evicted.length === 0) return links;
  const gone = new Set(evicted);
  const childParents = Object.fromEntries(
    Object.entries(links.childParents).filter(([child, parent]) => !gone.has(child) && !gone.has(parent)),
  );
  // dispatchChild 的值（子会话）被淘汰时清掉——双投路由查不到就回落全量反查
  //（reduce.ts 的 dispatchChildSession 兜底路径），语义安全。
  const dispatchChild = Object.fromEntries(
    Object.entries(links.dispatchChild).filter(([, child]) => !gone.has(child)),
  );
  return { childParents, dispatchChild };
}

/** per-session 键值表的配套清理（prevBusy / prevCompacting / sendQueues 同款）。
 *  通用形态：删掉被淘汰会话的键，其余原样；无淘汰返回原引用。 */
export function pruneSessionKeys<T>(
  table: Record<string, T>,
  evicted: readonly string[],
): Record<string, T> {
  if (evicted.length === 0) return table;
  const next = { ...table };
  for (const id of evicted) delete next[id];
  return next;
}

/** 把一条完整 state **骨架化**（2026-12 会话切换优化批 4）：丢弃 blocks 时间线
 *  （内存大头）与随它派生的展示值（streaming/分页元数据/占用/统计/操作错误），
 *  保留会话级元数据（busy/sending/compacting/model/approval/todos）并打上
 *  `lightweight` 标志。切回时侧栏的忙闲/模型/权限档立即正确，时间线进入
 *  「正在载入会话」而不是先闪一帧 EmptyState（详见 blocks.ts 的 lightweight 注）。
 *
 *  **豁免语义不变**：isBusyState 的四项在骨架里原样保留——忙碌的会话在淘汰判定
 *  就被豁免，骨架里只可能出现 false（或 null pending——同属豁免前的检查对象，
 *  保留是为了防御未来口径变化）。骨架**不进 keep**（store.ts 不为它加豁免）：
 *  它是淘汰的产物，再豁免就永不淘汰；超 TTL / 超上限时骨架整体移除（回到
 *  「无 state」形态，重进同样走全量装载，只是少了元数据，无害）。
 *
 *  纯函数：测试直测「块被丢弃、元数据保留、标志打上、无块时返回原引用」。 */
export function skeletonizeState(prev: UIState | undefined): UIState | undefined {
  if (!prev) return prev;
  const slim: UIState = {
    ...prev,
    blocks: [],
    streaming: null,
    pending: null,
    historyMore: false,
    historyFirstSeq: 0,
    context: null,
    stats: null,
    operationError: null,
    historyReady: false,
    rebuiltGen: (prev.rebuiltGen ?? 0) + 1,
    lightweight: true,
  };
  // 没有时间线可丢（已是骨架/刚建的默认态）：丢弃不为它产生任何收益——返回
  // 原引用，调用方据此跳过无意义的条目替换（引用不变 = 不触发重渲染）。
  return (prev.blocks?.length ?? 0) === 0 && !prev.streaming && prev.historyReady === false
    ? prev
    : slim;
}
