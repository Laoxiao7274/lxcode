// 展示映射：机器可读值（published / stable / features）→ 中文文案与既有视觉原语。
//
// 为什么集中放一处：状态、渠道、分类的文案在首页、下载页、后台三处都要出现，
// 分散写就会出现「同一状态两种叫法」。配色只指 base.css 里已有的原语
// （.pill / .pill-ok / .pill-warn / .pill-danger / .tag-*），这里不新造样式。
//
// 本文件是纯函数/常量（无 React），因此可被测试钉住：每个分类都必须有标签配色。

import type { Channel, ChangelogEntry, ChangelogKind, ReleaseStatus } from "./release";

export const STATUS_PILL: Record<ReleaseStatus, { cls: string; label: string }> = {
  published: { cls: "pill pill-ok", label: "已发布" },
  draft: { cls: "pill pill-warn", label: "草稿" },
  revoked: { cls: "pill pill-danger", label: "已撤回" },
};

export const CHANNEL_PILL: Record<Channel, { cls: string; label: string }> = {
  stable: { cls: "pill", label: "stable" },
  beta: { cls: "pill pill-warn", label: "beta" },
};

export const KIND_TAG: Record<ChangelogKind, { cls: string; label: string }> = {
  features: { cls: "tag tag-feat", label: "新功能" },
  fixes: { cls: "tag tag-fix", label: "修复" },
  breaking: { cls: "tag tag-breaking", label: "破坏性变更" },
  docs: { cls: "tag tag-docs", label: "文档" },
};

/** 分组展示顺序：破坏性变更排最前（升级前最该看见的就是它），其余按开发习惯。 */
export const KIND_ORDER: ChangelogKind[] = ["breaking", "features", "fixes", "docs"];

/** ISO 时间 → 日期（表格列宽有限，时刻放进 title）。 */
export function dateOf(iso: string | null): string {
  return iso ? iso.slice(0, 10) : "—";
}

/** 把一个版本的日志按分类分组，组内保持原顺序，空组不返回。 */
export function entriesByKind(
  entries: ChangelogEntry[],
): Array<{ kind: ChangelogKind; items: ChangelogEntry[] }> {
  return KIND_ORDER.map((kind) => ({ kind, items: entries.filter((e) => e.kind === kind) })).filter(
    (group) => group.items.length > 0,
  );
}
