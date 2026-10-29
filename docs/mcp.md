# MCP 执行面（`internal/mcp` + `tools/mcp.go`）

> 从 `AGENTS.md` §3.3 整段搬来（2026-10-07）：AGENTS.md 有 65536 字节的指令预算，超了会被静默截断，所以细节按该文件自己的规矩外移到 docs/。正文未改一字。

**只依赖标准库**的 MCP 客户端：服务器注册后暴露的能力以工具形式进目录（`source=mcp` + `server` 指回），与 `source=binary` 同一段动态注册（`SetDynamic` 整体替换）。

- **两种传输**：`stdio`（起子进程 + **换行分隔** JSON-RPC，故编码必须紧凑——带缩进的 JSON 破坏分帧）与 `sse`（= **Streamable HTTP**：单端点 POST、`Accept: application/json, text/event-stream`、响应可能是 JSON 或 SSE 的 `data:` 行、回带 `Mcp-Session-Id`/`MCP-Protocol-Version`）。磁盘 `transport` 取值 `stdio|sse` 受 SQLite CHECK 约束，**把 sse 实现成 Streamable HTTP 正好让老配置直接可用，不必改表**。
- **握手顺序固定**：`initialize` → `notifications/initialized` → `tools/list` → `tools/call`。`initialize` 结果形状是 `{protocolVersion, capabilities, serverInfo:{name,version}}`——`name`/`version` **嵌套**，按扁平结构解析会静默拿到空名字（单测抓到过）。
- **同连接串行**（`Client.mu` 覆盖整个往返）：MCP 允许并发（靠 id 配对），但使用面是「启动列举一次 + 调用」，串行换来实现简单且不会两处同读 stdout。
- **stdout 噪音跳过而不是判死**：规范要求 stdout 只写协议、日志走 stderr，但现实有服务器混写。`decodeResponse` 对非 JSON 行/无 id 通知/别人的响应一律返回「不是我的」让调用方继续读，**不报错**。
- **对账而非增量**（`Manager.Sync` 唯一入口，幂等）：传「当前应该连哪些」整份清单，自己算要连/要断/要保持。三条硬纪律：① 配置没变**保持原连接**（重连打断在途调用）；② 配置变了必须重连（沿用旧连接 = 配置没生效）；③ **上次没连上的必须重试**——`connect` 失败时 `client` 为 nil，按「指纹相同就保持」处理会让启动时连不上的服务器**永远**不再尝试（用户修好命令也没用），最难排查的一类问题。清理一律走 nil-safe 的 `entry.close()`。
- **工具名净化必须做**（`ExposedName` = `<server>_<tool>`，非 `[A-Za-z0-9_-]` 换 `_`，截到 64）：MCP 允许点号（`web.search`），而工具名有硬字符集约束（见 §3 末条）——不净化会被 400 拒收**整轮**。净化后撞名**报错**而非悄悄加后缀（名字必须稳定，否则模型上轮学到的名字下轮就不存在）。
- **风险一律高危 + `Mutates=true`，不采信服务器自报注解**（`readOnlyHint` 等）：MCP 规范明说「clients MUST consider tool annotations to be untrusted unless they come from trusted servers」——服务器可自称只读换自动执行。放宽只能靠用户显式选 `auto` 档；`Annotations` 只作展示，**不参与定级**。
- **三处状态缺一处就是半截功能**：① 连接（`mcp.Manager`）② 目录（`tools` 表里 `source=mcp` 的条目 = 服务器的事实投影，`materializeMCPTools` 整份重建：删多的、补缺的、更描述变了的）③ 注册表（`syncDynamicTools` 的动态段）。`mcpDefs()` 的数据源是 **manager 而不是目录**（从目录反推 MCP 原名是绕远路——目录里只有净化后的名字）。
- **停用 = 能力挂起**：断开 + 撤下目录条目。**MCP 物化条目是 `custom=0` 但可删**（`RemoveTool` 只读守卫的判据是 `source` 而不只是 `custom`：只看 `custom` 会让 MCP 工具永远删不掉，能力挂起变成假的——真链路探针抓到的真 bug）。
- **运行期状态与磁盘形状分开**：`McServerEntry` 的 `status`/`tool_count`/`last_error`/`stderr` 由 `mcpServerViews` 应答时合成（不往 `McServerSpec` 塞运行期字段）。前端 **`enabled ≠ 已连接`**：`enabled` 是用户意图，连不上时仍为 true——拿它显示「已连接」等于骗用户（`mcp-status.ts` 的 `mcpStatusPill` 纯函数 + 测试钉住，缺状态字段时回落按 `enabled` 显示）；失败原因必须显示出来。
- **`tools` 包不 import `mcp`**（用扁平 `MCPToolSpec` 构造，依赖方向保持「装配在 server」——映射在 `server/mcp.go`）（MCP 工具 id 同受这条约束）。
- **验收**：`internal/mcp` 用「测试二进制自我 re-exec」当假 stdio 服务器（`TestMain` 看 `MCP_FAKE_SERVER`，不引外部依赖），HTTP 用 `httptest`；`internal/server/mcp_test.go` 走完整装配链（加服务器 → 物化 → 注册 → 执行 → 状态 → 停用 → 删除）；**真链路**探针 `node temp/ws-mcp-live.mjs` 打真实 Exa MCP 端点。
