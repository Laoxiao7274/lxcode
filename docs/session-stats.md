# 会话统计（session stats）契约

> 2026-09-30 落地，对齐 DSH 的 `dsh-session-stats`（整段日志折叠）+ `dsh-token-meter`（用量四桶）。
> 本文件是**完整契约**（每条纪律与它换来的东西）；AGENTS.md §2.4 只留指针。

## 1. 它回答什么问题

两个数字长得很像、经常被搞混，分工必须先说清：

| | 回答的问题 | 折叠输入 | 压缩后 | 撤回后 |
|---|---|---|---|---|
| `ContextUsage`（§2.2） | **此刻窗口里有多少** | 最近一次主轮的真实 prompt 总量（+ 估算分类） | 变（要更新） | 变（要更新） |
| `SessionStats`（本节） | **这条会话一共花了多少** | 整段日志（全部消息行） | **不变** | **变**（真删了行） |

`SessionStats` 的字段：`Turns`（轮）、`Steps`（步）、`LLMMs`（模型时间）、`ToolMs`（工具时间）、
`TTFTMs`/`TTFTSteps`（首字）、`DecodeMs`/`DecodeTokens`（解码），以及计费四桶
`InputTokens`/`CacheReadTokens`/`CacheWriteTokens`/`OutputTokens`。

## 2. 折叠（`internal/store/stats.go` 的 `foldSessionStats`）

**输入是整段日志**（`Store.SessionStatsOf` → `readRows` 的**全部**行，含被压缩检查点影子掉的那些）。
DSH 的原话是「full-session figures that paging and compaction cannot change」——逐条口径：

- **turns**：真实用户消息数（`notice = 0` 且 `checkpoint = 0`）。与右栏「轮次」面板同一口径（一条用户
  消息开一轮）。**注入的提示条不算**：重复调用提醒与后台任务通告在历史里都是真实 user 角色消息（模型必须把它
  当用户回合才能回应），但它们不是用户说的话；
- **steps**：assistant 消息数（每次模型调用一条）；
- **llmMs**：assistant 的 `duration_ms` 之和（请求发出 → 收尾）；
- **toolMs**：tool 消息的 `duration_ms` 之和（`runTools` 执行处计时）。**被拒绝/取消而没执行**的调用不写它
  （0 = 未知，不计入）——把"没跑"算成"0ms"会让工具时间的均值失真；
- **ttftMs / ttftSteps**：只算**有首字**的步（工具轮与非流式回放没有"首字"这个时刻）；
- **decodeMs / decodeTokens**：只算**同时有首字与输出 token** 的步——分子分母必须来自同一批步，
  否则"没报用量的步"会把它稀释成假速度。分母扣掉首字延迟（口径与 `agent.OutputTokensPerSec` 一致：
  首字是 prefill/排队，算进分母会把"排队久"误报成"吐字慢"）；
- **四桶**：assistant 消息上 provider 回报的用量（未回报 = 0，不计）。

**为什么不做增量累加**（把累计值存进 `sessions` 行）：撤回会删行、压缩会影子行，累加器在这两种改写之后就是
错的，而要修正它得反推被删掉的那一段——比每次重算一遍更容易漂移。折叠是纯函数，重算的成本是扫一遍这张会话的
消息（本地 SQLite，一条会话几千行的量级）。

**为什么必须整段日志**：读当前可见历史的话，用户一压缩就会看到步数/token 突然变小，而那是个假事实（活没少干）。
**撤回是唯一的例外**：它真把行删了，统计跟着变小才是对的（`TestSessionStatsFollowsRewind`）。

## 3. 用量语义归一（`internal/llm`）

**两家的口径差异只在 llm 层抹平一次**（`openAIUsage` / `anthropicUsage`，四条解析路径共用——各写一遍必然漂移，
而漂移的表现是「同一条端点在流式/非流式下给出不一样的数字」）：

| 字段 | 语义 |
|---|---|
| `ChatResult.UsageTokens` | **输出** token（生成速度的分母口径） |
| `ChatResult.PromptTokens` | **prompt 侧总量** = 未缓存输入 + 缓存读 + 缓存写（上下文压力用它） |
| `InputTokens` | **未缓存**输入 |
| `CacheReadTokens` | 命中缓存的输入 |
| `CacheWriteTokens` | 写入缓存的输入 |

- **OpenAI 兼容**：`prompt_tokens` **包含**缓存命中部分 → 未缓存 = prompt − cached（钳到 0，端点偶尔报
  cached > prompt 的脏数据）；缓存命中取 `prompt_tokens_details.cached_tokens`，缺席回落 deepseek 的扁平
  `prompt_cache_hit_tokens`；`completion_tokens` 缺席时按 `total − prompt` 推，推不出来就是 0（未知）；
