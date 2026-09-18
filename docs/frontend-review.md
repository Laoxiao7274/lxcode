# 前端架构审查（2026-09-18）

> 范围：frontend/src 全量（93 文件 / 442KB TS）。聚焦用户四点：模块化、解耦/拆分、性能、渲染逻辑（多会话×长会话扩展性）。
> 结论先行：**架构底座是健康的**（分层清晰、热路径的 memo 纪律到位、流式渲染管线设计正确），但存在 **1 个高危扩展性缺陷**（会话切换的全量重放）、**2 个中危**（App 双订阅、Orb 资产体积）、**5 个拆分债**。逐项如下。

---

## 一、模块化 ✅ 基本合格，两处越界

**好的部分**（不需要动）：
- 分层纪律由 `check-boundaries.mjs`（TS AST）静态钉死：网络只在 `agent/ws`、宿主桥只在 Topbar/AddProjectDialog、表单套件统一 `components/form`。这是全仓最有价值的模块化资产。
- `shared/` 是干净的域层：`store.ts`（事件归约）零 React 依赖之外的 import；`tool-import.ts`/`mcp-config.ts`/`agent-delegation.ts` 纯函数 + 测试。
- Provider 模式统一（settings/agents/connections/update/search-providers），每个域自包含。

**越界 1：`shared/agents.tsx` 是 26KB 的上帝文件**。它同时是：类型定义（ToolSpec/McServerSpec/…）+ 种子数据（BUILTIN_TOOLS/CONTEXT_MODULES/MC_SERVERS/演示 agents，~300 行）+ Provider 状态机。任何消费方只要 import 一个类型就把 675 行全部拉进 bundle。**建议三拆**：`shared/agent-types.ts`（纯类型）、`shared/agent-seeds.ts`（种子数据，可懒加载/后端化时整体删除）、`shared/agents.tsx`（保留 Provider）。`agent-protocol.ts` 已示范了叶类型文件的正确做法。

**越界 2：`SearchSection` 里的 `ProviderCard`/`AddCustomForm` 内联在分区文件**。settings 其它分区（模型）的行组件独立成文件（ProviderBlock/SessionRow 先例），搜索的卡组件 170 行内联在 SearchSection.tsx——中等程度，属于同一目录约定内的一致性债，不紧急。

---

## 二、解耦与拆分 ⚠️ 五个具体点

**① `CatalogPage.tsx` 16.9KB，六个职责**（p1，最值得拆）
一个文件里有：EntryCard、ToolCard、ModuleCard、McServerCard（四种卡片渲染器）+ 页面编排 + 三个对话框的挂载逻辑。建议拆成 `catalog/cards.ts`（或每卡一个文件，与 sidebar/SessionRow 先例对齐）+ `CatalogPage.tsx` 只留编排。**注意**：卡之间共享 `confirming` 两步确认逻辑——拆时把「两步删除确认」抽成 `useConfirmClick()` hook（现在这段逻辑在 EntryCard/McServerCard/conn-item/tunnel 卡四处复制粘贴，是全仓最明显的重复代码，第四次出现于 SearchSection 的自定义渠道卡）。

**② `AgentEditor.tsx` 18.2KB**（p2）——表单本体 + DetailPanel（紧凑详情面板）两个不相干的东西在一个文件。DetailPanel 是只读展示（消费 agents 域），表单是编辑器——拆 `AgentEditor/DetailPanel.tsx` 即减 ~120 行。

**③ `ConnectionManager.tsx` 15.8KB**（p2）——CopyBtn、SakuraBlock、RemoteAccessBlock、RemoteForm、ConnectionManager 五个组件。SakuraBlock（公网穿透）是独立领域（有自己的状态、自己的冒烟链路），应拆 `connections/SakuraBlock.tsx`；RemoteForm 同理可拆。

**④ `Sidebar.tsx` 14.1KB**（p3）——导航区/搜索框/项目分组/会话列表/底部行五个视觉段在 JSX 里平铺。已有 SessionRow 先例，可按视觉段拆 2-3 个文件。优先级最低（逻辑内聚尚可）。

**⑤ `Thread.tsx` 的空态（SUGGESTIONS 数组 ~40 行 JSX）**（p3）——建议卡数据+图标硬编码在渲染组件里，拆 `thread/EmptyState.tsx` 后 Thread 只剩滚动状态机（它真正复杂的部分）。

---

## 三、性能 ⚠️ 两个实锤 + 一个测量项

**① `aicss/Orb.tsx` 18.3KB vendor 资产**（p1，bundle 实锤）
Orb 是 @aicss/react 的 vendor 拷贝（636 行，25 个变体的装饰动画），但全应用只用 `variant="S1" size={16}` **一处**（Composer 的生成中指示器）。18KB 源码（tree-shake 后估 ~8-10KB gzip）为一个 16px 的 spinner 服务。建议二选一：**换成 3 行 CSS 动画的 spinner**（现有 mset-spinner 就是），删 Orb；或 lazy import（但它只在 busy 时出现，CSS 动画完全够）。这是最便宜的 bundle 优化。

