// React 设置组合层：模型注册表通过注入能力获取；纯映射与演示目录独立。
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import type { AgentSource } from "./types";
import { applyModelPatch, mapModels, modelForProvider, type ModelPatch, type ProviderMeta, type EffortId } from "./settings-models";
import { demoModel, demoProviders, CONNECTABLE_PROVIDERS } from "./settings-catalog";
export type { ModelMeta, ModelPatch, ProviderMeta, EffortId } from "./settings-models";
export { CONNECTABLE_PROVIDERS } from "./settings-catalog";

export interface Settings {
  model: string; effort: EffortId; approval: "auto" | "confirm" | "strict";
  showThinking: boolean; showFullOutput: boolean; keepAwake: boolean;
  enterToSend: boolean; personality: "friendly" | "pragmatic" | "none";
}
const DEFAULTS: Settings = { model: "MYT", effort: "medium", approval: "confirm", showThinking: true,
  showFullOutput: true, keepAwake: false, enterToSend: true, personality: "pragmatic" };
export const EFFORTS: { id: EffortId; label: string; hint: string }[] = [];
export const APPROVALS = [{ id: "confirm", label: "固定确认策略", hint: "低危自动执行；高危需确认，模式切换未实现" }];
interface ContextValue {
  settings: Settings; set(patch: Partial<Settings>): void; providers: ProviderMeta[];
  live: boolean; error: string | null;
  setProviderEnabled(id: string, on: boolean): void;
  setModelVisible(providerId: string, modelId: string, on: boolean): void;
  connectProvider(id: string): void; fetchModels(id: string): void;
  addModel(providerId: string, modelId: string): Promise<boolean>;
  updateModel(providerId: string, oldId: string, patch: ModelPatch): Promise<boolean>;
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
    // 未接通的设置在状态入口也禁用，避免其它调用方绕过 UI。
    const { effort: _e, approval: _a, keepAwake: _k, personality: _p, model, ...supported } = patch;
    setLocal((s) => ({ ...s, ...supported, ...(!admin && model ? { model } : {}) }));
    if (admin && model) void run(() => admin.setRole("default", model));
  }, [admin, run]);
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
  const removeModel = (pid: string, id: string) => {
    if (admin) { void run(() => admin.removeModel(id)); return; }
    setDemo((ps) => ps.map((p) => p.id === pid ? { ...p, models: p.models.filter((m) => m.id !== id) } : p));
  };
  const connectProvider = (id: string) => {
    if (admin) { setError("真实模式请使用自定义提供商填写端点和模型"); return; }
    setDemo((ps) => ps.map((p) => p.id === id ? { ...p, connected: true, enabled: true } : p));
  };
  const fetchModels = (id: string) => {
    if (admin) { setError("远端模型发现尚未实现；当前列表由后端注册表同步"); return; }
    const name = CONNECTABLE_PROVIDERS.find((p) => p.id === id)?.name ?? id;
    setDemo((ps) => ps.map((p) => p.id === id ? { ...p, models: p.models.length ? p.models : [demoModel(name + "-demo")] } : p));
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
      color: "#8e6fbe", fetching: false, custom: true, models: models.map(demoModel) }]);
  };
  const disconnectProvider = (id: string) => {
    if (admin) { void run(async () => { for (const m of providers.find((p) => p.id === id)?.models ?? []) await admin.removeModel(m.id); }); return; }
    setDemo((ps) => ps.map((p) => p.id === id ? { ...p, connected: false, models: [] } : p));
  };
  return <Ctx.Provider value={{ settings, set, providers, live: Boolean(admin), error, setProviderEnabled,
    setModelVisible, connectProvider, fetchModels, addModel, updateModel, removeModel, addCustomProvider, disconnectProvider }}>{children}</Ctx.Provider>;
}
export function useSettings() {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("SettingsProvider 未挂载");
  return ctx;
}
