// 会话标签条的纯规则（组件只管渲染与动效，规则放这里才能测）。
//
// 抽出来的直接原因：两个真实缺陷都出在「关标签」这一条路径上——
//   ① 关掉当前标签时组件调了「新对话」，那会**真的在后端建一个新会话**，
//      于是"关一个就冒出一个"；
//   ② 新会话被追加进 order，又按容量顶掉最老的标签，于是"关掉新会话，
//      别的标签也没了"。
// 规则挪到这里之后，①的判据（关掉当前标签后交给谁）与②的判据（追加/容量）
// 都能被测试钉住，而不是埋在组件的 onClick 里。
import type { SessionMeta } from "./types";

/** 标签条容量：最多同时显示的标签数（超出丢最老的——浏览器同款）。 */
export const SESSION_TAB_LIMIT = 8;

/** 首次拿到会话列表铺开标签：后端序是最近在前 → 反转成「新的在右」。 */
export function seedSessionTabs(sessions: SessionMeta[], limit: number = SESSION_TAB_LIMIT): string[] {
  return sessions
    .filter((s) => !s.archived)
    .slice(0, limit)
    .map((s) => s.id)
    .reverse();
}

/** 把一个会话追加到标签条右侧；已在其中则原样返回（点标签只切焦点，绝不重排）。
 *  超出容量丢最老的。 */
export function appendSessionTab(order: string[], id: string, limit: number = SESSION_TAB_LIMIT): string[] {
  if (!id || order.includes(id)) return order;
  return [...order.slice(-(limit - 1)), id];
}

/** 可见标签：按打开顺序，剔除归档与用户关掉的。
 *
 *  **用户显式关掉的标签立刻消失**，连当前会话也不例外。原来的写法把"当前会话恒显示"
 *  放在最前，于是关掉当前标签后它还被强行留在标签条上，一直留到 resumeSession 走完
 *  一个 WS 往返、焦点真正移走为止——用户看到的就是"点了 × 没反应，过一会儿才一下全变"，
 *  也就是那一下"卡"。关标签是用户的明确意图，不该为"标签条始终指着当前会话"让路。
 *
 *  "当前会话恒显示"仍然保留，但它管的是**另一种**情况：当前会话被容量挤出 order
 *  （或还没进 order），那时标签条必须仍然指着它，否则会出现"内容是一个会话、
 *  标签条上找不到它"。 */
export function visibleSessionTabs(
  order: string[],
  sessions: SessionMeta[],
  closed: ReadonlySet<string>,
  currentId: string,
): string[] {
  const visible = order.filter((id) => {
    if (closed.has(id)) return false;
    if (id === currentId) return true;
    const meta = sessions.find((s) => s.id === id);
    return Boolean(meta && !meta.archived);
  });
  // 当前会话可能已被容量挤出 order（它不是被用户关掉的）——它必须仍然出现在标签条上，
  // 否则会出现"对话区是一个会话、标签条上却找不到它"。追加到最右（最近聚焦的位置）。
  if (currentId && !closed.has(currentId) && !visible.includes(currentId)) {
    return [...visible, currentId];
  }
  return visible;
}

/** 把已经"死掉"的标签从 order 里剔掉（归档、用户关掉的）。
 *  不剔的话它们会继续占容量：下次追加新标签时，容量满丢最老丢掉的会是**活**标签
 *  ——用户看到的是"我明明只关了一个标签，另一个却不见了"。这就是同一类症状的第二处来源。
 *  sessions 为空（列表还没到）时原样返回：那时判不出死活，剔了会把整条标签栏清空。 */
export function pruneSessionTabs(
  order: string[],
  sessions: SessionMeta[],
  closed: ReadonlySet<string>,
  currentId: string,
): string[] {
  if (sessions.length === 0) return order;
  const keep = new Set(visibleSessionTabs(order, sessions, closed, currentId));
  const next = order.filter((id) => keep.has(id));
  return next.length === order.length ? order : next;
}

/** 关掉**当前**标签后焦点交给谁：标签条里最近打开的那个（最右——新标签追加在右）。
 *  一个都不剩时返回空串：调用方保持现状（最后一个标签关不掉），**绝不新建会话**。
 *  新建会话看起来像"顺手补一个"，实际会顶掉最老的标签（容量满丢最老），
 *  用户看到的就是"关一个冒一个，别的标签还一起没了"。 */
export function sessionTabFallback(visible: string[], closingId: string): string {
  const rest = visible.filter((id) => id !== closingId);
  return rest.length > 0 ? rest[rest.length - 1] : "";
}
