// React 设置组合层：模型注册表通过注入能力获取；纯映射与演示目录独立。
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import type { AgentSource, ApprovalMode, CatalogModel, CatalogModelList, CatalogProvider, CatalogProviderList, DiscoveredModel, DiscoverResult, ModelEntry } from "./types";
import { subscribeApprovalSync } from "./approval";
import { applyModelPatch, applyProviderPatch, catalogMetadata, mapModels, modelForProvider, providerKey, validateProviderPatch, type ModelPatch, type ProviderMeta, type ProviderPatch, type EffortId } from "./settings-models";
import { demoCatalogModelList, demoCatalogProviderList, demoModel, demoProviders, CONNECTABLE_PROVIDERS } from "./settings-catalog";
export type { ModelMeta, ModelPatch, ProviderMeta, ProviderPatch, EffortId } from "./settings-models";
export { CONNECTABLE_PROVIDERS } from "./settings-catalog";

export interface Settings {
  model: string; effort: EffortId; approval: ApprovalMode;
  showThinking: boolean; showFullOutput: boolean; keepAwake: boolean;
  enterToSend: boolean; personality: "friendly" | "pragmatic" | "none";
  /** 主题（浅色/深色/跟随系统——前端态，后端化时落配置）。 */
  theme: "light" | "dark" | "system";
  /** Git 新任务默认分支名。 */
  gitBranch: string;
}
const DEFAULTS: Settings = { model: "MYT", effort: "medium", approval: "confirm", showThinking: true,
  showFullOutput: true, keepAwake: false, enterToSend: true, personality: "pragmatic",
  theme: "light", gitBranch: "main" };
