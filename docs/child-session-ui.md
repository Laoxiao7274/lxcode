# 子会话的前端形态（子 Agent = 独立会话）

> 2026-09-30 用户拍板；**2026-12 修订：子会话不再以标签页呈现**——标签栏只显示真实
> （顶层）会话，点主时间线的派发卡打开的是**临时视图**（页头「返回主会话」关闭它）。
> 子会话视图看到的是**实时流**，主会话的调度卡里**不再有展开收缩**。

## 0. 导航呈现：标签页 → 临时视图（2026-12 用户拍板）

用户需求：**标签栏（tabs）只显示真实（顶层）会话，子会话不再以标签页形式出现**。

- **打开**：点主时间线的派发摘要卡（整行可点，§2.3）→ `focusChildView`
  （`shared/workspace-tabs.ts`）把当前工作区视图切到 `child:<sessionId>`——
  **只切 `active`，不进 `tabs`**，所以标签栏长度不变、永不出现子会话标签；
- **呈现**：`ChildSessionPage` 覆盖当前会话区（与原标签页同一套完整版式，§2.4），
  页头带明显的「返回主会话」按钮；
- **返回**：页头按钮 / 点标签栏「聊天」→ `focusChatTab`，临时视图随之卸载
  （它不在 `tabs` 里，`WorkspaceViewPanels` 只保活 `tabs` 中的页面）；
- **不残留、不重复**：重复点同一张卡回到同一个视图键（active 已是该键 = 焦点保持），
  多张卡来回点只是切换 active——连点/快速切换不会长出重复视图或弄乱状态；
- **数据不丢**：子会话自身的 state 归约（§2.1 双投）、实时流缓存、子→父映射照旧保留
  （导航层不碰 store）；关闭/返回后重新打开由 `ChildSessionPage` 重新装载历史
  （`source.childHistory`），实时事件从双投续上；
- **重开必是完整历史（2026-12 修复）**：`ChildSessionPage` 每次挂载都重新
  `source.childHistory`，store 对 `historyLoaded` 一律**全量重建** blocks（替换不是
  追加）——state 已存在、`historyReady=true` 也不跳过，没有「只拉一次」守卫。关闭
  期间双投积累的实时块被重放的历史快照整体替换：不丢（历史 ⊇ 已见事件落库后的
  内容）、不重复。这条的服务端半边同样要成立：**子会话运行时不进 `s.sessions`
  运行时缓存**（`internal/server/session_ops.go`）——缓存里那份是 AttachTo 时刻的
  静态快照（收不到事件、不再追加），一旦缓存，重开视图的 `chat.history` 会一直
  命中第一次打开那一刻的快照（用户报的「历史丢了」根因）。钉子：
  `internal/server/child_history_fresh_test.go`（子会话在跑时拉一次历史、跑完再拉
  一次必须读到结论）+ `frontend/tests/child-reopen.test.mjs`（关闭 → 实时双投 →
  重开全量重建不丢不重复）；
- **浏览器历史**：打开/返回仍各 pushState 一个条目，侧键/浏览器后退可回到子会话视图
  （`focusHistoryView` → `focusChildView`，不要求该键在 `tabs` 里）。

**机制层零变化**：双投归约、确认归属键（`request.dispatch_id`）、`resolveConfirmEverywhere`、
子会话 state「不存在时不建而由 childHistory 装载」、busy 上抛、任务块渲染——全部不动，
本节只改导航/呈现层（tab 生成逻辑 + 打开方式）。

## 1. 用户报的现象与根因

用户原话：「子会话的标签页里面有问题啊，我点开之后他里面就没有接着思考，但是我主会话下拉子
Agent 就在思考」「他应该是做成子 Agent 独立跑会话里，标签页看到的也是实时的」「而且在主会话也不
应该有展开收缩了」。

两条现象两个根因，都不是"渲染慢"：

1. **子会话视图看不到实时流**。子 Agent 的事件按**父会话 id** 广播（后端由父会话的 emitter
   发出 + `dispatch_id` 归属），所以父会话的卡能实时更新；而子会话**自己**那个 id 的 state 从来
   没收到过事件——旧标签页只有打开那一刻的历史快照（`source.childHistory`），子 Agent 之后继续思考、
   继续跑工具都不出现在那一页。
2. **卡里那套展开/收起本身就是错位的重复**。同一段子过程在卡里只可能是**滞后的摘要**，用户还得
   先展开、再滚到底才看得见；而它真正的形态是一个独立会话的完整时间线。

## 2. 前端契约（每条都有测试钉住）

### 2.1 store 双投（`shared/reduce.ts` 的 `reduceSessionStates`）

