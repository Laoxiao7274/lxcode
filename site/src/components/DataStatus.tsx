// 数据状态提示：载入中 / 载入失败（读后端失败时如实说明，不静默显示空列表）。
//
// 为什么要有它：数据现在来自后端，网络断了/后端没起 与 「真的没有版本」 是两件事——
// 前者要告诉人「载入失败」，后者才是空状态。混在一起会让人以为站点数据丢了。

import type { ApiState } from "../shared/store";

export function DataStatus({ state }: { state: ApiState }) {
  if (state.status === "ready") return null;
  if (state.status === "loading") {
    return <p className="muted small">正在从后端载入数据…</p>;
  }
  return <p className="err small">载入失败：{state.error ?? "未知错误"}</p>;
}

/** 管理页的数据范围提示：没配 token（或 token 不对）时只看到公开数据，要如实说。 */
export function AdminScopeNote({ state }: { state: ApiState }) {
  if (state.status === "loading") {
    return <p className="muted small">正在从后端载入数据…</p>;
  }
  if (state.status === "error") {
    return <p className="err small">载入失败：{state.error ?? "未知错误"}</p>;
  }
  if (state.scope === "admin") return null;
  return (
    <p className="inline-note">
      <span className="pill pill-warn">只读公开数据</span>
      <span className="meta">
        {state.error ?? "未配置管理 token：草稿与撤回操作不可见。在下面填入后端启动时的 --token 即可看到全部版本。"}
      </span>
    </p>
  );
}
