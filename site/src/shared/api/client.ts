// 站点后端的 API 客户端（会话版）。
//
// 管理站用 **HttpOnly 会话 cookie**：登录后浏览器自动带上，JS 读不到会话 id
//（XSS 也偷不走）；脚本 / CI 仍可用 Authorization: Bearer <token>（后端两条通道都接受）。
// 子路径部署（vite base）由 SITE_BASE 统一补前缀，见 shared/base.ts。
//
// 为什么不再有 localStorage token：上轮让人手贴长期 token 存 localStorage，
// 既能被 XSS 读走、又不会过期；会话 cookie 是同一件事的安全做法。

import { SITE_BASE } from "../base";
import type { PublishCheck } from "../release";

/** 接口错误：status 给页面判断（401 = 未登录/密码错；429 = 限流；400 = 校验阻断项）。 */
export class ApiError extends Error {
  readonly status: number;
  readonly checks: PublishCheck[] | null;

  constructor(status: number, message: string, checks: PublishCheck[] | null) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.checks = checks;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

async function request<T>(path: string, init: RequestInit): Promise<T> {
  let response: Response;
  try {
    response = await fetch(SITE_BASE + path, {
      ...init,
      // 会话 cookie 必须显式带上：跨站/子路径部署时默认值不一定够
      credentials: "same-origin",
    });
  } catch (cause) {
    throw new ApiError(0, `连不上后端（${path}）：${String(cause)}`, null);
  }
  const text = await response.text();
  let body: unknown = null;
  if (text !== "") {
    try {
      body = JSON.parse(text);
    } catch {
      body = null;
    }
  }
  if (!response.ok) {
    const message =
      isRecord(body) && typeof body.error === "string"
        ? body.error
        : `请求失败（HTTP ${response.status}）`;
    const checks =
      isRecord(body) && Array.isArray(body.checks) ? (body.checks as PublishCheck[]) : null;
    throw new ApiError(response.status, message, checks);
  }
  return body as T;
}

export function apiGet<T>(path: string): Promise<T> {
  return request<T>(path, { method: "GET" });
}

export function apiSend<T>(path: string, method: string, body?: unknown): Promise<T> {
  return request<T>(path, {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body ?? {}),
  });
}

export function apiUpload<T>(path: string, form: FormData): Promise<T> {
  return request<T>(path, { method: "POST", body: form });
}

/** 登录：密码 = 后端启动时配的凭据。401 密码错、429 尝试过多。 */
export async function login(password: string): Promise<void> {
  await apiSend<{ ok: boolean }>("/api/admin/login", "POST", { password });
}

/** 退出登录：销毁服务端会话（同时清 cookie）。 */
export async function logout(): Promise<void> {
  await apiSend<{ ok: boolean }>("/api/admin/logout", "POST");
}

/** 会话探测：管理站用它决定「显示登录页还是管理台」。 */
export async function fetchSession(): Promise<boolean> {
  try {
    await apiGet<{ authenticated: boolean }>("/api/admin/me");
    return true;
  } catch (error) {
    if (error instanceof ApiError && (error.status === 401 || error.status === 503)) {
      return false;
    }
    throw error;
  }
}
