# 模型目录与端点探测（modelcatalog）

> 2026-10-07 落地。两个能力、三条协议方法，**都不改注册表**：目录回答「世界上有什么模型」，探测回答「这个端点实际提供什么」，注册表由用户勾选后显式写入。

## 为什么是「Go 侧实现 + models.dev 数据」，而不是依赖 pi-ai

用户最初的要求是「按 pi-ai 来做」（DSH 引入的依赖）。**结论是照它的设计做，不把 pi-ai 当依赖**，理由三条：

1. **Go 后端加载不了 JS 包**，而模型目录本来就该在后端（key 不经页面 JS、出网只许在 `agent/ws`——`scripts/check-boundaries.mjs` 钉着这条）；
2. `AGENTS.md` §2.1 **禁止 FFI/cgo**，且第二语言模块要同时满足三门槛（热路径 >30% / 自包含 / Go 生态无等效品）——模型目录只占一次 HTTP GET + 一次 JSON 解析，三条一条都不占；
3. 放进前端更糟：那会把 **API key 与出网放进渲染层**，直接违反 `scripts/check-boundaries.mjs`（出网只许在 `agent/ws`）与「key 不经页面 JS 中转」这条既有纪律。

所以 pi-ai 与 pi-web-access 一样，**是设计参考而不是依赖**（MIT，仅借鉴）。借鉴的是它的三点判断：目录与注册表分离、按需拉取 + 缓存、以及「两阶段」（先用缓存、再联网）。数据源换成 **models.dev**：公开、免 key、结构简单，实测一次 GET 拿到全量目录。

## 数据源：models.dev/api.json

实测（2026-10-07）：`https://models.dev/api.json` HTTP 200、**5.07 MB**、**226 个厂商 / 8394 个模型**，拉取 ~3.3–5.1s。

- 厂商字段：`id/name/env/npm/doc` 全量都有，**200 个有 `api`**（端点地址，即注册表 `base_url` 的建议值）。
- `npm` → wire 格式映射（`internal/modelcatalog/catalog.go` 的 `formatOf`）：`@ai-sdk/openai-compatible`(185) / `@ai-sdk/openai`(6) / `@openrouter/ai-sdk-provider` → `openai`；`@ai-sdk/anthropic`(8) → `anthropic`；**其余一律丢弃**（Google/Bedrock/Azure 走原生协议，lxcode 的 `internal/llm` 调不了——列出来只会让用户点了报错）。
- 模型字段：`id/name/description/family/attachment(视觉)/reasoning/tool_call/structured_output/temperature/limit{context,output}/cost{...}/status/release_date/modalities/open_weights`。
- **过滤后 = 199 个厂商 / 6657 个模型**（丢 1 个无 `api` 的、丢 2 个格式不支持的、丢 0 模型的厂商）；落盘 1177 KB（实测，未压缩）。
- **`status` 空 = 正常**：原始目录 8394 个模型里 `deprecated` 261 个、`beta` 71 个，其余 8062 个没有该键——空值当异常是错的。

## 三条纪律

- **目录是只读查询，不写注册表**（`Service.Providers`/`Models`/`Discover` 都没有副作用）：目录里 6600+ 个模型**绝不自动写入**——自动添加会把模型选择器淹掉，而且用户根本没机会看到自己接了什么。写入一律走「用户勾选 → `model.save`」。
- **两阶段 + stale 位**（`snapshot`）：TTL 24h 内直接用缓存（实测第二次调用 1ms，首次 3.7s）；过期才联网，**联网失败时保留手上这份并标 `stale=true`**（前端如实显示「目录已过期，显示上次缓存」）。拿不到数据时**不假装有数据**——目录拉取失败就退化成手工填模型 ID，也就是没有这个功能之前的行为。
- **单飞（single-flight）**：`fetchMu` + 双检，并发请求只发一次网络往返。TTL 内不联网是**刻意的**（不是「顺手加个缓存」）：打开设置面板不该每次都等 5 MB 下载。

## 端点探测（`model.discover`）

回答「这个端点实际提供什么」，与目录互补：目录是**免 key 的公共数据**，探测是**打用户自己的端点**。

- **两种入参**：`id` = 已注册条目（`base_url`/`api_key`/`format` 全取自注册表，**key 不过 wire**，也就不存在「前端手里的 key 与注册表不一致」这种分叉）；或 `base_url` + `api_key` + `format` = 还没进注册表的新端点（自定义提供商表单）。
- **URL 归一化**（`modelsURL`）：裸地址 / 带 `/v1` / 带 `/v1/models` / 带 `/chat/completions` / 带 `/completions` / 带 `/messages` 六种形态都收敛到 `<base>/v1/models`；已以 `/models` 结尾的原样保留。回显 `endpoint` 让用户能核对自己填的地址被解析成了什么。
- **鉴权约定与 `internal/llm` 一致**：openai → `Authorization: Bearer <key>`；anthropic → `x-api-key` + `anthropic-version: 2023-06-01`；**无 key 不发鉴权头**（自建端点常见）。
- **不做 SSRF 守卫是刻意的**：探测目标就是用户自己的端点（局域网 / localhost 是主要场景），加内网拦截会把主要用途挡掉。`validateBaseURL` 只校验「是不是 http(s) 地址」。
- **错误必须自解释**：401/403 → 「端点返回 HTTP 401（检查 API Key）」+ 端点回显的错误正文；404 → 「端点没有 /v1/models」。实测真 `api.deepseek.com` + 假 key 报 `HTTP 401（检查 API Key）: {"error":{"message":"Authentication Fails, Your api key: ****ance is invalid"...`——这条同时证明 key 真的被用上了。
- 去重 + 按 id 排序；响应同时接受 `data`（OpenAI）与 `models`（部分网关）两个键。

