# 樱花frp 集成实现文档

> 状态：设计定稿（2026-09-18）。API 依据 [SakuraFrp 开放 API 定义](https://github.com/natfrp/api)（OpenAPI 3.0，AGPL-3.0，服务端点 `https://api.natfrp.com/v4`）。
> 定位：把「远程访问」从局域网地址升级成公网可达——用户在 lxcode 内登录樱花frp，自动创建/管理隧道，远程设备直接连公网地址。

## 1. 目标与验收

- 用户在连接管理（远程访问块）里登录樱花frp（访问密钥）；
- 一键开启「公网访问」：自动选节点 → 创建（或复用）一条指向本地后端 7789 的 TCP 隧道 → 拉起 frpc → 展示公网连接地址；
- 账户流量（本日消耗/总剩余）、隧道状态与用量可见；
- 关闭公网访问 = 停 frpc（隧道保留，可再次开启）；
- 前端原型先行：假数据渲染全部 UI，API 层留桩。

## 2. 领域模型

```ts
/** 樱花frp 账户（本地只存访问密钥——凭证哲学：服务端生成校验，壳只保管）。 */
interface SakuraAccount {
  /** 访问密钥（用户中心生成，Bearer 认证）。 */
  token: string;
  /** /user/info 摘要。 */
  name: string;
  avatar?: string;
  /** [本日消耗字节, 总剩余字节]。 */
  traffic: [number, number];
  /** 限速描述（如 "10 Mbps"）。 */
  speed?: string;
}

/** 一条公网隧道（tcp——本地后端 → 樱花frp 节点）。 */
interface SakuraTunnel {
  id: number;
  name: string;
  /** 节点 ID。 */
  node: number;
  /** 节点主机名（连接地址的一部分）。 */
  nodeHost: string;
  /** 远程端口（创建时留空自动分配）。 */
  remotePort: number;
  /** 是否在线（frpc 在跑且节点可达）。 */
  online: boolean;
  /** 隧道累计用量（字节）。 */
  trafficUsed: number;
}
```

## 3. API 接线（全部有官方端点）

| 能力 | 端点 | 备注 |
|---|---|---|
| 登录校验 | `GET /user/info` | Bearer token；拿 name/traffic/speed |
| 节点选择 | `GET /nodes` + `GET /node/stats` | 过滤：flag bit2（允许创建）置位、bit9（离线）清零；按 load 升序取首个 |
| 隧道列表 | `GET /tunnels` | 找 lxcode 建的（命名约定 `lxcode-backend`） |
| 创建隧道 | `POST /tunnels` | `name=lxcode-backend, type=tcp, node=<id>, local_ip=127.0.0.1, local_port=7789`；remote 留空=自动分配端口 |
| frpc 配置 | `POST /tunnel/config` | `query=<id>, frpc=<版本>` → ini/toml；壳写盘后 spawn |
| 停/启 | frpc 进程生命周期 | 关闭=kill frpc（隧道登记保留）；节点端踢下线由服务端处理 |
| 删除隧道 | `POST /tunnel/delete` | `ids=<id>`（用户显式操作，不在关闭时自动删） |
| 账户流量 | `GET /user/info` | `[本日消耗, 总剩余]` |
| 隧道流量 | `GET /tunnel/traffic?id=` | Timestamp→字节数组 |

**认证**：`Authorization: Bearer <token>`（或 query `token=`——POST 表单兼容）。

**错误**：统一 `{code, msg}`；429 限频注意重试退避。

## 4. 架构与安全决策

1. **凭证边界**：访问密钥只存本机（配置文件，与远程连接的 token 同一存储层）；不上传、不进日志。
2. **frpc 进程归属**：壳（Electron 主进程）spawn frpc，配 `--remote_control` 关闭；`-f` 用 `/tunnel/config` 拉的启动参数。frpc 生命周期挂壳的退出路径（Job Object 兜底——与 Go sidecar 同款纪律）。
3. **隧道命名约定**：`lxcode-backend`——列表里靠它识别「我们的」隧道，复用不重建；用户改了名就当普通隧道展示。
4. **自动选节点**：可创建 + 在线 + 负载最低；VIP 节点按用户等级过滤（`/user/info` group.level 对比节点 vip）。原型阶段简化为前三个可用节点任选。
5. **流量的计费语义**：SakuraFrp 按 frpc↔frps 间上下行计费（[流量规则](https://www.natfrp.com/policy/rule)）——展示上注明「隧道流量 = 双向计费」。
6. **后端零改动**：隧道打的是本地 7789（壳直连的同一个后端端口）——Go 侧不需要知道自己在被穿透。Token 认证仍是后端的远程访问机制，樱花frp 只是传输层。

## 5. UI 设计（原型已落地）

远程访问块内新增「公网穿透」分区（登录后展开）：

```
远程访问                    [⬤ 开]
┌─ 公网穿透 · 樱花frp ────────────────────┐
│ ● 已登录 DemoUser        流量 1.2G / 剩 8.8G │
│                                          │
│ 隧道  lxcode-backend · tcp                │
│ 节点  中国·台州 · 负载 19%               │
│ 地址  idea-leaper-1.natfrp.io:1919  [复制] │
│ 状态  ● 在线 · 用量 234 MB    [断开][删除]  │
│                                          │
│ [新建隧道]（选节点 → 自动分配端口）          │
└──────────────────────────────────────────┘
未登录态：[访问密钥登录] → 输入密钥（说明从用户中心获取）
```

- 登录：密钥输入（不存明文展示）；退出登录
- 流量：本日消耗 / 总剩余（进度条语义——剩余比例）
- 隧道行：地址一键复制（和远程访问本机地址同款交互）
- 新建：选节点（下拉，标注负载/VIP）→ 创建（自动端口）
- 断开 = 停 frpc；删除 = `POST /tunnel/delete`（两步确认）

## 6. 后端化接线点（原型之后的真实实现）

1. **frpc 二进制分发**：`/system/clients` 按平台取下载 URL（带 hash 校验）；首次开启公网访问时下载到 userData；打包形态走 extraResources（与 Go sidecar 同款）。
2. **轮询**：/user/info（流量）与 /tunnel/traffic（用量）60s 轮询；节点状态在新建隧道时拉取。
3. **网络边界**：frpc 是独立进程，出网走系统网络（与壳同代理语义）；API 请求从壳发（fetch）。
4. **协议无关**：隧道透传 TCP——后端的 WS JSON-RPC 与 Token 认证原样工作。
5. **降级路径**：无樱花frp 时远程访问仍是局域网地址（现在的形态）；两者不互斥（局域网直连优先，公网穿透是补充）。

## 7. 风险与边界

- **CORS**：浏览器模式下 api.natfrp.com 的 CORS 未知——壳内发请求无此问题（Electron 主进程 fetch）；纯浏览器模式可能要后端代理。原型 UI 不发真实请求，不受影响。
- **限频**：API 有频率限制（错误码 429）——轮询间隔不低于 60s。
- **实名/封禁**：/user/info 返回冻结形态（ban 字段）——UI 需处理冻结提示。
- **流量耗尽**：服务端会直接关隧道（[规则](https://www.natfrp.com/policy/rule)）——展示「剩余流量」并在耗尽前预警（<100MB 提示）。
- **AGPL**：API 定义文件 AGPL-3.0——我们只调用 API 不分发其代码，无传染风险；自研实现文档与代码不引用其源码。

## 8. 里程碑

1. ✅ M1 页面原型（本轮）：登录/隧道/流量 UI 全量假数据，交互闭环
2. M2 API 层：壳内 fetch 接线（登录校验/列表/创建/删除）+ 密钥安全存储
3. M3 frpc 生命周期：下载/配置拉取/spawn/停止/重启
4. M4 轮询与状态机：流量刷新、断线重连、节点切换