- **anthropic**：`input_tokens` **不含**缓存那两项 → 三桶直接搬运（`cache_read_input_tokens` /
  `cache_creation_input_tokens`）；
- **老实现把 `UsageTokens` 填成 `total_tokens`（含输入）**——那会让 tok/s 把输入当输出算（虚高），而
  `llm.Message.UsageTokens` 的注释一直写的是"输出 token"。2026-09-30 归一。

`llm.StampUsage` 是**唯一的落点**（agent 的轮收尾调用）：各适配器自己盖一份的话，「有的路径盖了、
有的没盖」就是半截状态——而会话统计的四桶正是读消息上的这份。

## 4. 落库（`messages` 表）

加五列（幂等 ALTER）：`input_tokens` / `cache_read_tokens` / `cache_write_tokens` / `notice` /
`usage_split`。输出 token 复用既有的 `usage_tokens` 列（现在它的语义真的是输出）。

**`usage_split` 是「这行的用量是拆分口径」的写边界标记**（与 `notice` 位同一套做法）：

- 本功能上线（2026-09-30）**之前**，适配器把 provider 的 `total_tokens`（输入+输出）写进了
  `usage_tokens`，而且当时根本没有输入侧那三列。那些行的口径是「总量已知、拆分未知」——
  把它当输出累加会把生成速度报得离谱（本机实测一条会话显示 **687.8 tok/s**，真值约 40），
  当输入累加又缺了输出。所以折叠时它们**单独记账**进 `LegacyTokens`：不进四桶、不进速度，
  只在明细里如实说明「早期记录：N tok（输入+输出）」；
- ALTER 的 `DEFAULT 0` 正好把所有**已存在的行**标成老口径（它们确实是旧二进制写的），
  新写入一律置 `1`（`AppendMsg` 的 INSERT 里是字面量 1）；
- **为什么用写边界的一位标记，而不是在折叠时用「输入侧三列全 0」去猜**：猜在"端点只报
  completion_tokens、不报 prompt_tokens"时会把新行误判成老行（少显示）。写边界标记没有这个
  失效模式，而且旧二进制（降级运行）写的行拿列默认值 0，判定依然正确；
- **首字与耗时与口径无关**（那时也是如实测的）：老行照样计入 `LLMMs`/`TTFTMs`/`TTFTSteps`，
  只有 token 桶与 `decode`（速度的分子分母）把它们排除。

**`notice` 位记在写边界**（`agent.noticeMessage` 与 `runTurn` 的重复提醒注入）：两个前缀常量分别在
`agent`（`RepeatNoticePrefix`）与 `protocol`（`JobNoticePrefix`）包里，而折叠统计的 `store` 谁都不能 import
（分层规则，AGENTS.md §4）——判定记在写边界，读侧就不必猜。

**簿记位必须写回消息**（`readRows` 的 `r.msg.Notice = r.notice`）：本仓库的老坑是"写进去的与读出来的对不上"，
漏这一行的表现是"内存里的历史少了这个位"，而库里其实有（2026-09-30 实测：折叠读 rowData 是对的、`Load` 出来的
消息却是 false——两条路径分叉）。

## 5. wire

- `chat.done` 带 `stats`：**仅主轮**（与 `context` 同一条纪律——子会话有自己的会话，它的统计挂在它自己的
  session_id 上）。一轮里的每个 done 都带一份最新的，客户端取最后收到的那份即可（与 DSH 的投影随事件推进同语义）；
- `ChatHistoryResult.stats`：回放路径与实时路径必须是同一份数字（本仓库为"两条路径分叉"吃过三次亏）；
- `chat.rewound.stats`：**重算后**的统计（撤回真删了行，步数/token 会跟着变小）。压缩**不发**统计——
  整段日志折叠出来的数字本来就不变；
- **零值 = 还没有任何一步 → 整键缺席**（`sessiondata.SessionStats.Empty()`）：前端不渲染统计胶囊，
  **不显示一排 0**（"0 轮 0 步"是个假事实）。

## 6. 前端

- `frontend/src/shared/session-stats.ts`：纯函数口径（组件与 node:test 共用一份）——`statsVisible`（没有步数 →
  整行不渲染）、`hasTokens`（四桶 > 0；决定「会话消耗」一节渲不渲染）、`totalTokens`/`promptTokens`、
  `decodeTokensPerSec`（缺数据 → null）、`averageTtftMs`、`formatStatsTokens`（1.2k/1.2M）、
  `formatDuration`（45.2s / 2m42s）、`cacheHitPercent`、`timeDialogRows`/`usageDialogRows`、
  `timePillLabel`/`usageTotalLabel`；
