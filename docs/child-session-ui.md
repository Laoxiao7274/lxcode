# 子会话的前端形态（子 Agent = 独立会话）

> 2026-09-30 用户拍板。内核侧的子会话设计见 AGENTS.md §2.3；这里只写**前端**这一半：
> 子会话标签页看到的是**实时流**，主会话的调度卡里**不再有展开收缩**。

## 1. 用户报的现象与根因

用户原话：「子会话的标签页里面有问题啊，我点开之后他里面就没有接着思考，但是我主会话下拉子
Agent 就在思考」「他应该是做成子 Agent 独立跑会话里，标签页看到的也是实时的」「而且在主会话也不
应该有展开收缩了」。

两条现象两个根因，都不是"渲染慢"：

1. **子会话标签页看不到实时流**。子 Agent 的事件按**父会话 id** 广播（后端由父会话的 emitter
   发出 + `dispatch_id` 归属），所以父会话的卡能实时更新；而子会话**自己**那个 id 的 state 从来
   没收到过事件——标签页只有打开那一刻的历史快照（`source.childHistory`），子 Agent 之后继续思考、
   继续跑工具都不出现在那一页。
2. **卡里那套展开/收起本身就是错位的重复**。同一段子过程在卡里只可能是**滞后的摘要**，用户还得
   先展开、再滚到底才看得见；而它真正的形态是一个独立会话的完整时间线。

## 2. 前端契约（每条都有测试钉住）

### 2.1 store 双投（`shared/reduce.ts` 的 `reduceSessionStates`）

带 `dispatch_id` 的事件在归约进**父会话的卡**（摘要 + 状态）的同时，也归约进**子会话自己的
state**——清掉 `dispatchId`，让它当一条普通事件走（delta 续写 assistant、toolCall 建工具行、
confirmRequest 挂待裁决、compacted 插压缩标记）。

- **确认请求的归属键在 `request.dispatch_id` 里，不在事件顶层**（`eventDispatchId`）：漏了这条，
  子会话标签页里就看不到待裁决的确认卡（用户没开标签时反倒只能回主会话批）。
- **子会话自己的 state 还不存在时不建**：没有历史基线，光靠实时增量拼出来的是半截时间线；打开标签
  页时由 `source.childHistory` 装载历史（它发 `historyLoaded`，**sessionId 是子会话自己的**，所以主
  时间线一个块都不动）。
- 子会话 id 从**卡上**取（`dispatchChildSession`）：实时 `dispatchStart` 的 `childSessionId`，或历史
  回放里工具结果那行 `[子会话 id: …]`（`history.ts` 的 `splitDispatchResult`）。两处都写进
  `block.sessionId`——只认一处的话，刷新之后实时事件就找不到子会话了。

### 2.2 裁决两处同时定格（`resolveConfirmEverywhere`）

确认在父会话的卡里与子会话自己的时间线里**各有一份**（双投的必然结果）。只定格用户点的那一处，
另一处会永远挂着「待确认」——那是僵尸卡。

- 协议请求要发给**持有挂起确认的那个会话**：确认门由**父会话代理**（AGENTS.md §2.3），子会话自己
  没有 pending；而 `tool.confirm` 是按 `session_id` 找会话再找它那条挂起确认的。所以 App 按
  `dispatch_id` **反查卡在哪条会话里**，把请求发给父会话。
- 只碰**真的有这个确认**的会话：否则每次裁决都会把别人的挂起确认一起清掉。

### 2.3 主会话的卡 = **一行摘要**

用户第二轮的原话是「执行完了还是会展示整个子Agent会话展开，然后打开子会话这个按钮太丑了，没必要」——
所以卡收敛成**一行**：

- **dot + Agent 名 + 任务摘要 + 状态（执行中 / ✓ 完成 / ✗ 失败）+ 用量 + 子会话 id（带 ↗）**，
  整行可点 = 进子会话；
- **没有**折叠区、**没有**卡内子时间线、**没有**结论正文、**没有**底部按钮。子 Agent 的过程与结论
  都在它自己的标签页里看（而且是实时的）——卡里再画一份只可能是滞后的摘要，用户还得先展开、
  再滚到底才看得见；
- **整行是唯一入口**：能进就是 `<button>`（打开独立会话），进不去就是静态行——点了没反应的按钮
  比没有入口更糟。`dispatch-primary.ts` 的判定因此只剩 `open`/`none`（两处各判一遍会漂移成
  "提示说能进、点下去没反应"）；
