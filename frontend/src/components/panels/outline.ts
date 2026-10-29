// 「已发送消息」大纲的纯函数（**不 import React**——面板组件与 node:test 共用
// 同一份判定，测试不必拉起渲染层）。
//
// 为什么只认 kind === "user"：提示条（kind === "notice"）在**历史里也是真实 user
// 角色消息**（后端必须把它当用户回合，模型才会回应它——见 shared/blocks.ts 的
// notice 分支注释），但它不是用户说的话。把它列进「我发过的消息」是错的——用户会
// 看到一条自己没发过的消息，以为会话被串了。这个仓库已经踩过这个坑
// （tests/notices.test.mjs 的两条回归钉子）。
//
// 判定必须落在**块的 kind**上，不能靠角色、也不能靠文本前缀猜：kind 是归约器
// （reduce.ts）已经做完的判定，前缀表只属于 notices.ts 一家。
import type { ThreadBlock } from "../../shared/blocks";

export interface OutlineItem {
  /** 块的唯一序号——跳转锚点（Thread 给每个块挂 data-uid，App 按它 querySelector）。 */
  uid: number;
  /** 后端历史序号（撤回锚点）。老后端 / 更早落库的历史没有它——没有也照样能跳。 */
  seq?: number;
  /** 原文（**不截断**：展示宽度是渲染层的事，纯函数不预设侧栏有多宽）。 */
  text: string;
  /** 在时间线块列表里的下标（窗口化渲染/对账时用得着）。 */
  index: number;
}

/** 块列表 → 大纲条目：**只取 user 块**（kind === "user"）。
 *
 *  顺带说明为什么不能「取所有带 text 的块」：notice 也带 text（提示条正文），
 *  assistant 带 content 而不是 text，confirm/tool 的正文在别的字段里——按「有没有
 *  text」筛会同时漏掉该有的、混进不该有的。只有 kind 是准确判据。 */
export function outlineItems(blocks: ThreadBlock[]): OutlineItem[] {
  const items: OutlineItem[] = [];
  blocks.forEach((block, index) => {
    if (block.kind !== "user") return;
    items.push({ uid: block.uid, seq: block.seq, text: block.text, index });
  });
  return items;
}

/** 单行摘要：折叠换行 + 超长截断（侧栏不是用来读长文的）。
 *
 *  换行按**空白折叠**（\s+ → 单个空格）而不是只换 \n：制表符与连续空格同样会
 *  把一行撑得难看。截断按**码点**（[...text]）而不是 str.slice——emoji 与其它
 *  代理对从中间切开就是半个字符（渲染成方块）。 */
export function outlineLabel(text: string, max = 40): string {
  const one = text.replace(/\s+/g, " ").trim();
  const chars = [...one];
  if (chars.length <= max) return one;
  return chars.slice(0, max).join("") + "…";
}