// 档位目录（后端协议值域——chat.send 的 effort 参数；仅对声明 reasoning
// 能力的模型生效，选择器在 ModelPicker 里按模型能力显隐）
export const EFFORTS: { id: EffortId; label: string; hint: string }[] = [
  { id: "minimal", label: "极低", hint: "几乎不思考，最快响应" },
  { id: "low", label: "低", hint: "轻度思考，速度优先" },
  { id: "medium", label: "中", hint: "均衡（默认）" },
  { id: "high", label: "高", hint: "深入思考，质量优先" },
];
// 权限模式目录（后端工具执行三档策略——chat.send 的 approval 参数）
export const APPROVALS = [
  { id: "confirm", label: "默认", hint: "低危自动执行，高危需确认" },
  { id: "auto", label: "完全访问", hint: "全部自动执行，几乎不打断；仅隔离环境用" },
  { id: "strict", label: "只读", hint: "只读取和搜索，不改文件、不执行命令" },
] as const;
interface ContextValue {
  settings: Settings; set(patch: Partial<Settings>): void;
  /** 改权限档：本地持久化 + **立刻发给后端**（运行中的一轮即刻生效）。
   *  与 set({approval}) 的区别就是后半句——只改本地设置要等下一次 chat.send
   *  才到后端，那时运行中的那一轮早按旧档位跑完了。 */
  applyApproval(mode: ApprovalMode): void;
  providers: ProviderMeta[];
  live: boolean; error: string | null;
  setProviderEnabled(id: string, on: boolean): void;
  setModelVisible(providerId: string, modelId: string, on: boolean): void;
  connectProvider(id: string): void;
  /** 探测某提供商的端点，返回候选清单（调用方渲染勾选面板——**不直接改注册表**）。 */
  fetchModels(id: string): Promise<DiscoverResult>;
  /** 目录厂商清单（live 走后端目录；demo 用演示目录，UI 只有一条代码路径）。 */
  catalogProviders(refresh?: boolean): Promise<CatalogProviderList>;
  /** 某厂商的模型明细（目录按需拉）。 */
  catalogModels(provider: string, refresh?: boolean): Promise<CatalogModelList>;
  /** 探测一个还没进注册表的端点（自定义提供商表单用）。 */
  discover(input: { baseUrl?: string; apiKey?: string; format?: string }): Promise<DiscoverResult>;
  /** 把勾选的目录模型写进注册表（元数据取自目录——不让用户手抄上下文窗口）。 */
  addCatalogModels(provider: CatalogProvider, models: CatalogModel[], apiKey: string): Promise<boolean>;
  /** 把探测到的模型加进已有提供商分组（只继承连接配置，不猜元数据）。 */
  addDiscovered(providerId: string, models: DiscoveredModel[]): Promise<boolean>;
  addModel(providerId: string, modelId: string): Promise<boolean>;
  updateModel(providerId: string, oldId: string, patch: ModelPatch): Promise<boolean>;
  /** 改提供商的连接配置（Base URL / API Key / 格式），一次写到该组**所有**模型。
   *  磁盘上没有提供商实体（这三个字段逐模型存），所以「改提供商的 key」就是
   *  「把这一组条目的连接配置一起改掉」——比「移除再重加」少丢一次模型配置。 */
  updateProvider(providerId: string, patch: ProviderPatch): Promise<boolean>;
  removeModel(providerId: string, modelId: string): void;
  addCustomProvider(input: { name: string; baseUrl: string; models: string[]; apiKey?: string }): void;
  disconnectProvider(id: string): void;
}
const Ctx = createContext<ContextValue | null>(null);
export function SettingsProvider({ source, children }: { source: AgentSource; children: ReactNode }) {
  const admin = source.modelAdmin;
  const [local, setLocal] = useState(DEFAULTS);
  const [demo, setDemo] = useState(demoProviders);
  const [snapshot, setSnapshot] = useState(() => admin?.models());
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    setSnapshot(admin?.models());
    return admin?.onModelsChanged(() => setSnapshot(admin.models()));
  }, [admin]);
  const providers = useMemo(() => snapshot ? mapModels(snapshot.models) : demo, [snapshot, demo]);
  // 后端角色绑定是唯一事实源：派生选中态，不反向 effect 写回旧选择。
  const settings = { ...local, model: admin ? snapshot?.roles.default ?? "" : local.model };
  const run = useCallback(async (action: () => Promise<unknown>) => {
    setError(null);
    try { await action(); return true; }
    catch (e) { setError(e instanceof Error ? e.message : String(e)); return false; }
  }, []);
  const set = useCallback((patch: Partial<Settings>) => {
    // 全部设置本地生效（keepAwake/theme/personality 前端态；模型在
    // live 模式走后端角色绑定——后端是事实源）
    const { model, ...supported } = patch;
    setLocal((s) => ({ ...s, ...supported, ...(!admin && model ? { model } : {}) }));
    if (admin && model) void run(() => admin.setRole("default", model));
  }, [admin, run]);
  // 反向同步：后端广播的权限档变更落到本地设置（多客户端/壳+浏览器同时开着时
  // 两边一致）。当前会话从事件流里跟——sessionFocused 是它的唯一事实源。
  useEffect(() => subscribeApprovalSync(source, (mode) => {
    setLocal((s) => (s.approval === mode ? s : { ...s, approval: mode }));
  }), [source]);
  const applyApproval = useCallback((mode: ApprovalMode) => {
    setLocal((s) => (s.approval === mode ? s : { ...s, approval: mode }));
    // 两件事都要做：本地持久化管下次开应用，这次调用管**当前**这一轮。
    void run(() => source.setApproval(mode));
  }, [source, run]);
  const setModelVisible = (pid: string, id: string, on: boolean) => {
    if (admin) { void run(() => admin.setModelEnabled(id, on)); return; }
    setDemo((ps) => ps.map((p) => p.id === pid ? { ...p, models: p.models.map((m) => m.id === id ? { ...m, visible: on } : m) } : p));
  };
  const setProviderEnabled = (id: string, on: boolean) => {
    if (admin) { void run(() => Promise.all(providers.find((p) => p.id === id)?.models.map((m) => admin.setModelEnabled(m.id, on)) ?? [])); return; }
    setDemo((ps) => ps.map((p) => p.id === id ? { ...p, enabled: on } : p));
  };
  const addModel = async (pid: string, id: string) => {
    if (admin) return run(() => admin.addModel(modelForProvider(admin.models().models, pid, id)));
    if (!id.trim() || demo.some((p) => p.models.some((m) => m.id === id.trim()))) return false;
    setDemo((ps) => ps.map((p) => p.id === pid ? { ...p, models: [...p.models, demoModel(id.trim())] } : p));
    return true;
  };
  const updateModel = async (pid: string, oldId: string, patch: ModelPatch) => {
    if (admin) return run(async () => {
      const entry = admin.models().models.find((m) => m.id === oldId);
      if (!entry) throw new Error("模型不存在");
      await admin.updateModel(applyModelPatch(entry, patch));
    });
    if (demo.some((p) => p.models.some((m) => m.id === patch.id && m.id !== oldId))) return false;
    setDemo((ps) => ps.map((p) => p.id === pid ? { ...p, models: p.models.map((m) => m.id === oldId ? { ...m, ...patch, efforts: [] } : m) } : p));
    if (local.model === oldId) set({ model: patch.id });
    return true;
  };
  /** 改提供商的连接配置（Base URL / API Key / 格式）——一次写到该组**所有**模型。
   *
   *  磁盘上这三个字段是逐模型存的（没有提供商实体），所以「改提供商的 key」就是
   *  「把这一组条目的连接配置一起改掉」。逐条收集失败并点名回抛：部分成功是最坏
   *  的结果（一半模型用新 key、一半还用旧的，而用户以为全改了）。 */
  const updateProvider = async (pid: string, patch: ProviderPatch) => {
    const invalid = validateProviderPatch(patch);
    if (invalid) { setError(invalid); return false; }
    if (admin) {
      const group = providers.find((p) => p.id === pid);
      if (!group) { setError("提供商不存在，请刷新后重试"); return false; }
      const ids = group.models.map((m) => m.id);
      return run(async () => {
        const failed: string[] = [];
        for (const id of ids) {
          // 每条都从**当前快照**重新取，不用循环外的快照：上一条写成功会让
          // 注册表变化，拿旧对象去覆盖会把并发改动抹掉。
          const entry = admin.models().models.find((m) => m.id === id);
          if (!entry) { failed.push(`${id}（已不在注册表中）`); continue; }
          try { await admin.updateModel(applyProviderPatch(entry, patch)); }
          catch (e) { failed.push(`${id}（${e instanceof Error ? e.message : String(e)}）`); }
        }
        if (failed.length) throw new Error(`${failed.length}/${ids.length} 个模型没更新成功: ${failed.join("；")}`);
      });
    }
    setDemo((ps) => ps.map((p) => p.id === pid
      ? { ...p, tagline: patch.baseUrl, baseUrl: patch.baseUrl, apiKey: patch.apiKey, format: patch.format }
      : p));
    return true;
  };
  const removeModel = (pid: string, id: string) => {
    if (admin) { void run(() => admin.removeModel(id)); return; }
    setDemo((ps) => ps.map((p) => p.id === pid ? { ...p, models: p.models.filter((m) => m.id !== id) } : p));
  };
  const connectProvider = (id: string) => {
    if (admin) { setError("真实模式请从模型目录选择厂商并填 API Key"); return; }
    setDemo((ps) => ps.map((p) => p.id === id ? { ...p, connected: true, enabled: true } : p));
  };
  // ---- 模型发现：目录（免 key，带元数据）与端点探测（自建端点）----
  //
  // 三条路径（目录 / 探测 / 手填 ID）都收敛到「候选清单 → 用户勾选 → 写注册表」，
  // 且都**不自动添加**：目录里有 6000+ 个模型，自动写入会把模型选择器淹掉。
  //
  // 读类（目录/探测）**抛错**给调用方内联展示（错误就发生在那个按钮旁边），
  // 写类（批量添加）走 run()——错误进设置面板顶部的 error 块。

  const fetchModels = useCallback(async (id: string): Promise<DiscoverResult> => {
    if (admin) {
      // 用该分组里任一条目的连接配置探测：key 取自注册表，不必再过一遍 wire。
      const entry = admin.models().models.find((m) => providerKey(m.base_url) === id);
      if (!entry) throw new Error("该提供商没有已注册的模型，无法推断端点");
      return admin.discoverModels({ id: entry.id });
    }
    // 演示模式没有真端点：保持老的「填一个演示模型」行为，并回演示清单让面板可点。
    const name = CONNECTABLE_PROVIDERS.find((p) => p.id === id)?.name ?? id;
    setDemo((ps) => ps.map((p) => p.id === id ? { ...p, models: p.models.length ? p.models : [demoModel(name + "-demo")] } : p));
    return { endpoint: `demo://${id}/v1/models`, format: "openai", models: [{ id: name + "-demo", name: name + " 演示模型" }] };
  }, [admin]);

  const catalogProviders = useCallback(
    async (refresh = false): Promise<CatalogProviderList> =>
      admin ? admin.catalogProviders(refresh) : demoCatalogProviderList(),
    [admin],
  );

  const catalogModels = useCallback(
    async (provider: string, refresh = false): Promise<CatalogModelList> =>
      admin ? admin.catalogModels(provider, refresh) : demoCatalogModelList(provider),
    [admin],
  );

  const discover = useCallback(
    async (input: { baseUrl?: string; apiKey?: string; format?: string }): Promise<DiscoverResult> => {
      if (admin) return admin.discoverModels(input);
      // 演示：把演示厂商的模型当作「探测结果」，让自定义表单的流程可点。
      const list = demoCatalogModelList("custom");
      return { endpoint: (input.baseUrl || "demo://") + "/v1/models", format: input.format ?? "openai",
        models: list.models.map((m) => ({ id: m.id, name: m.name })) };
    },
    [admin],
  );

  /** 批量写注册表：单个失败不中断整批（成功的留下），失败的逐条点名回抛——
   *  否则用户不知道 5 个里到底进了几个。 */
  const addBatch = async (ids: string[], build: (id: string) => Partial<Omit<ModelEntry, "id">> & { id: string }) => {
    if (!admin) return false;
    return run(async () => {
      const failed: string[] = [];
      for (const id of ids) {
        try { await admin.addModel(build(id)); }
        catch (e) { failed.push(`${id}（${e instanceof Error ? e.message : String(e)}）`); }
      }
      if (failed.length) throw new Error(`${failed.length} 个模型没加进去: ${failed.join("；")}`);
    });
  };

  const addCatalogModels = async (provider: CatalogProvider, models: CatalogModel[], apiKey: string) => {
    if (models.length === 0) return false;
    if (admin) {
      const byId = new Map(models.map((m) => [m.id, m]));
      return addBatch([...byId.keys()], (id) => {
        const m = byId.get(id)!;
        return {
          id,
          model: id,
          base_url: provider.api,
          api_key: apiKey || undefined,
          format: provider.format,
          display_name: `${provider.name} · ${m.name || id}`,
          ...catalogMetadata(m),
          enabled: true,
        };
      });
    }
    setDemo((ps) => ps.map((p) => p.id === provider.id
      ? { ...p, connected: true, enabled: true,
          models: [...p.models, ...models.map((m) => ({ ...demoModel(m.id), name: m.name ?? m.id }))] }
      : p));
    return true;
  };

  const addDiscovered = async (providerId: string, models: DiscoveredModel[]) => {
    if (models.length === 0) return false;
    if (admin) {
      const snapshot = admin.models().models;
      return addBatch(models.map((m) => m.id), (id) => modelForProvider(snapshot, providerId, id));
    }
    setDemo((ps) => ps.map((p) => p.id === providerId
      ? { ...p, models: [...p.models, ...models.map((m) => demoModel(m.id))] }
      : p));
    return true;
  };
  const addCustomProvider = ({ name, baseUrl, models, apiKey }:  { name: string; baseUrl: string; models: string[]; apiKey?: string }) => {
    if (admin) { void run(async () => {
      const url = new URL(baseUrl);
      if (!/^https?:$/.test(url.protocol)) throw new Error("端点必须使用 HTTP 或 HTTPS");
      for (const id of models.map((m) => m.trim()).filter(Boolean)) {
        await admin.addModel({ id, model: id, base_url: baseUrl, api_key: apiKey, display_name: name + " · " + id, enabled: true });
      }
    }); return; }
    setDemo((ps) => [...ps, { id: "custom-" + name, name, tagline: baseUrl, connected: true, enabled: true,
      color: "#8e6fbe", fetching: false, custom: true, baseUrl, apiKey: apiKey ?? "", format: "openai",
      models: models.map(demoModel) }]);
  };
  const disconnectProvider = (id: string) => {
    if (admin) { void run(async () => { for (const m of providers.find((p) => p.id === id)?.models ?? []) await admin.removeModel(m.id); }); return; }
    setDemo((ps) => ps.map((p) => p.id === id ? { ...p, connected: false, models: [] } : p));
  };
  return <Ctx.Provider value={{ settings, set, applyApproval, providers, live: Boolean(admin), error, setProviderEnabled,
    setModelVisible, connectProvider, fetchModels, catalogProviders, catalogModels, discover, addCatalogModels, addDiscovered,
    addModel, updateModel, updateProvider, removeModel, addCustomProvider, disconnectProvider }}>{children}</Ctx.Provider>;
}
export function useSettings() {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("SettingsProvider 未挂载");
  return ctx;
}