- **待裁决确认卡仍渲染在卡里**：它**不是**过程展示，是必须点得到的操作入口——去掉会让没开子会话
  标签的用户无从批准（会话卡在忙态）。

### 2.4 子会话页头有它**自己的**两条读数（2026-09-30 用户报「上下文 会话信息这些展示没有」）

子会话是独立会话（AGENTS.md §2.3），所以它有自己的窗口占用与自己的累计消耗——页头必须显示**它
自己那份**，不是主会话那份：

- **`StatsPills`（会话统计 = 整条子会话花了多少）** 与 **`ContextIndicator`（会话用量 = 此刻它自己的
  窗口里有多少）**，数据都取 `sessionStates[childId]` 的 `stats` / `context`；
- **数据来源两条腿**：打开标签页时 `source.childHistory` 的 `historyLoaded` 带回子会话的
  `context`/`stats`；之后的实时 `chat.done` 由 store 双投路由进子会话 state。**后端必须给**：
  `emit.go` 原先只在 `e.DispatchID == ""` 时挂 `Stats`，子会话的 `chat.done` 因此没有统计（页头永远
  空着）——现在一律按 `sessionStatsOf(sessionID)` 折叠，子会话按**它自己的 session_id** 取数；
- **缺席就不渲染**：一步都没有 → 统计胶囊整行不出、上下文未知 → 中性态「—」（AGENTS.md §2.4 的老纪律，
  这里不破例）。

### 2.5 弹层方向由调用方显式指定（`placement`）

`.ctx-pop` / `.stats-pop` 默认 `bottom: calc(100% + 8px)`（向上开）——那是给 composer 的：chip 就在
屏幕底部，向上开才有空间。子会话页头在页面**顶部**，同一个默认值会把弹层开到视口外（用户原话「上下文
展示的下拉框跑上面去被遮住了」）。

- 两个组件各多一个 `placement?: "up" | "down"` 属性（默认 `"up"` 保持 composer 行为不变），子会话页头
  传 `"down"` → CSS 加 `.ctx-pop.down` / `.stats-pop.down` 类（`top: calc(100% + 8px); bottom: auto`）；
- **不做运行时测量**：弹层高度渲染前未知，而"触发器在页面的哪一头"调用方本来就知道——显式传比运行时猜稳。
- 定位锚是 `.ctx-wrap` / `.stats-anchor`（都是 `position: relative`）：漏了它，`position: absolute` 的弹层
  会去认 `.child-session-page` 这个更外层的定位祖先，"向下开"就变成"开到整页底下"。

## 3. 演示态（DemoAgent）也要给一个真子会话

演示数据源不落库，所以派发过程**一边发事件一边录制**子会话的消息（`childEmit` → `childMsgs_`），
`childHistory` 按同一个子会话 id 返回它——经 `historyBlocks` 映射后与真实链路的回放**共用一份映射**。

- 演示的 `dispatchStart` 必须带 `childSessionId`（否则卡上的子会话 id 徽标是空的，标签页也打不开）；
- 演示的 `childHistory` 也必须发 `historyLoaded`（与 WSAgent 同一份契约）：漏了它，演示态的子会话
  标签页永远显示"没有可显示的历史"。

## 4. 验收

- 单测：`frontend/tests/store.test.mjs`（双投、两处定格、子会话历史重建不动主时间线）、
  `frontend/tests/dispatch-card-click.test.mjs`（`open`/`none` 与提示语同源）、
  `frontend/tests/child-history.test.mjs`（`childHistory` 按子会话 id 寻址且发 `historyLoaded`）。
- 真布局：`node shell/node_modules/electron/cli.js scripts/check-preview-layout.cjs --no-sandbox --disable-gpu`
  的 dispatch 段——结论常显、无 chevron / 无卡内子时间线、点卡头进子会话标签页且看得到子 Agent 的
  工具行；**子会话页头两条读数**（`child-head-readouts`：统计胶囊有非空标签、上下文 chip 不是「—」）与
  **弹层几何**（`child-ctx-pop`：`down` 为真、上下边都在视口内）也在这一段里钉住。
- 真链路：`node shell/node_modules/electron/cli.js temp/probe-live-child-tab.cjs --no-sandbox --disable-gpu`
  ——打真实后端 7789，发一条会派发的消息，点卡头进子会话页，两次采样确认它在长（实时流），并检查页头
  两条读数有值、弹层向下开且在视口内。
