// 配对二维码载荷的纯函数层——桌面端两张卡（局域网直连 / 樱花frp）共用一份
// 生成逻辑，口径与测试同一份（二维码内容不许各写各的）。
//
// 载荷契约（安卓 PairingScreen.parsePairPayload 对齐）：
//   lxcode://pair?addr=<host:port>&token=<token>&name=<可选>
// token 是后端远程访问 token（remote.json）——樱花frp 只是隧道，公网入口
// 与局域网入口是同一个后端、同一把 token 门（internal/server/remote.go：
// 所有 /rpc 升级含回环都验 token，回环豁免 = 门是假的）。

/** 一条可扫码的配对入口（一个二维码对应一条）。 */
export interface PairEndpoint {
  /** 连接地址（host:port——frp 公网入口或局域网 IP:port）。 */
  addr: string;
  /** 二维码旁的入口标签（如「樱花frp」「局域网」）。 */
  label: string;
  /** 预填连接名（载荷 name 参数；空 = 载荷按地址主机名自动带出）。 */
  name: string;
}

/** 从地址带出显示名（对齐 components/connections/RemoteForm 的 nameFromAddr：
 *  取 host 段——这里刻意本地实现，避免 shared 层反向依赖组件）。 */
function hostFromAddr(addr: string): string {
  try {
    const u = addr.includes("://") ? new URL(addr) : new URL(`http://${addr}`);
    return u.hostname;
  } catch {
    return addr;
  }
}

/** 配对载荷：lxcode://pair?addr=…&token=…&name=…（组件值 URL encode）。
 *  name 缺省时取地址主机名（与旧版局域网二维码逐字节一致）；name 与地址
 *  主机名相同时不带该参数（安卓侧会自动按地址带出，无需冗余）。 */
export function pairPayload(addr: string, token: string, name = ""): string {
  const params = new URLSearchParams();
  params.set("addr", addr);
  params.set("token", token);
  const n = name.trim() || hostFromAddr(addr);
  if (n && n !== addr) params.set("name", n);
  return `lxcode://pair?${params.toString()}`;
}

/** 从樱花frp 隧道挑出可扫码的公网入口：
 *  只收**在线**隧道（离线隧道的地址扫了也连不上，不放进去误导用户）；
 *  剔除占位地址（「待分配」——端口还没分配，扫了连不上）；
 *  按 addr 去重（同一节点同端口不会出现两条码）；顺序保持隧道列表顺序。 */
export function tunnelPairEndpoints(tunnels: { addr: string; online: boolean }[]): PairEndpoint[] {
  const out: PairEndpoint[] = [];
  const seen = new Set<string>();
  for (const t of tunnels) {
    const addr = t.addr.trim();
    if (!t.online || addr === "" || !/^[a-zA-Z0-9.-]+:\d{1,5}$/.test(addr) || seen.has(addr)) continue;
    seen.add(addr);
    out.push({ addr, label: "樱花frp", name: `樱花frp · ${hostFromAddr(addr)}` });
  }
  return out;
}