- `frontend/src/components/stats-pills/`：**输入框那一行**一个胶囊（时间）、**紧挨上下文环**（2026-09-30 用户拍板
  从"输入框上方一行"移进来）——「N 轮 · M 步 · X tok/s」，点开是模型时间 / 工具时间 / 首字延迟 / 生成速度；
- `frontend/src/components/context-indicator/`：上下文环那个「会话用量」弹层里**多了一节「会话消耗」**
  （2026-09-30 用户拍板把用量胶囊并进来）：标题 + 总量（`usageTotalLabel`）+ 明细行（`usageDialogRows`：
  缓存命中 / 未缓存输入 / 缓存读取 / 缓存写入 / 输出 / 早期记录）。并进来的两个理由：
  ① 它原先是输入条里的第二个胶囊，而那一行的可用宽度被 `.composer-inner` 的 `max-width: 720px` 卡死
  （内边距 24px → 实际约 696px），两个完整标签放不下时浏览器会把可收缩的控件压到**文字折成两行**
  （2026-09-30 用户实测踩到）；② 上下文环与累计消耗都是"这条会话用了多少"，分两处看反而让人以为是两件事。
  现在输入条里只剩时间胶囊，控件一律 `flex: none` + `white-space: nowrap`（永不折行）、统计胶囊是唯一让位项
  （`flex: 0 1 auto` + 标签 ellipsis 兜底）。
- **三个名字必须分得开**（同一个弹层标题会让人把两件事当一回事）：「会话统计」= 时间胶囊（轮/步/耗时/速度）、
  **「会话消耗」**= 「会话用量」弹层里那一节（累计四桶 + 缓存命中）、**「会话用量」**= 上下文环那个弹层
  （此刻窗口里有多少 + 累计消耗两节）。

三条展示纪律：

1. **缺席的项直接不出现**（工具时间 0 与"没有工具时间"必须分得开；速度/命中率算不出就 null → 那一行不渲染）；
2. **缓存命中率不许把部分命中四舍五入成 100%**：99.6% 显示成 "100%" 会让用户以为全都命中了，而那正是这一栏
   唯一要说的事。所以逐级加精度直到四舍五入不再把它抬到 100（对齐 DSH 的 `formatCacheHitPercent`）；
3. **done 不带 stats 时保持旧值**（后端读不到库 ≠ 没有——把用户已经看到的数字擦掉更坏），
   **rewound 不带时清空**（历史真被删了，旧数字一定是错的）。

## 7. 钉子（测试）

- `internal/store/stats_test.go`：折叠口径、**压缩不改统计**、**撤回改统计**、重启后一致、提示条不算轮；
- `internal/agent/session_stats_test.go`：四桶与工具耗时真的落到消息上、提示条带 `notice` 位；
- `internal/server/session_stats_ws_test.go`：走完整条链（store → server → wire）——live 与 replay 同一份、
  重启后仍在、空会话整键缺席、部分撤回后变小、子会话不写主会话、压缩不改；
- `internal/protocol/protocol_test.go` 的 `TestSessionStatsPayloads`：逐字断言 snake_case 键名（字段改名不会编译
  失败，只会静默变成一排 0）；
- `frontend/tests/session-stats.test.mjs` / `stats-pills.test.mjs` / `context-usage.test.mjs`：纯函数口径与
  组件接线（静态渲染看不到弹层里的字，所以口径抽进纯函数才测得到；`stats-pills.test.mjs` 还渲染真的
  `Composer` 钉住"胶囊在 `.piBar` 里、紧挨上下文环"这个位置）。

**真链路探针**：`node temp/ws-session-stats.mjs [端口]`（前置 = 后端在跑）。它在真实端点 + 真实模型上
走三轮：① 主轮 `chat.done` 带 stats 且数字是活的；② `chat.history` 与 `chat.done` 逐字段一致；
③ 两轮之后轮/步数按轮增长；④ 让主 Agent 真用一次工具（会派子 Agent，撞确认门时探针自动放行）→
`tool_ms > 0`。本机实测（anthropic 格式的真实端点）：`tool_ms=1857ms`、`turns=3 steps=4`、live 与 replay 一致。

两条**探针自身的**坑（都会伪装成产品 bug）：① 只认**主轮** done（`dispatch_id` 缺席）——子会话自己的
done 按设计不带 stats，取"最后一个 done"会偶发取到子会话那条；② 收尾后要再等一拍——`chat.history` 的
`busy=false` 响应可能先于最后那条 `chat.done` 事件到达（事件是广播、响应是应答，两条通道没有顺序保证）。
