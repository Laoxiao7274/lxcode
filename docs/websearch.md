# 网页搜索渠道（websearch）

> 从 `AGENTS.md` §3.2 整段搬来（2026-09-29）：AGENTS.md 有 65536 字节的指令预算，超了会被静默截断，所以细节按该文件自己的规矩外移到 docs/。正文未改一字。

## 网页搜索渠道（`internal/websearch`，2026-09-27）

`web_search` 工具背后是一个**多渠道 + 失败自动降级**的检索层，移植自 pi-web-access（MIT，仅作设计参考、**不是依赖**）。设计要点：

- **代码持元数据，磁盘持用户配置**：渠道的 label/desc/docURL/envVar/needsKey/needsBaseURL/optIn/category/options 全在代码里（各适配器的 `base` 字面量 + `provider.go` 的分类表），`config/search.json` 只存用户填的 `api_key`/`base_url`/`options`/`disabled`——**新增渠道零迁移**，老配置文件照常能读。
- **渠道私有设置项 = `OptionSpec` 声明 + `ChannelConfig.Options` 取值**（2026-09-27）：渠道除通用凭据外还有各自私有的设置（Brightdata 的 SERP zone、Mistral 的模型与档位、Firecrawl 的 API 版本），此前只能靠环境变量配——**设置面板配不出一个能用的 Brightdata 渠道**。现在：适配器声明 `Options() []OptionSpec`（key/label/placeholder/hint/envVar/required/default/choices），磁盘存 `options` 对象，设置面板**按声明渲染输入项**（`choices` 非空 → 下拉，`required` → 红星标）。三条纪律：
  - **`effectiveChannel` 是唯一解析点**（配置 → `spec.EnvVar` 环境变量 → `spec.Default`），适配器只认 `ch.Options`、**不许再碰 `os.Getenv`**（否则各文件自行决定读哪个变量，行为不可预测也无法测试）；`readyIn`/`channelsOf` 都传 `effectiveChannel(p, raw)`，所以必填项的就绪判定与环境变量回退**零改调用点**就生效；
  - **只解析声明过的键**（磁盘上多出来的键不往适配器传）——手写配置里一个拼错的键名不该被当成有效设置悄悄生效（用户以为配上了）；
  - **环境变量回退是兼容承诺**：这些项在加 UI 之前只能靠环境变量配，去掉回退等于让老用户升级后渠道突然失效；
  - 摘要里的设置项**值只进哈希**（键名进明文）：`snapshot()` 只用于变更检测，而它可能被打印，值可能是凭据（`TestSnapshotDetectsOptionChangeWithoutLeaking` 钉住「能察觉变化」+「不泄漏明文」两头）；`clone()` 必须**深拷贝** Options（map 是引用类型，浅拷贝让读侧与写侧共享同一份）。
  - 渲染侧：`ChannelView` 把 `OptionSpecs`（声明）与 `Options`（已解析取值）一起给前端，**所以给渠道加设置项不需要改前端**；`choices` 渲染成下拉是因为手打错了要么被后端硬校验拦下（白填一次）、要么静默改变计费（如 Mistral 的 premium 档）。
