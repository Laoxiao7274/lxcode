// sakura-nodes.ts —— 樱花frp 节点选择领域逻辑（纯函数 + 可注入依赖，便于单测）。
//
// 拆出来的理由：节点判定原先内嵌在 pickNode 里（sakura.ts），「列表能选」与
// 「自动选」要共用同一份判定——两份判定的漂移代价是：UI 里能点的节点创建时
// 被拒，或者自动选挑中一个 UI 标着"不可用"的节点。
//
// 延迟探测：樱花 API **没有延迟字段**，官方 FAQ 也明确节点不响应 ICMP——只能
// 自己测。这里用 TCP connect 到节点的 **frps 控制端口 7000**：
//   · 依据一：natfrp 的 frpc 走 `server_port = 7000`（官方 frpc 配置的默认
//     控制端口），frpc 日志「正在连接节点 [<host>, tcp]」之后即建立控制连接；
//   · 依据二（2026-10 本机实测）：frp-fly.com / frp-act.com 的 7000 均可
//     connect 成功（285ms / 94ms），而这两台节点的 80 端口连不上——说明 7000
//     是节点上真开着的服务端口，不是随便什么端口的巧合；
//   · 为什么不用 8880（节点面板 HTTP）：部分节点没开面板端口（natfrp.com 的
//     8880 通、但它是门户站不是节点），且面板延迟包含 HTTP 处理时间，不如
//     控制端口的 TCP 握手 RTT 干净。
// 注意：/nodes 里同一主机名可能被多个节点共用（如 frp-fly.com），所以缓存键
// 是 host（不是节点 id）——同一主机的 RTT 本来就是一回事。
import { connect } from "node:net";

/** 探测端口：frps 控制端口（见文件头注的依据）。 */
export const PROBE_PORT = 7000;
/** 单次探测超时：超过就当"测不出"（跨境节点常见 200-400ms，1.5s 足够）。 */
export const PROBE_TIMEOUT_MS = 1500;
/** 并发上限：70 个节点一起打会把自己家的 NAT 会话表打满，也容易触发限流。 */
export const PROBE_CONCURRENCY = 6;
/** 结果缓存：5 分钟内不重复探测（避免面板反复打开时刷屏式 connect）。 */
export const PROBE_CACHE_TTL_MS = 5 * 60_000;

/** /nodes 的原始形状（只列用到的字段）。 */
export interface RawNode {
  name?: string;
  host?: string;
  vip?: number;
  flag?: number;
  description?: string;
}

/** 一个候选节点（列表展示与自动选共用这一份形状）。 */
export interface NodeCandidate {
  id: number;
  name: string;
  /** frps 主机名（空串 = API 没给地址，无法组隧道地址也无法测延迟）。 */
  host: string;
  vip: number;
  flag: number;
  /** 负载百分比（/node/stats 拿不到时为 null——**不要**强转成 0）。 */
  load: number | null;
  /** 能否用于创建隧道（离线/等级不够/无地址 → false）。 */
  usable: boolean;
  /** 不可用原因（可用时为 null）——UI 就着它显示「需更高等级」「离线」。 */
  reason: string | null;
}

/** flag bit2 = 允许创建隧道；bit9 = 节点离线。 */
const FLAG_ALLOW_CREATE = 1 << 2;
const FLAG_OFFLINE = 1 << 9;

/** 单节点可用性判定（唯一的判定源：candidateNodes 与自动选都走这里）。 */
export function nodeUnusableReason(n: RawNode, groupLevel: number): string | null {
  if (typeof n.flag !== "number") return "节点状态未知";
  if ((n.flag & FLAG_OFFLINE) !== 0) return "离线";
  if ((n.flag & FLAG_ALLOW_CREATE) === 0) return "不可创建隧道";
  if ((n.vip ?? 0) > groupLevel) return "需更高等级";
  if (!n.host) return "节点地址缺失";
  return null;
}

/**
 * 候选节点列表：/nodes 的**全量**节点都进来（不可用的也进——UI 要按原因
 * 置灰展示，用户才知道"为什么这台点不了"），每个带 usable/reason；
 * 自动选只从 usable 里挑。
 *
 * 排序：可用在前 → 负载升序（未知负载排后）→ id 升序（结果稳定可复现）。
 */
