// 搜索渠道的纯判定（面板与测试共用）。
//
// 放在 shared/ 而不是组件文件里：这两个判定是**纯函数**，而组件文件一 import
// 就拉进 React/gsap 整棵树，纯函数测试没法只测逻辑（项目惯例：纯逻辑进
// shared/，node:test 钉住）。
import type { SearchChannel } from "./types";

/** 面板是否给出 API Key 输入框。
 *
 * 「接受 key」与「需要 key 才就绪」是两件事：Exa 缺 key 也就绪（走免配置
 * 通道），但有 key 走直连 API——只看 needs_key 会把它的输入框藏掉，用户
 * 永远进不了直连路径。
 *
 * 回落是给老后端的：未热重载的后端不返回 accepts_key（AGENTS.md §5 坑 11），
 * 此时按 needs_key 处理，行为与加这个字段之前完全一致。 */
export function acceptsKey(c: Pick<SearchChannel, "needs_key" | "accepts_key">): boolean {
  return c.accepts_key ?? c.needs_key;
}

/** 磁盘上是否真的存了用户配置（决定「清除」按钮该不该出现）。
 *
 * 优先用后端的 `stored`（「配置文件里存在该条目」）——这是唯一精确的判据。
 *
 * 为什么不能用别的：
 *   - `configured` 不行：Exa 这类刻意默认就绪的渠道不填任何东西也是就绪的，
 *     于是「清除」按钮出现在一张空卡片上（点了什么都不会发生——后端删一个
 *     不存在的条目是幂等空操作）；
 *   - 「options 有没有值」也不行：后端给的是**已解析**的取值（配置 → 环境变量
 *     → 默认值），Exa 的 mcp_url 不填也有默认值，照样误判成「配过」。
 *     （这个误判真的发生过，被 CDP 截图自查抓到。）
 *
 * 回落是给老后端的（同上，§5 坑 11）：没有 stored 时按「值存在且不等于声明
 * 的默认值」判断——比只看 api_key/base_url 更接近真相（Brightdata 只填 zone
 * 也是配过）。 */
export function hasStoredConfig(c: SearchChannel): boolean {
  if (c.stored !== undefined) return c.stored;
  if ((c.api_key ?? "") !== "" || (c.base_url ?? "") !== "") return true;
  return (c.option_specs ?? []).some((spec) => {
    const v = (c.options?.[spec.key] ?? "").trim();
    return v !== "" && v !== (spec.default ?? "");
  });
}