- **`presetProviders()` 是唯一登记点**（`internal/websearch/provider.go`）：漏登记 = 渠道在 UI 与工具里都不可见（静默失效）。`provider_test.go` 钉住 id 字符集/唯一性/元数据齐备/分类存在/数量下限，并新增 `TestOptionSpecsContract`（键名/EnvVar 字符集、Choices 必须含 Default、有设置项的渠道不能是零配置）+ `TestRequiredOptionGatesReadiness`（必填项必须真的挡住就绪判定，否则「必填」只是个 UI 装饰）。改渠道清单先看它们。
- **降级语义只认错误分类，不做字符串匹配**：`FallbackKinds`（transient/quota/network/invalid-response/unsupported）才降级；credential/config/auth/invalid-request **不降级**（换渠道只会把配置错误掩盖成搜索成功）。空结果集是**成功**，不是降级理由。
- **渠道自身超时必须可降级**（2026-09-27 修的缺陷）：`ClassifyTransport` 必须收到**调用方**的 ctx——拿 `withTimeout` 派生出来的那份会让「渠道太慢」被判成「用户取消」（`KindAborted`），主渠道一慢整次搜索直接失败。判据是「调用方 ctx 是否已结束」：结束了 = 真取消（不降级），没结束 = 渠道侧问题（降级）。`TestChannelTimeoutFallsBack` 钉住这条。
- **`optIn` 是零配置渠道的门闩**：DuckDuckGo 这类既不需要 key 也不需要地址的渠道，若不标 `optIn` 就会**默认就绪**并悄悄参与降级（上游把它列为 explicit-only 正是这个理由）。`channelReady(p, ch, present)` 是唯一的就绪判定（`ChannelView`/`Ready`/`firstReadyLocked`/`chainOrder`/`SearchWith` 全走它），「配置文件里存在该条目」这件事只有 Service 知道，所以 `present` 必须由 Service 传入。
- **与上游 explicit-only 的偏离是刻意的**：lxcode 没有上游 `all` 那种「一次打全部渠道」的扇出，只有顺序降级，而渠道只有在用户配置过之后才进链——配置行为本身就是显式意图（`chainOrder` 里有完整注释）。
- **key 脱敏是硬要求**：渠道常在错误体里回显请求内容（含 key），`Redact` 在 `NewProviderError` 里统一做。**测试夹具的假 key 必须够长**——`Redact` 会把 key 及其前 8 字符在消息里整串替换，用 `"k"` 会把 `"invalid api key"` 打成 `"invalid api ***ey"`，精确匹配断言随之失效（多批适配器都踩过）。
- **未移植的渠道**（上游共 32 个）：需 MCP 客户端的（parallel-mcp/baizhi）、需浏览器 Cookie 或 ADC 的（gemini-web/gemini-adc，不建议移植）、复用宿主模型凭据的（openai/gemini/kimi/xai，等模型注册表凭据复用落地）。
- 协议：`search.channels.list` / `search.channel.save` / `search.channel.remove` / `search.primary.set` / `search.test` + `search.changed` 事件（载荷即快照）。**`search.test` 只测单渠道、不降级**——降级会把「这个渠道坏了」测成「搜索正常」。前端走 `SearchAdminSource` 能力接口（与 `ModelAdminSource` 同模式，UI 不知道数据来自 WS 还是 Demo）。
- 适配器测试一律用 `httptest` 假服务器，**绝不打真渠道**（不花钱、不依赖网络）。
- **Exa 是唯一「开箱即用」的默认渠道（2026-09-27）**：免配置路径 = 直连官方 MCP 端点 `https://mcp.exa.ai/mcp?tools=<tool>` 打**裸 JSON-RPC `tools/call`**（无 initialize、无会话 id，响应是 SSE 的 `data:` 行），**不需要通用 MCP 客户端**（stdlib 就够）。三条旗标各管一件事：`needsKey` = 就绪是否必须 key（Exa false）；`acceptsKey` = 面板是否给 key 输入框（默认回落 `needsKey`——Exa 缺 key 也就绪、有 key 走直连 API，只看 `needsKey` 会把输入框藏掉，用户永远进不了直连路径）；`defaultReady` = **刻意**的零配置就绪（「零配置渠道必须 optIn」那道守卫的显式例外，`optIn && defaultReady` 自相矛盾、测试禁止）。`base.AcceptsKey()` = `needsKey || acceptsKey`，另外 25 个渠道行为逐字节不变；wire 加 `accepts_key`（恒发）/`default_ready`（omitempty），**加法变更不递增协议 Version**。
- **`ChannelView.Stored` 是「配置文件里是否真有该条目」**（不是「值非空」）：后端给的是**已解析**取值（配置 → 环境变量 → 默认值），Exa 的 `mcp_url` 不填也有默认值——拿「options 有值」判断「用户配过」会让「清除」按钮出现在空卡片上（CDP 截图自查抓到的真 bug）。前端 `hasStoredConfig` 优先用 `stored`，老后端回落「值存在且 ≠ 声明默认值」。
- **Exa 的默认地位零接线**：`firstReadyLocked` 按 `presetProviders()` 顺序找首个就绪渠道，空配置下 searxng/duckduckgo/jina/tavily 都不就绪 → Exa 即首个就绪；`chainOrder` 同理排最前。`TestExaIsTheDefaultReadyChannel` 钉住「恰好一个 defaultReady 且是 exa」。