export function candidateNodes(
  nodes: Iterable<[number, RawNode & { load?: number | null }]>,
  groupLevel: number,
): NodeCandidate[] {
  const out: NodeCandidate[] = [];
  for (const [id, n] of nodes) {
    const reason = nodeUnusableReason(n, groupLevel);
    out.push({
      id,
      name: n.name ?? String(id),
      host: n.host ?? "",
      vip: n.vip ?? 0,
      flag: typeof n.flag === "number" ? n.flag : 0,
      load: typeof n.load === "number" ? n.load : null,
      usable: reason === null,
      reason,
    });
  }
  return out.sort((a, b) => {
    if (a.usable !== b.usable) return a.usable ? -1 : 1;
    const la = a.load ?? Number.POSITIVE_INFINITY;
    const lb = b.load ?? Number.POSITIVE_INFINITY;
    if (la !== lb) return la - lb;
    return a.id - b.id;
  });
}

// ---- 隧道 extra（ini 风格键值串）—— auto_https 自愈 ----
//
// 背景（docs/sakurafrp-integration.md §3）：内地节点对 TCP 隧道上的 HTTP
// （WS 升级）流量回 501（官方 FAQ 建站条件表：内地节点 + TCP + HTTP 流量
// = 需配置自动 HTTPS）。extra 是 /tunnels 与 /tunnel/edit 都接受的 ini
// 风格键值串（多行 `key = value`），官方示例即含 `auto_https = auto`。

/** 我们的隧道统一带的 extra 值（创建时直接用；passthrough 模式待真机验证后再加）。 */
export const AUTO_HTTPS_EXTRA = "auto_https = auto";

/** 解析 extra 串为键值表：key 小写归一、值去空白；注释行（# ;）与空行忽略。 */
export function parseExtra(extra: unknown): Map<string, string> {
  const out = new Map<string, string>();
  if (typeof extra !== "string") return out;
  for (const line of extra.split(/\r?\n/)) {
    const t = line.trim();
    if (t === "" || t.startsWith("#") || t.startsWith(";")) continue;
    const eq = t.indexOf("=");
    if (eq <= 0) continue; // 没有键或键为空——畸形行丢弃，不抛（extra 是外部数据）
    out.set(t.slice(0, eq).trim().toLowerCase(), t.slice(eq + 1).trim());
  }
  return out;
}

/** auto_https 是否已配置：值 auto 或非空（域名列表也算已配置）；缺失/空 = 未配置。 */
export function autoHttpsConfigured(extra: unknown): boolean {
  const v = parseExtra(extra).get(AUTO_HTTPS_KEY);
  return typeof v === "string" && v !== "";
}

/**
 * 写回 auto_https = auto，**保留其余键**（用户在面板配的其他项不能被这次
 * 自愈抹掉）：已有 auto_https 一律替换成规范形态，其余键按原顺序原样保留。
 */
export function withAutoHttps(extra: unknown): string {
  const pairs = parseExtra(extra);
  pairs.delete(AUTO_HTTPS_KEY);
  const lines = [...pairs.entries()].map(([k, v]) => `${k} = ${v}`);
  lines.push(`${AUTO_HTTPS_KEY} = ${AUTO_HTTPS_VALUE}`);
  return lines.join("\n");
}

/** auto_https 的目标值（auto = 节点按 SNI 自动申请证书并在节点侧终结 TLS）。 */
const AUTO_HTTPS_KEY = "auto_https";
const AUTO_HTTPS_VALUE = "auto";

export type NodeChoice =
  | { ok: true; node: NodeCandidate }
  | { ok: false; error: string };

/** 自动选：可用节点里负载最低者（未知负载按最高处理，与旧逻辑一致）。 */
export function autoNodeChoice(cands: NodeCandidate[]): NodeChoice {
  const pick = cands.find((c) => c.usable); // candidateNodes 已按负载升序
  if (!pick) return { ok: false, error: "没有可用的节点（全部满载/离线或需要更高用户等级）" };
  return { ok: true, node: pick };
}

/**
 * 手动选：nodeId 必须**在当前候选列表里且可用**。
 *
 * 校验而不是照单全收的理由：nodeId 来自渲染层，节点可能已经离线/等级变更
 * （列表是几秒前拉的），直接拿去创建会得到服务端一句难懂的报错，或者建到
 * 一个用户等级不够的节点上（越权面）。
 */
export function resolveNodeChoice(cands: NodeCandidate[], nodeId: number | null | undefined): NodeChoice {
  if (nodeId === null || nodeId === undefined || !Number.isFinite(nodeId)) return autoNodeChoice(cands);
  const hit = cands.find((c) => c.id === nodeId);
  if (!hit) return { ok: false, error: "该节点已不在可用列表里（可能刚下线），请刷新后重试" };
  if (!hit.usable) return { ok: false, error: `节点「${hit.name}」当前不可用：${hit.reason ?? "未知原因"}` };
  return { ok: true, node: hit };
}

