// 系统提示条（notice）的前缀 → 标签表：一条 **user 角色**消息只要以某个前缀开头，
// 就不是用户说的话——渲染成提示条而不是用户气泡。
//
// 为什么需要这张表：这类消息（后台任务通告 / 重复调用提醒）在历史与实时流里都是
// **真实 user 角色消息**（模型必须把它当用户回合才会回应，这是后端的硬约束），所以
// 前端**只能按文本前缀识别**，不能按角色判断。判定原先写死在 jobs.ts（只有通告一种），
// 每加一类就要再抄一遍 startsWith + slice，漏一处就有一类消息变成用户气泡——用户会
// 看到一句自己没说过的话挂在自己的气泡里（以为会话被串了）。表化之后新增一类只加一行。
//
// 与 mcp-status.ts 同款：纯函数才能被 node:test 直接钉住，组件不重复实现判定。
import { JOB_NOTICE_PREFIX, REPEAT_NOTICE_PREFIX } from "./jobs";

/** 提示条种类表：prefix 与 Go 侧常量**逐字一致**（含尾空格）。
 *
 *  **顺序敏感**：匹配取**第一个**命中的前缀，所以前缀之间若有包含关系（如将来
 *  「[重复调用提醒] 」与「[重复调用提醒-硬] 」），更长/更具体的必须排在前面——
 *  否则短前缀会先把长前缀的消息吞掉，标签张冠李戴。 */
export const NOTICE_KINDS: Array<{ prefix: string; label: string }> = [
  { prefix: JOB_NOTICE_PREFIX, label: "后台任务通告" },
  { prefix: REPEAT_NOTICE_PREFIX, label: "重复调用提醒" },
];

/** 按表顺序找命中的种类（找不到 = null）。 */
function matchKind(text: string): { prefix: string; label: string } | null {
  for (const kind of NOTICE_KINDS) {
    if (text.startsWith(kind.prefix)) return kind;
  }
  return null;
}

/** 提示条标签（渲染层显示在前缀位置）；没命中任何前缀 = null（普通用户消息）。 */
export function noticeLabel(text: string): string | null {
  return matchKind(text)?.label ?? null;
}

/** 提示条正文（去掉前缀 + trim——前缀由渲染层作为标签单独显示）；
 *  没命中 = 原样返回（调用方可以把它当普通文本直接用）。 */
export function noticeBody(text: string): string {
  const kind = matchKind(text);
  return kind ? text.slice(kind.prefix.length).trim() : text;
}
