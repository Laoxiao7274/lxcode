// ModelAdminSource 域（模型注册表直通；settings 面板数据源）：请求编排 + 缓存。
// 连接层（connection.ts）只管传输；这里的 applyModelList 同时服务两条路——
// 握手并行组的 model.list 刷新（经 onInitList 注册）与 model.changed 事件的
// 副作用（经 onEventSideEffect 注册）。
import type { CatalogModelList, CatalogProviderList, DiscoverResult, ModelAdminSource, ModelEntry } from "../../shared/types";
import type { WsConnection } from "./connection";

export class ModelAdmin implements ModelAdminSource {
  /** 模型注册表快照（model.changed 驱动刷新；settings 面板的数据源）。 */
  private modelsCache: ModelEntry[] = [];
  private rolesCache: Record<string, string> = {};
  private listeners = new Set<() => void>();

  constructor(private readonly conn: WsConnection) {
    // 握手并行组：model.list 的结果落这里
    conn.onInitList("model.list", (r) => this.applyModelList(r));
    // 事件副作用：模型注册表变更——重拉列表（settings 的 providers 数据源）
    conn.onEventSideEffect((method) => {
      if (method !== "model.changed") return;
      this.conn.call("model.list")
        .then((r) => this.applyModelList(r))
        .catch((e) => this.conn.opError(`刷新模型列表失败: ${e.message}`));
    });
  }

  models(): { models: ModelEntry[]; roles: Record<string, string> } {
    return { models: this.modelsCache, roles: this.rolesCache };
  }

  /** 订阅模型列表变化（返回退订）。 */
  onModelsChanged(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  addModel(entry: Partial<Omit<ModelEntry, "id">> & { id: string; base_url?: string }): Promise<void> {
    return this.conn.call("model.add", { ...entry, id: entry.id }).then(() => undefined);
  }

  updateModel(entry: ModelEntry): Promise<void> {
    return this.conn.call("model.update", entry).then(() => undefined);
  }

  removeModel(id: string): Promise<void> {
    return this.conn.call("model.remove", { id }).then(() => undefined);
  }

  setModelEnabled(id: string, enabled: boolean): Promise<void> {
    return this.conn.call("model.enable", { id, enabled }).then(() => undefined);
  }

  setRole(role: string, modelId: string): Promise<void> {
    return this.conn.call("role.set", { role, model_id: modelId }).then(() => undefined);
  }

  /** 模型目录的厂商清单（免 key；后端按 TTL 缓存，refresh 强制重拉）。 */
  catalogProviders(refresh = false): Promise<CatalogProviderList> {
    return this.conn.call("model.catalog.list", { refresh }) as Promise<CatalogProviderList>;
  }

  /** 某厂商的模型明细（目录按需拉——厂商清单里不带模型明细）。 */
  catalogModels(provider: string, refresh = false): Promise<CatalogModelList> {
    return this.conn.call("model.catalog.models", { provider, refresh }) as Promise<CatalogModelList>;
  }

  /** 探测端点的模型清单。key 只在后端用：给了 id 就不必再送 key。 */
  discoverModels(input: { id?: string; baseUrl?: string; apiKey?: string; format?: string }): Promise<DiscoverResult> {
    return this.conn.call("model.discover", {
      id: input.id,
      base_url: input.baseUrl,
      api_key: input.apiKey,
      format: input.format,
    }) as Promise<DiscoverResult>;
  }

  /** model.list 结果 → 缓存 + 通知（model.changed 事件也走这里）。 */
  private applyModelList(result: unknown) {
    const r = result as { models?: ModelEntry[]; roles?: Record<string, string> };
    if (!r || !Array.isArray(r.models)) return;
    this.modelsCache = r.models;
    this.rolesCache = r.roles ?? {};
    this.listeners.forEach((l) => l());
  }
}