// ---- 延迟探测（TCP connect RTT） ----

/** 探测用的最小 socket 面（测试注入假 socket 用，不需要真网络）。 */
export interface ProbeSocket {
  setTimeout(ms: number, cb: () => void): unknown;
  on(event: "connect" | "error", cb: () => void): unknown;
  destroy(): void;
}

export type ConnectFn = (opts: { host: string; port: number }) => ProbeSocket;

export interface LatencyOptions {
  port?: number;
  timeoutMs?: number;
  now?: () => number;
  connect?: ConnectFn;
}

/**
 * 单次探测：TCP connect 成功 → RTT 毫秒；超时/拒绝/解析失败 → null。
 *
 * **失败返回 null 而不是 0**：0ms 会排在推荐列表最前面（"最快"），
 * 而实际上它根本没测出来——未知不能冒充最优。
 */
export function measureLatency(host: string, opts: LatencyOptions = {}): Promise<number | null> {
  if (!host) return Promise.resolve(null);
  const port = opts.port ?? PROBE_PORT;
  const timeoutMs = opts.timeoutMs ?? PROBE_TIMEOUT_MS;
  const now = opts.now ?? Date.now;
  const connectFn: ConnectFn = opts.connect ?? ((o) => connect(o) as unknown as ProbeSocket);

  return new Promise<number | null>((resolve) => {
    let done = false;
    const started = now();
    const finish = (value: number | null): void => {
      if (done) return;
      done = true;
      try { sock.destroy(); } catch { /* 已断开 */ }
      resolve(value);
    };
    let sock: ProbeSocket;
    try {
      sock = connectFn({ host, port });
    } catch {
      resolve(null); // 同步抛（host 非法等）
      return;
    }
    sock.setTimeout(timeoutMs, () => finish(null));
    sock.on("connect", () => finish(Math.max(0, now() - started)));
    sock.on("error", () => finish(null));
  });
}

export interface LatencyProber {
  /** 带缓存探测（缓存命中直接回；未命中走一次 connect）。 */
  probe(host: string): Promise<number | null>;
  /** 只读缓存（列表首屏用：已知的直接显示，不必等一轮异步推送）。 */
  peek(host: string): { known: boolean; ms: number | null };
  /** 并发受限地批量探测，每有结果立即回调（列表先出、延迟异步填充）。 */
  probeAll(hosts: string[], onResult: (host: string, ms: number | null) => void): Promise<void>;
  clear(): void;
}

export function createLatencyProber(opts: LatencyOptions & {
  cacheTtlMs?: number;
  concurrency?: number;
} = {}): LatencyProber {
  const ttl = opts.cacheTtlMs ?? PROBE_CACHE_TTL_MS;
  const limit = Math.max(1, opts.concurrency ?? PROBE_CONCURRENCY);
  const now = opts.now ?? Date.now;
  const cache = new Map<string, { ms: number | null; at: number }>();
  // 进行中的探测：面板关掉又立刻打开会再发一轮 probeAll，没有这道闸门
  // 同一台节点会被并发 connect 两次（自打自的限流）
  const inflight = new Map<string, Promise<number | null>>();

  const cached = (host: string): { hit: true; ms: number | null } | { hit: false } => {
    const e = cache.get(host);
    if (!e) return { hit: false };
    if (now() - e.at > ttl) {
      cache.delete(host);
      return { hit: false };
    }
    return { hit: true, ms: e.ms };
  };

  const probe = (host: string): Promise<number | null> => {
    const c = cached(host);
    if (c.hit) return Promise.resolve(c.ms);
    const running = inflight.get(host);
    if (running) return running;
    const task = measureLatency(host, opts)
      .then((ms) => {
        cache.set(host, { ms, at: now() });
        return ms;
      })
      .finally(() => { inflight.delete(host); });
    inflight.set(host, task);
    return task;
  };

  const probeAll = async (hosts: string[], onResult: (host: string, ms: number | null) => void): Promise<void> => {
    const uniq = [...new Set(hosts.filter(Boolean))];
    let next = 0;
    const worker = async (): Promise<void> => {
      for (;;) {
        const i = next++;
        if (i >= uniq.length) return;
        const host = uniq[i];
        const ms = await probe(host);
        onResult(host, ms);
      }
    };
    const workers = Array.from({ length: Math.min(limit, uniq.length) }, () => worker());
    await Promise.all(workers);
  };

  return {
    probe,
    peek: (host: string) => {
      const c = cached(host);
      return { known: c.hit, ms: c.hit ? c.ms : null };
    },
    probeAll,
    clear: () => cache.clear(),
  };
}
