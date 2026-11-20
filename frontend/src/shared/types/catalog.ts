// 模型注册表与模型目录（model.* / modelcatalog.* 的 wire 形态与管理接口）。

/** 后端模型注册表（config.ModelConfig 的 wire 形态，snake_case）。 */
export interface ModelEntry {
  id: string;
  /** 提供商标签（可选）：磁盘主键 = `provider::id`，两家提供商的同名模型可并存。
   *  省略 = 存量条目，主键就是纯 id。 */
  provider?: string;
  display_name?: string;
  base_url: string;
  api_key?: string;
  format?: string;
  model: string;
  context_window?: number;
  max_output_tokens?: number;
  capabilities?: { tools?: boolean; vision?: boolean; json_output?: boolean; reasoning?: boolean };
  enabled: boolean;
}

/** ModelAdminSource：模型注册表的查看与管理（后端 model.* 直通）。
 * 独立能力接口——UI 面板依赖它而非具体 WSAgent；Demo 不实现。 */
export interface ModelAdminSource {
  /** 当前快照（model.list 结果缓存）。 */
  models(): { models: ModelEntry[]; roles: Record<string, string> };
  /** 订阅注册表变化（连接建立/model.changed；返回退订）。 */
  onModelsChanged(listener: () => void): () => void;
  /** 新增注册表条目；校验失败以 rejected Promise 返回。 */
  addModel(entry: Partial<Omit<ModelEntry, "id">> & { id: string; base_url?: string }): Promise<void>;
  /** 更新（以现有条目为底套 patch）。 */
  updateModel(entry: ModelEntry): Promise<void>;
  /** 删除。 */
  removeModel(id: string): Promise<void>;
  /** 可见性开关。 */
  setModelEnabled(id: string, enabled: boolean): Promise<void>;
  /** 角色绑定（default/vision）。 */
  setRole(role: string, modelId: string): Promise<void>;
  /** 可选模型目录的厂商清单（refresh = 忽略 TTL 强制重拉）。 */
  catalogProviders(refresh?: boolean): Promise<CatalogProviderList>;
  /** 某厂商的模型明细（目录按需拉，不随厂商清单一起下发）。 */
  catalogModels(provider: string, refresh?: boolean): Promise<CatalogModelList>;
  /** 探测一个端点**实际**提供的模型清单。
   *  id = 已注册条目（用它那条的连接配置，key 不过 wire）；
   *  或 baseUrl + apiKey + format = 还没进注册表的新端点。 */
  discoverModels(input: { id?: string; baseUrl?: string; apiKey?: string; format?: string }): Promise<DiscoverResult>;
}

/** 目录里的一个模型（后端 modelcatalog.Model 的 wire 形态）。 */
export interface CatalogModel {
  id: string;
  name?: string;
  context_window?: number;
  max_output_tokens?: number;
  tools?: boolean;
  vision?: boolean;
  json_output?: boolean;
  reasoning?: boolean;
  /** 非正常状态（deprecated / beta）；空 = 正常。 */
  status?: string;
  /** 发布日期 YYYY-MM-DD（目录按它倒序）。 */
  released?: string;
}

/** 目录里的一个厂商（后端 modelcatalog.Provider 的 wire 形态）。 */
export interface CatalogProvider {
  id: string;
  name: string;
  doc?: string;
  /** 端点地址：注册表 base_url 的建议值。 */
  api?: string;
  /** key 的环境变量名（表单提示用）。 */
  env?: string[];
  format: string;
  model_count: number;
  /** 只在 catalogModels 的结果里有明细。 */
  models?: CatalogModel[];
}

/** 厂商清单载荷。 */
export interface CatalogProviderList {
  fetched_at: string;
  /** 手上这份已过期且这次没刷新成功——UI 要如实提示，不假装新鲜。 */
  stale?: boolean;
  providers: CatalogProvider[];
}

/** 某厂商的模型清单载荷。 */
export interface CatalogModelList {
  fetched_at: string;
  stale?: boolean;
  provider: string;
  models: CatalogModel[];
}

/** 探测到的一个模型。元数据由后端按 id 从目录回填（best-effort）；缺省 = 未知。 */
export interface DiscoveredModel {
  id: string;
  name?: string;
  context_window?: number;
  max_output_tokens?: number;
  tools?: boolean;
  vision?: boolean;
  json_output?: boolean;
  reasoning?: boolean;
}

/** 端点探测结果。 */
export interface DiscoverResult {
  /** 实际请求的地址（用户填的 base_url 会被归一化，回显便于核对）。 */
  endpoint: string;
  format: string;
  models: DiscoveredModel[];
}
