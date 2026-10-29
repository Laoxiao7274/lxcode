// backend-remote.ts —— 后端远程访问管理（壳主进程）：token 读取/轮换、开关、
// 局域网地址。后端端点 /remote-access 仅限本机回环（internal/server/remote.go）。
import { networkInterfaces } from "node:os";

const ADDR = process.env.LXCODE_ADDR ?? "127.0.0.1:7789";
const BASE = `http://${ADDR}`;

export interface BackendRemoteState {
  enabled: boolean;
  token: string;
  /** 本机局域网地址（供「局域网直连」模式展示；可能是空串——无可用网卡时）。 */
  lanAddr: string;
}

let cached: BackendRemoteState | null = null;

function lanAddress(): string {
  for (const list of Object.values(networkInterfaces())) {
    for (const ni of list ?? []) {
      if (ni.family === "IPv4" && !ni.internal) {
        return `${ni.address}:${ADDR.split(":").pop()}`;
      }
    }
  }
  return "";
}

async function call(method: "GET" | "POST", body?: unknown): Promise<BackendRemoteState> {
  const res = await fetch(`${BASE}/remote-access`, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    signal: AbortSignal.timeout(5_000),
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(text.trim() || `后端 /remote-access 返回 HTTP ${res.status}`);
  }
  const ra = (await res.json()) as { enabled: boolean; token: string };
  cached = { enabled: ra.enabled, token: ra.token, lanAddr: lanAddress() };
  return cached;
}

/** 启动时拉一次（窗口加载前调用——渲染层要同步取 token 拼 WS URL）。 */
export async function initBackendRemote(): Promise<void> {
  try {
    await call("GET");
  } catch (e) {
    console.log(`[shell] 远程访问状态获取失败（可能后端未就绪）: ${e instanceof Error ? e.message : String(e)}`);
  }
}

/** 渲染层同步取 token（preload sendSync 用——WS 连接 URL 要在创建时拼好）。 */
export function cachedToken(): string | null {
  return cached?.token ?? null;
}

export function cachedState(): BackendRemoteState | null {
  return cached;
}

export async function enable(): Promise<BackendRemoteState> {
  return call("POST", { enabled: true });
}

export async function disable(): Promise<BackendRemoteState> {
  return call("POST", { enabled: false });
}

export async function rotate(): Promise<BackendRemoteState> {
  return call("POST", { rotate: true });
}

export async function refresh(): Promise<BackendRemoteState> {
  return call("GET");
}