带 `dispatch_id` 的事件在归约进**父会话的卡**（摘要 + 状态）的同时，也归约进**子会话自己的
state**——清掉 `dispatchId`，让它当一条普通事件走（delta 续写 assistant、toolCall 建工具行、
confirmRequest 挂待裁决、compacted 插压缩标记）。

- **确认请求的归属键在 `request.dispatch_id` 里，不在事件顶层**（`eventDispatchId`）：漏了这条，
  子会话视图里就看不到待裁决的确认卡（用户没打开视图时反倒只能回主会话批）。
- **子会话自己的 state 还不存在时不建**：没有历史基线，光靠实时增量拼出来的是半截时间线；打开临时
  视图时由 `source.childHistory` 装载历史（它发 `historyLoaded`，**sessionId 是子会话自己的**，所以主
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
  整行可点 = 打开子会话**临时视图**（§0）；
- **没有**折叠区、**没有**卡内子时间线、**没有**结论正文、**没有**底部按钮。子 Agent 的过程与结论
  都在它自己的临时视图里看（而且是实时的）——卡里再画一份只可能是滞后的摘要，用户还得先展开、
  再滚到底才看得见；
- **整行是唯一入口**：能进就是 `<button>`（打开临时视图），进不去就是静态行——点了没反应的按钮
  比没有入口更糟。`dispatch-primary.ts` 的判定因此只剩 `open`/`none`（两处各判一遍会漂移成
  "提示说能进、点下去没反应"）；
- **待裁决确认卡仍渲染在卡里**：它**不是**过程展示，是必须点得到的操作入口——去掉会让没打开子会话
  视图的用户无从批准（会话卡在忙态）。

### 2.4 子会话临时视图 = 主会话同款完整版式，只锁输入（2026-10-07 用户要求）

用户原话：「输入框什么都应该全部保留，样式完全一致，只是不允许输入而已，不然你得重新做一个页面的样式非常麻烦」——
所以子会话视图不是"页头读数 + 时间线"的简化版，而是与主会话**同一套结构**：

- **同一个 `Composer`**（`readOnly`）：输入框（禁用 + 只读占位「只读视图——回到主会话才能发消息」）、
  三个选择器（Agent/权限/模型，包在 `.pi-controls.pi-locked` 里整组锁定——它们作用的是**主会话**
  的上下文，在子会话视图可交互就是改错会话）、上下文环、统计胶囊，全保留、版式逐字节一致；
  **上下文环/统计胶囊是读数，保持可点开**，数据就是这个子会话自己的 `stats`/`context`；
- **页头只剩身份信息**（子会话/模型徽标/只读说明/返回）——读数从页头移进输入条后，
  「同一块信息两个位置」的分叉就不存在了（§2.5 的 `placement="down"` 因此退役，组件的 prop
  保留给别的调用方）；
- **布局机制与主会话同一条**：`.composer-zone` 是 absolute 浮层、`.child-session-body` 按
  `--composer-h` 预留底部空间、「回到底部」浮标让位输入区——全部照抄 `.thread-scroll` 的规则。
  `--composer-h` 由 Composer 发布到**最近的工作区面板**（不是 `.main`）：工作区面板保活
  （隐藏不卸载），聊天页与子会话视图的 Composer 可能同时挂载，发布到 `.main` 会互踩；
- **只读语义的例外不变**：待裁决的确认卡在这一页能批（§2.2）。

（2026-09-30 的旧形态——页头两条读数——被本节取代；数据来源两条腿的纪律照旧生效。）

### 2.5 弹层方向由调用方显式指定（`placement`）

`.ctx-pop` / `.stats-pop` 默认 `bottom: calc(100% + 8px)`（向上开）——那是给 composer 的：chip 就在
屏幕底部，向上开才有空间。子会话页头在页面**顶部**，同一个默认值会把弹层开到视口外（用户原话「上下文
展示的下拉框跑上面去被遮住了」）。

- 两个组件各多一个 `placement?: "up" | "down"` 属性（默认 `"up"` 保持 composer 行为不变），子会话页头
  传 `"down"` → CSS 加 `.ctx-pop.down` / `.stats-pop.down` 类（`top: calc(100% + 8px); bottom: auto`）；
- **不做运行时测量**：弹层高度渲染前未知，而"触发器在页面的哪一头"调用方本来就知道——显式传比运行时猜稳。
- 定位锚是 `.ctx-wrap` / `.stats-anchor`（都是 `position: relative`）：漏了它，`position: absolute` 的弹层
  会去认 `.child-session-page` 这个更外层的定位祖先，"向下开"就变成"开到整页底下"。

## 2.6 派发卡下的附件条（2026-12：子会话成果回传主会话）

用户需求：子 Agent 跑完把图/文件 share_file 共享后，**主会话里看不到**——附件按
`dispatch_id` 归属只进子会话时间线，主时间线只有一行摘要卡。

