// 网页搜索渠道（search.* 的 wire 形态与管理接口）。

/** 渠道私有设置项的声明（后端 websearch.OptionSpec 的 wire 形态）。
 *
 * 声明来自后端适配器，取值来自用户的 search.json——面板按声明渲染输入项，
 * 所以「渠道多了一个设置」不需要改前端。
 *
 * choices 非空时渲染成下拉：档位/版本这类有限枚举让用户手打，打错了要么被
 * 后端硬校验拦下（白填一次），要么静默改变计费。 */
export interface SearchChannelOption {
  key: string;
  label: string;
  placeholder?: string;
  /** 取值约束与注意事项（渲染在输入项下方）。 */
  hint?: string;
  /** 环境变量回退来源（提示用户也可用环境变量配）。 */
  env_var?: string;
  /** 缺了它渠道就不就绪（面板据此标必填）。 */
  required?: boolean;
  /** 未配置时的默认取值。 */
  default?: string;
  /** 有限枚举（非空时渲染成下拉）。 */
  choices?: string[];
}

/** 搜索渠道（后端 websearch.Channel 的 wire 形态）。
 *
 * 「代码元数据 + 用户配置」合并后的视图：label/desc/doc_url/needs_key 等来自
 * 后端内置的渠道适配器，api_key/base_url/enabled 来自用户的 search.json。
 * 前端不持有渠道清单——新增渠道只改后端，UI 自动出现。 */
export interface SearchChannel {
  id: string;
  label: string;
  desc: string;
  /** 获取 key 的入口（空 = 无需 key）。 */
  doc_url?: string;
  /** 约定环境变量名（提示用户也可用环境变量配）。 */
  env_var?: string;
  needs_key: boolean;
  /** 面板是否应给出 API Key 输入框。
   *
   * 多数渠道与 needs_key 同值；Exa 是「缺 key 也就绪、有 key 走直连」——
   * 只看 needs_key 会把它的输入框藏掉，用户永远进不了直连路径。
   * 可选是因为老后端（未热重载）没这个字段，此时回落到 needs_key。 */
  accepts_key?: boolean;
  /** 需要自填实例地址（自建渠道）。 */
  needs_url: boolean;
  /** 零配置渠道：必须由用户手动启用才生效（如 DuckDuckGo 抓取）。 */
  opt_in?: boolean;
  /** 刻意默认就绪的零配置渠道（如 Exa 的免配置通道）——不填任何配置即可用。 */
  default_ready?: boolean;
  /** 设置面板分组键（free/general/cn/serp/other）。 */
  category: string;
  /** 分组显示名（后端给出，前端不内置一份分类表）。 */
  category_label: string;
  api_key?: string;
  base_url?: string;
  /** 私有设置项的声明（面板据此渲染输入项；空 = 该渠道没有私有设置）。 */
  option_specs?: SearchChannelOption[];
  /** 私有设置项的当前取值（已解析：配置 → 环境变量 → 默认值）。
   *
   * 注意：**可能包含默认值**（用户没填也有值），所以不能用「有没有值」
   * 判断「用户配过没有」——那是 stored 的职责。 */
  options?: Record<string, string>;
  /** 配置文件里是否真的存在该渠道的条目（用户配过东西）。
   *
   * 面板据此决定要不要显示「清除」按钮：零配置渠道（Exa 的免配置通道）
   * 不填任何东西也就绪，拿 configured 当判据会让按钮出现在空卡片上。 */
  stored?: boolean;
  enabled: boolean;
  primary: boolean;
  /** 就绪（凭据/端点齐备且已启用）——搜索时会用到它。 */
  configured: boolean;
}

/** 渠道快照（search.channels.list 结果 + search.changed 载荷）。 */
export interface SearchChannelsSnapshot {
  channels: SearchChannel[];
  /** 主渠道 id（空 = 未指定，按预设顺序降级）。 */
  primary?: string;
  /** 是否存在至少一个就绪渠道——false 时 web_search 不可用。 */
  ready: boolean;
}

/** 单渠道测试结果（search.test）。 */
export interface SearchTestResult {
  provider: string;
  answer?: string;
  results: Array<{ title: string; url: string; snippet?: string }>;
  elapsed_ms: number;
}

/** SearchAdminSource：搜索渠道的查看与管理（后端 search.* 直通）。
 *
 * 与 ModelAdminSource 同模式：UI 依赖能力接口而非具体 WSAgent；
 * 测试按钮只测单渠道、不降级（降级会把「这个渠道坏了」测成「搜索正常」）。 */
export interface SearchAdminSource {
  /** 当前快照（search.channels.list 结果缓存）。 */
  channels(): SearchChannelsSnapshot;
  /** 订阅渠道变化（连接建立/search.changed；返回退订）。 */
  onChannelsChanged(listener: () => void): () => void;
  /** 保存（upsert）渠道配置。options 是渠道私有设置（整体覆盖，键名见 option_specs）。 */
  saveChannel(
    id: string,
    patch: { apiKey?: string; baseUrl?: string; options?: Record<string, string>; enabled?: boolean },
  ): Promise<void>;
  /** 删除渠道配置（回到未配置状态）。 */
  removeChannel(id: string): Promise<void>;
  /** 设主渠道（空 id = 清空）。 */
  setPrimary(id: string): Promise<void>;
  /** 测试单个渠道（不降级）。 */
  testChannel(id: string, query?: string): Promise<SearchTestResult>;
}
