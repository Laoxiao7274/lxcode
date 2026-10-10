// 站点数据源：**从后端读、往后端写**（不再是内存 mock）。
//
// 上轮这里是内存单例（刷新即回初始 mock 态）；现在真后端在了，于是：
//   · 官网读 = GET /api/releases?include=revoked（含已撤回历史，草稿不出现）+ /api/changelog；
//   · 管理站读 = GET /api/admin/releases（含草稿，带会话 cookie）；
//   · 写 = 发布 / 撤回 / 日志增删改全部打管理接口，然后**重新拉一遍**（服务端是唯一事实源）。
//
// 形态仍是「模块级单例 + useSyncExternalStore」（与 hash 路由同一套）：
// 跨路由切换不重拉、不丢状态；区别只是数据来自网络而不是内存。
// 前端不再有任何 mock/种子数据：样例数据只在后端（--seed），页面一律读接口。

import { apiGet, apiSend } from "./api/client";
import type { ChangelogEntry, PublishCheck, Release, ReleaseStatus } from "./release";

export interface ApiState {
  releases: Release[];
  changelog: ChangelogEntry[];
  status: "loading" | "ready" | "error";
  /** 数据范围：admin = 带会话读到（含草稿）；public = 只有公开数据 */
  scope: "admin" | "public";
  error: string | null;
}

let state: ApiState = {
  releases: [],
  changelog: [],
  status: "loading",
  scope: "public",
  error: null,
};

const listeners = new Set<() => void>();

export function subscribeStore(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function getStoreSnapshot(): ApiState {
  return state;
}

function commit(next: Partial<ApiState>): void {
  state = { ...state, ...next };
  for (const listener of listeners) listener();
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** 数据范围：官网只要公开数据；管理站（已登录）要含草稿的全量。 */
export type Scope = "public" | "admin";

let scope: Scope = "public";

/** 拉一遍数据。scope=admin 走管理接口（带会话 cookie，含草稿）。 */
export async function reload(next: Scope = scope): Promise<void> {
  scope = next;
  commit({ status: "loading", error: null });
  try {
    const [releases, changelog] = await Promise.all([
      scope === "admin"
        ? apiGet<Release[]>("/api/admin/releases")
        : apiGet<Release[]>("/api/releases?include=revoked"),
      apiGet<ChangelogEntry[]>("/api/changelog"),
    ]);
    commit({ releases, changelog, status: "ready", error: null });
  } catch (error) {
    commit({ status: "error", error: messageOf(error) });
  }
}

/** 发布 / 撤回：打管理接口，然后重拉（服务端是唯一事实源）。失败时把阻断项抛给调用方。 */
export async function setReleaseStatus(version: string, status: ReleaseStatus): Promise<void> {
  const action = status === "published" ? "publish" : "revoke";
  await apiSend<Release>(`/api/admin/releases/${encodeURIComponent(version)}/status`, "POST", {
    status: action,
  });
  await reload();
}

/** 新增日志条目（服务端返回带 id 的条目，列表随之重拉）。 */
export async function addChangelogEntry(entry: Omit<ChangelogEntry, "id">): Promise<void> {
  await apiSend<ChangelogEntry>("/api/admin/changelog", "POST", entry);
  await reload();
}

export async function updateChangelogEntry(id: number, entry: Omit<ChangelogEntry, "id">): Promise<void> {
  await apiSend<ChangelogEntry>(`/api/admin/changelog/${id}`, "PUT", entry);
  await reload();
}

export async function removeChangelogEntry(id: number): Promise<void> {
  await apiSend<null>(`/api/admin/changelog/${id}`, "DELETE");
  await reload();
}

export type { PublishCheck };

// 模块加载即拉一次（首屏就有数据）；失败会落到 state.status="error" 由页面如实显示
void reload();