## 协议

`model.catalog.list`（`{refresh?}`）/ `model.catalog.models`（`{provider, refresh?}`）/ `model.discover`（`{id?|base_url?,api_key?,format?}`），全部**只读**、无事件、无广播（`AttachModelCatalog` 与 `AttachSearch` 的差别：目录不写配置、不改工具注册表、不订阅变更）。

- 载荷直接复用 `modelcatalog` 的类型（与 `SearchChannelsResult.Channels` 复用 `websearch.Channel` 同款）：**给目录加字段不用改协议**。
- `model.catalog.list` **不带模型明细**（6600+ 个塞不进一次响应），只给 `model_count`；明细按厂商 `model.catalog.models` 拉。
- 未装配目录服务时回 `CodeInternal "模型目录服务未装配"`——**不能回空清单**，那会被前端读成「目录里没有厂商」，两种失败必须分得开。
- 未知厂商 `model.catalog.models` 报错，不隐式触发重拉。

## 前端：三条路径收敛到同一条

目录（免 key、带元数据）/ 探测（自建端点、只有 id/name）/ 手工填 ID（老路径，仍然保留）三条路**都收敛到「候选清单 → 用户勾选 → 写注册表」**：

- **绝不自动添加**：目录 6600+ 个模型，自动写入会把选择器淹掉；探测面板也**默认不预勾选**（预勾选在 3 个模型时方便、在 200 个时是灾难——可预期胜过聪明）。唯一例外是自定义表单探测后默认全选，因为那是用户刚亲手点的「列出这个端点有什么」。
- **演示模式映射成同一套载荷形状**（`settings-catalog.ts` 的 `demoCatalogProviderList`/`demoCatalogModelList`）：好处是**连接对话框只有一条代码路径**，不必在 UI 里到处写 `live ? ... : ...`。
- **`catalogMetadata` 不猜元数据**，并且**输出上限不小于窗口时留空**：后端 `config.ModelConfig.validate` 硬拒「`max_output_tokens >= context_window`」（`internal/config/types.go:87`，输入+输出会超限），而目录里 **932/6554（14%）的条目恰好如此**——数据源自己把两者报成相等，照抄会让这些模型整条加不进去。留空 = 未知，由端点自己决定。
- **`mapModels` 去掉了 `|| 128_000` / `|| 8_000` 的兜底**：窗口未知时压缩**不触发**，编一个数字会让用户以为压缩在保护他。显示层用 `kfmtLimit` 说「未知」（`0` 不是「窗口是 0」，是「不知道」）。连带：`ModelEditDialog` 的 `parseK("")` 现在返回 0（空 = 未知），否则编辑一个没配窗口的模型会被迫编一个数才能保存。
- **`catalogTags` 的措辞与 `mapModels` 逐字一致**：候选行与已注册行的标签必须能对上（勾选时看到「工具/推理」，加进去不能变成别的字）。
- **批量写入单个失败不中断整批**（成功的留下、失败的逐条点名回抛）：否则用户不知道 5 个里到底进了几个。

## 缓存落盘

`config/model-catalog.json`（与 `models.json` 同目录，可用 `LXCODE_MODEL_CATALOG` 覆盖），**已进 .gitignore**（生成物、无凭据，但不该变成待提交文件）。写盘走 `internal/atomicfile`（同目录临时文件 + fsync + rename，与 `tools` 的 write_file 共用一份实现）。文件缺失静默忽略、损坏只记日志——缓存坏了不该拒绝启动。

启动时**后台预热**（`cmd/lxcode/serve.go`）：首次拉取 ~5s，预热过设置面板一开就是热的；预热失败只记日志，真正的错误在用户打开设置时如实回报。

## 验收

`node temp/ws-catalog-live.mjs`（真 models.dev + 本地假端点 + 断网降级，15 项断言）与 `node temp/cdp-catalog.mjs` / `node temp/cdp-catalog2.mjs`（CDP 截图自查：目录 picker / 厂商页 / 勾选 / 探测面板 / 完整旅程）。

## 未做（明确出界）

- **档位（effort）接线**：目录的 `reasoning_options` 里有 lxcode 发不出的取值（`xhigh`/`max`/`none`），且 `ModelConfig` 没有对应字段——要接线得先扩注册表字段，独立一轮。
- **成本（cost）接进会话统计**：目录有 `cost{input,output,...}`，接进 `sessiondata.SessionStats` 是另一件事。
- **目录自动刷新调度**：目前只有 TTL + 手动 `refresh`。