**② `useStreamReveal` 的 setState-in-rAF**（p2，设计权衡）
打字机缓冲每帧 `setShown`（30-1000 字/秒），意味着流式期间 AssistantBlock **每帧重渲染**。当前有 markdown 行级 memo 兜底（增长的尾块重渲染，历史行不动），可接受；但注意它与下面 §四-① 的 `reduce` 逐字重建是**叠加**的：一条 delta = store 层一次 setState（全量 blocks 数组重建）+ reveal 层一次 setState（尾块重渲染）。长会话下二者叠加的协调成本线性增长（见 §四-① 的量化）。

**③ 无测量数据**（测量项）
全部性能论断目前只有 bundle 尺寸是实测的（476KB → 主 chunk）。渲染性能（切会话耗时、千块会话的输入延迟）没有 benchmark。建议加一个 `scripts/bench-render.mjs`（Electron 下造 500/2000 块会话测 reduce+paint 耗时），给 §四 的重构决策提供数字依据——**改并发归约还是窗口化，先有数字再动**。

---

## 四、渲染逻辑与会话扩展性 🔴 一个高危 + 两个防御

**① 高危：会话切换/历史加载是全量重放，无窗口化**（`store.ts` `reduceHistory` L148-189 + `Thread.tsx` L226-228）
现状：`historyLoaded` 一次 `setState(reduceHistory(...))` 重建整个 blocks 数组，`Thread` 把**全部** blocks 一次性 map 成 DOM。500 条消息的会话 = 一次同步渲染几百个 memo(Block)（每个含 markdown 解析）。实测可见的卡顿点。
三层影响：
- **首渲染**：reduceHistory 同步跑 + React 同步 commit——主线程冻结与块数成正比；
- **流式期间**：每条 delta 走 `reduce`（L46-70）——它 `[...state.blocks]` **复制整个数组**再换尾块引用，O(n) 每帧，2 千块会话 = 每秒几十次数组复制；
- **内存**：所有历史块的 DOM 常驻（无卸载）。

建议（按代价递增）：
1. **窗口化渲染**（低成本高收益）：`Thread` 只渲染底部 N 块 + 上方 sentinel，滚动到顶再前插（`react-window` 式手动实现即可，Block 尺寸不定所以用「块数窗口」不用虚拟行高）。UI 不变，DOM 从 O(全部) 降到 O(视口)。
2. **reduce 的尾块不可变优化**：delta 分支只对尾块做不可变替换、数组用 persistent 结构（或简单地把「正在流式的那一块」移出 blocks 数组单独 setState）——消灭 O(n) 复制。
3. **historyLoaded 分批**：大历史分片 `setTimeout` 注入，首屏先出底部 50 块。
其中 1+2 是「很多会话×很多内容」目标的必需项；3 是锦上添花。

**② 防御：`source.sessions()` 在渲染体内直接调用**（`Sidebar` L65、`SettingsPanel` L61、`App` L88）
WS 实现是缓存数组返回（`ws/index.ts` L346-347），demo 是成员引用——**引用稳定**，所以每次重渲染只是 `.filter()` 的 O(n) 开销，当前无碍。但这是隐性契约（哪天 sessions() 返回新数组就是全树重渲染）。建议在 useAgent/useSessions 层统一订阅 `sessionsChanged` 缓存派生结果，把「渲染体读源」收敛到一处。

**③ 防御：App 的双订阅**（`App.tsx` L58-64 与 `store.ts` L201-204 各自 subscribe 同一 source）
同一事件流两处消费本身没错（各管各的），但 `sessionChanged` 在 App 里 setCurrentId、在 store 里清 blocks——**两处 setState 各自触发渲染**，切会话必然双 commit。合并进 store 的 reduce（把 currentId 也放进 UIState）可减一半切会话渲染。顺手可解：`filterProjectName`（L67-70）在渲染体每帧跑 `source.projects().find`——小，但同属「渲染体读源」。

**④ 好的部分**（点名表扬，别动）：`Block` memo + `uid` 稳定 key + `onConfirm` useCallback（App L82-86）的配合是教科书级的；markdown 行级 memo（序列化 src 比较兜住打字机重渲染）；`ThinkingState` 的 sentences useMemo 锁字符串——这三个是流式不卡的前提，重构 §四-① 时必须原样保留。

---

## 优先级排序（建议实施序）

| # | 事项 | 类型 | 理由 |
|---|---|---|---|
| 1 | Thread 窗口化 + reduce 尾块优化 | 高危 | 「很多会话很多内容」的直接前提；不改这个其余都白搭 |
| 2 | bench-render.mjs 渲染基准 | 测量 | 给 #1 验收与后续优化提供数字；半天工作量 |
| 3 | Orb 替换为 CSS spinner | 性能 | 最便宜的 bundle 优化，10 分钟 |
| 4 | CatalogPage 拆卡 + useConfirmClick 抽取 | 拆分债 | 重复四次的两步确认是全仓最大重复 |
| 5 | agents.tsx 三拆 | 模块化 | 类型/种子/Provider 分离，为后端化铺路 |
| 6 | App 双订阅合并（currentId 入 store） | 解耦 | 切会话双渲染消除 |
| 7 | AgentEditor/ConnectionManager/SakuraBlock 文件拆分 | 拆分债 | 纯机械搬移，随时可做 |
| 8 | Thread 空态拆 EmptyState | 拆分债 | 最小项 |

**不建议做的**：不要为拆分而引入 barrel re-export 层（现在 `components/*/index.ts` 已经是单文件转发，够了）；不要现在上 React.lazy 拆路由级 chunk（应用是单屏切换，chunk 收益小且拖慢首屏）。