**方案选型（拍板记录）**：候选有两条——
a) `chat.dispatchEnd` 带附件清单（卡的可展开附件区）；
b) 附件随**调度结果消息**回流（结构化元数据挂到主会话那条 `role=tool` 消息上 +
   实时 `toolResult` 事件同一份）。
选 **b**，理由：

- **零新 wire 形态**：附件回流走的全是既有通道（tool 消息的 `attachments` 列随消息
  落库回放；`ToolResultEvent.attachments` 与 share_file 同一份形状）——`chat.dispatchEnd`
  不加字段，协议 Version 不动；
- **归属正确**：附件的磁盘位置与归属会话不变（仍在**子会话**的附件目录
  `attachments/<子会话id>/<文件>`），引用自解释（路径首段就是子会话 id），前端
  `attachmentURL` 直接可用；回放路径天然一致（历史里那条工具消息带着元数据）；
- **主 Agent 拿得到路径**：调度结果文本追加「[附件] 名字 → attachments/<路径>」行
  （与用户文件附件的写入格式逐字一致，不发明第二套）——主 Agent 下一轮可转述或
  再次 share_file。

**渲染：卡 = 一行摘要的例外，但不是推翻**。§2.3 说卡里没有子过程、没有结论正文——
附件**不是过程也不是结论，是成果**（用户派活的交付物）。所以附件条渲染在卡**下方**
（`DispatchCard` 尾部，`AttachmentCards` 复用 share_file 工具行同一组件），不进卡头、
不做展开区、不复制子会话时间线——卡头那一行的形态逐字节不变，整行可点进子会话的
入口也不变。

- **实时路径**：`agent_dispatch` 的 `toolResult` 事件带 `attachments` →
  `reduceToolResult` 按 id 找 dispatch 块挂上（没带附件的调度结果照旧 no-op）；
- **回放路径**：调度结果消息的 `m.attachments` → `historyBlocks` 的 dispatch 卡分支
  挂上——两条路径同一份元数据、同一个组件（分叉纪律与 §2.1 同源）；
- 子会话视图里自己的 share_file 工具行照旧渲染（双投路径不动）——主时间线的附件条
  是**回流副本**，不是子时间线的搬运。

## 3. 演示态（DemoAgent）也要给一个真子会话

演示数据源不落库，所以派发过程**一边发事件一边录制**子会话的消息（`childEmit` → `childMsgs_`），
`childHistory` 按同一个子会话 id 返回它——经 `historyBlocks` 映射后与真实链路的回放**共用一份映射**。

- 演示的 `dispatchStart` 必须带 `childSessionId`（否则卡上的子会话 id 徽标是空的，临时视图也打不开）；
- 演示的 `childHistory` 也必须发 `historyLoaded`（与 WSAgent 同一份契约）：漏了它，演示态的子会话
  视图永远显示"没有可显示的历史"。

## 4. 验收

- 单测：`frontend/tests/store.test.mjs`（双投、两处定格、**切完全访问一次性定格** `allowAllPendingConfirms`、
  子会话历史重建不动主时间线）、
  `frontend/tests/dispatch-card-click.test.mjs`（`open`/`none` 与提示语同源）、
  `frontend/tests/child-view.test.mjs`（**临时视图状态模型**：打开不进 tabs / 标签栏长度不变、
  去重、返回回到原会话、再点同一张卡视图还在——§0 的钉子）、
  `frontend/tests/child-links.test.mjs`（子→父映射两数据源 + 双投路由 + 标题反查）、
  `frontend/tests/child-history.test.mjs`（`childHistory` 按子会话 id 寻址且发 `historyLoaded`）、
  `frontend/tests/approval.test.mjs`（**档位显示按会话隔离**：`chat.history` 的 approval 对齐显示）、
  `frontend/tests/dispatch-attachments.test.mjs`（**附件回流**：实时 toolResult 挂卡、
  回放从调度结果消息同一份形状——§2.6 的钉子）。
- 真布局：`node shell/node_modules/electron/cli.js scripts/check-preview-layout.cjs --no-sandbox --disable-gpu`
  的 dispatch 段——结论常显、无 chevron / 无卡内子时间线、点卡头打开子会话临时视图且看得到子 Agent 的
  工具行；**子会话页头两条读数**（`child-head-readouts`：统计胶囊有非空标签、上下文 chip 不是「—」）与
  **弹层几何**（`child-ctx-pop`：`down` 为真、上下边都在视口内）也在这一段里钉住。
- 真链路：`node shell/node_modules/electron/cli.js temp/probe-live-child-tab.cjs --no-sandbox --disable-gpu`
  ——打真实后端 7789，发一条会派发的消息，点卡头进子会话临时视图，两次采样确认它在长（实时流），并检查页头
  两条读数有值、弹层向下开且在视口内。
