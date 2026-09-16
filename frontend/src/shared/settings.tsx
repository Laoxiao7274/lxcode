// Mock 设置：模型 / 推理强度 / 高危确认模式 + 提供商目录。
// 浏览器原型模式 = 本地 mock 数据；壳（WSAgent）模式 = 后端模型注册表
// （model.list/model.changed 事实源；CRUD 调 model.* 方法）。
// 提供商 + 模型两级结构参考 OpenCode Desktop（provider → models，可见性开关）。
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { getAgentSource } from "../agent";
import { WSAgent } from "../agent/ws";

export interface Settings {
  model: string;
  effort: EffortId;
  approval: "auto" | "confirm" | "strict";
  /** 思考链显示（对话里的推理过程折叠块）。 */
  showThinking: boolean;
  /** 命令输出完整展示（Codex General 同款；关闭折叠为摘要）。 */
  showFullOutput: boolean;
  /** 生成时阻止休眠（Codex General 同款）。 */
  keepAwake: boolean;
  /** Enter 发送（关闭则 Cmd+Enter 多行——Codex General 同款）。 */
  enterToSend: boolean;
  /** 回答语气（Codex Personalization：friendly/pragmatic/none）。 */
  personality: "friendly" | "pragmatic" | "none";
}

/* ---------- 提供商目录（OpenCode 式：提供商 → 模型，两级可见性开关） ---------- */

/** 推理档位全集。各家机制不同：OpenAI 是 reasoning.effort 枚举（xhigh 仅
 *  gpt-5.1/codex 系）；Claude 是 thinking.budget_tokens 预算（档位=预算映射）；
 *  R1/旧o1 不可调（efforts 空数组，UI 隐藏档位）；max 是客户端私有"预算拉满"档。 */
export type EffortId = "minimal" | "low" | "medium" | "high" | "xhigh" | "max";

export interface ModelMeta {
  id: string;
  name: string;
  desc: string;
  /** 能力标签：推理 / 视觉 / 工具调用。 */
  tags: string[];
  /** 该模型支持的推理档位（空 = 不支持调节，UI 隐藏档位）。 */
  efforts: EffortId[];
  /** 上下文窗口（tokens）。 */
  contextWindow: number;
  /** 最大输出（tokens）。 */
  maxOutput: number;
  /** 模型级可见性——是否出现在模型选择器。 */
  visible: boolean;
  /** 手动添加的模型（区别于拉取到的）。 */
  manual?: boolean;
}

export interface ProviderMeta {
  id: string;
  name: string;
  tagline: string;
  /** 已配置凭据（连接过 / 内置）。 */
  connected: boolean;
  /** 提供商级开关——一键显示/隐藏其全部模型。 */
  enabled: boolean;
  /** 字母标底色（无品牌图标的原型替代）。 */
  color: string;
  models: ModelMeta[];
  /** 正在拉取模型列表（模拟远端 API）。 */
  fetching: boolean;
  /** 上次获取时间（展示用）。 */
  fetchedAt?: string;
  /** 自定义提供商（断开即整体移除）。 */
  custom?: boolean;
}

export interface ModelPatch {
  id: string;
  name: string;
  desc: string;
  tags: string[];
  efforts: EffortId[];
  contextWindow: number;
  maxOutput: number;
}

const model = (
  id: string,
  desc: string,
  tags: string[],
  efforts: EffortId[],
  contextWindow: number,
  maxOutput: number,
  visible = true,
): ModelMeta => ({ id, name: id, desc, tags, efforts, contextWindow, maxOutput, visible });

/** 远端模型目录——「获取模型」时按提供商返回（模拟 GET /models）。
 *  档位声明参考各家 API：openai minimal~xhigh；anthropic 预算三档；
 *  deepseek-chat 不思考；qwen3 thinking 预算三档；本地模型不可调。 */
const REMOTE_MODELS: Record<string, ModelMeta[]> = {
  openai: [
    model("gpt-5.2", "旗舰 · 综合最强", ["推理", "工具", "视觉"], ["minimal", "low", "medium", "high", "xhigh"], 272_000, 128_000),
    model("gpt-5.1-codex", "代码特化", ["工具"], ["low", "medium", "high", "xhigh", "max"], 272_000, 128_000),
    model("gpt-5.2-mini", "轻量快速", ["工具"], ["low", "medium", "high"], 272_000, 64_000),
  ],
  qwen: [
    model("qwen3-max", "通义旗舰", ["推理", "工具", "视觉"], ["low", "medium", "high"], 256_000, 64_000),
    model("qwen3.5-coder", "代码特化", ["工具"], ["low", "medium", "high"], 128_000, 32_000),
  ],
  anthropic: [
    model("claude-opus-4.5", "旗舰 · 深度推理", ["推理", "工具", "视觉"], ["low", "medium", "high"], 200_000, 64_000),
    model("claude-sonnet-4.5", "均衡型", ["工具", "视觉"], ["low", "medium", "high"], 200_000, 64_000),
  ],
  openrouter: [
    model("moonshotai/kimi-k2", "聚合 · Kimi K2", ["工具"], [], 128_000, 8_000),
    model("minimax/minimax-m2", "聚合 · MiniMax M2", ["推理", "工具"], ["low", "medium", "high"], 128_000, 16_000),
  ],
  lmstudio: [
    model("qwen3-14b-instruct", "本地 · 14B", ["工具"], [], 32_000, 8_000),
    model("deepseek-r1-distill-32b", "本地 · 蒸馏 R1", ["推理"], [], 64_000, 8_000),
  ],
};

/** 内置可连接提供商（连接目录）。 */
export const CONNECTABLE_PROVIDERS: { id: string; name: string; tagline: string; color: string }[] = [
  { id: "openai", name: "OpenAI", tagline: "GPT 系列", color: "#0d0d0d" },
  { id: "qwen", name: "Qwen", tagline: "阿里云百炼", color: "#615ced" },
  { id: "anthropic", name: "Anthropic", tagline: "Claude 系列", color: "#d97757" },
  { id: "openrouter", name: "OpenRouter", tagline: "聚合网关 · 300+ 模型", color: "#8e8ea0" },
  { id: "lmstudio", name: "LM Studio", tagline: "本地模型", color: "#b48c5f" },
];

const INITIAL_PROVIDERS: ProviderMeta[] = [
  {
    id: "myt",
    name: "MYT 网关",
    tagline: "本机网关 · 免配置",
    connected: true,
    enabled: true,
    color: "#10a37f",
    fetching: false,
    fetchedAt: "内置",
    models: [
      model("MYT", "本机网关 · 日常任务", ["工具"], ["low", "medium", "high"], 128_000, 8_000),
      model("MYT-Deep", "本机网关 · 深度推理", ["推理", "工具"], ["low", "medium", "high", "xhigh"], 256_000, 16_000),
      model("MYT-Vision", "本机网关 · 视觉理解", ["视觉"], [], 128_000, 8_000, false),
    ],
  },
  {
    id: "deepseek",
    name: "DeepSeek",
    tagline: "官方 API",
    connected: true,
    enabled: true,
    color: "#4d6bfe",
    fetching: false,
    fetchedAt: "内置",
    models: [
      model("deepseek-chat", "通用对话", ["工具"], [], 128_000, 8_000),
      model("deepseek-reasoner", "深度推理", ["推理", "工具"], ["low", "medium", "high"], 128_000, 64_000),
    ],
  },
  {
    id: "openai",
    name: "OpenAI",
    tagline: "GPT 系列",
    connected: false,
    enabled: false,
    color: "#0d0d0d",
    fetching: false,
    models: [],
  },
  {
    id: "qwen",
    name: "Qwen",
    tagline: "阿里云百炼",
    connected: false,
    enabled: false,
    color: "#615ced",
    fetching: false,
    models: [],
  },
];

const DEFAULTS: Settings = {
  model: "MYT",
  effort: "medium",
  approval: "confirm",
  showThinking: true,
  showFullOutput: true,
  keepAwake: false,
  enterToSend: true,
  personality: "pragmatic",
};

export const EFFORTS: { id: EffortId; label: string; hint: string }[] = [
  { id: "minimal", label: "最低", hint: "最省，直答为主（gpt-5 系）" },
  { id: "low", label: "低", hint: "快，省 token" },
  { id: "medium", label: "中", hint: "均衡" },
  { id: "high", label: "高", hint: "慢，想得更深" },
  { id: "xhigh", label: "超高", hint: "gpt-5.1 / codex 系才有" },
  { id: "max", label: "极致", hint: "思考预算拉满（客户端私有档）" },
];

export const APPROVALS: { id: Settings["approval"]; label: string; hint: string }[] = [
  { id: "auto", label: "完全访问", hint: "高权限，几乎不打断；仅在可随时销毁的隔离环境用" },
  { id: "confirm", label: "默认", hint: "工作区内自动改与跑命令，越界才询问（推荐）" },
  { id: "strict", label: "只读", hint: "规划、审查、问答，不改文件、不联网" },
];

interface ProviderOps {
  providers: ProviderMeta[];
  /** 提供商级开关（联动其全部模型的选择器可见性）。 */
  setProviderEnabled: (id: string, on: boolean) => void;
  /** 模型级可见性开关。 */
  setModelVisible: (providerId: string, modelId: string, on: boolean) => void;
  /** 连接已有提供商（输入了 Key 之后）——模型列表待获取。 */
  connectProvider: (id: string) => void;
  /** 从提供商拉取模型列表（模拟远端，异步合并新增）。 */
  fetchModels: (id: string) => void;
  /** 在提供商里手动添加模型。返回 false = ID 已存在。 */
  addModel: (providerId: string, modelId: string) => boolean;
  /** 编辑模型（ID/名称/标签/上下文窗口/最大输出）。返回 false = 新 ID 冲突。 */
  updateModel: (providerId: string, oldModelId: string, patch: ModelPatch) => boolean;
  /** 从提供商删除模型。 */
  removeModel: (providerId: string, modelId: string) => void;
  /** 添加自定义提供商（OpenAI 兼容）。 */
  addCustomProvider: (input: { name: string; baseUrl: string; models: string[] }) => void;
  /** 断开提供商（内置移回可连接列表并清空模型；自定义整体移除）。 */
  disconnectProvider: (id: string) => void;
}

const Ctx = createContext<{
  settings: Settings;
  set: (patch: Partial<Settings>) => void;
} & ProviderOps>({
  settings: DEFAULTS,
  set: () => {},
  providers: [],
  setProviderEnabled: () => {},
  setModelVisible: () => {},
  connectProvider: () => {},
  fetchModels: () => {},
  addModel: () => false,
  updateModel: () => false,
  removeModel: () => {},
  addCustomProvider: () => {},
  disconnectProvider: () => {},
});

export function SettingsProvider({ children }: { children: ReactNode }) {
  const [settings, setSettings] = useState<Settings>(DEFAULTS);
  const [providers, setProviders] = useState<ProviderMeta[]>(INITIAL_PROVIDERS);
  // 全部 action 走函数式 setState + useCallback 稳定身份，context value
  // 用 useMemo 锁定——否则 provider 每次渲染都新建 value，消费者全树重渲染。
  const set = useCallback((patch: Partial<Settings>) => setSettings((s) => ({ ...s, ...patch })), []);

  // ---- 壳模式：后端模型注册表为事实源（model.list 拉取 + model.changed 刷新） ----
  const source = getAgentSource();
  const ws = source instanceof WSAgent ? source : null;
  const [backendTick, setBackendTick] = useState(0);

  // model.changed / 初始拉取 → 重算 providers（按 base_url host 聚合成 provider 分组）
  useEffect(() => {
    if (!ws) return;
    const off = ws.onModelsChanged(() => setBackendTick((n) => n + 1));
    // 连接建立后 model.list 到达（onModelsChanged 触发）也走这里——首次挂载
    // 时列表可能还没到，tick 变化后重算即可
    return off;
  }, [ws]);

  const effectiveProviders = useMemo(() => {
    if (!ws) return providers; // 浏览器原型：本地 mock
    void backendTick;
    const { models, roles } = ws.models();
    const byHost = new Map<string, ProviderMeta>();
    for (const m of models) {
      let host: string;
      try {
        host = new URL(m.base_url).host;
      } catch {
        host = m.base_url || "未知";
      }
      const tagline = new URL(m.base_url).protocol === "https:" ? host : host + "（本地）";
      if (!byHost.has(host)) {
        byHost.set(host, {
          id: host,
          name: host.split(".").length > 1 ? host.split(".")[0].replace(/^api-?/, "") : host,
          tagline,
          connected: true,
          enabled: true,
          color: "#615ced",
          fetching: false,
          fetchedAt: m.display_name ? "已配置" : "内置",
          models: [],
        });
      }
      const p = byHost.get(host)!;
      p.models.push({
        id: m.id,
        name: m.display_name || m.id,
        desc: m.format === "anthropic" ? "Anthropic 格式" : "OpenAI 兼容",
        tags: [m.capabilities?.tools ? "工具" : "", m.capabilities?.vision ? "视觉" : ""].filter(Boolean),
        efforts: [], // 后端无档位概念——UI 隐藏档位
        contextWindow: m.context_window || 128_000,
        maxOutput: m.max_output_tokens || 8_000,
        visible: m.enabled,
      });
    }
    // 当前默认模型（settings.model 指向后端 id；roles.default 是事实源）
    const defaultId = roles["default"];
    if (defaultId && settings.model !== defaultId) {
      // 异步同步选中态（避免渲染中 setState）——下一轮渲染生效
      setTimeout(() => set({ model: defaultId }), 0);
    }
    return [...byHost.values()];
  }, [ws, backendTick, providers, settings.model, set]);

  // ---- 原型模式操作（浏览器演示；壳模式下也被后端路径取代） ----

  const setProviderEnabled = useCallback(
    (id: string, on: boolean) => setProviders((ps) => ps.map((p) => (p.id === id ? { ...p, enabled: on } : p))),
    [],
  );

  const setModelVisible = useCallback(
    (providerId: string, modelId: string, on: boolean) => {
      if (ws) {
        ws.setModelEnabled(modelId, on).catch(() => {});
        return;
      }
      setProviders((ps) =>
        ps.map((p) =>
          p.id === providerId
            ? { ...p, models: p.models.map((m) => (m.id === modelId ? { ...m, visible: on } : m)) }
            : p,
        ),
      );
    },
    [ws],
  );

  const connectProvider = useCallback(
    (id: string) =>
      setProviders((ps) =>
        ps.map((p) => (p.id === id ? { ...p, connected: true, enabled: true, models: [], fetching: false, fetchedAt: undefined } : p)),
      ),
    [],
  );

  const fetchModels = useCallback((id: string) => {
    setProviders((ps) => ps.map((p) => (p.id === id && !p.fetching ? { ...p, fetching: true } : p)));
    // 模拟远端 GET /models（800ms）——合并新增，不动已有（保留可见性等本地状态）
    window.setTimeout(() => {
      setProviders((ps) =>
        ps.map((p) => {
          if (p.id !== id) return p;
          const remote = REMOTE_MODELS[id] ?? [];
          const known = new Set(p.models.map((m) => m.id));
          const merged = [...p.models, ...remote.filter((m) => !known.has(m.id))];
          return { ...p, fetching: false, fetchedAt: "刚刚", models: merged };
        }),
      );
    }, 800);
  }, []);

  const addModel = useCallback(
    (providerId: string, modelId: string) => {
      const trimmed = modelId.trim();
      if (!trimmed) return false;
      if (ws) {
        // 壳模式：后端注册表 add（host 推导 base_url 不可靠——沿用 provider 的
        // 既有 base_url：从当前分组里取第一条的 base_url）
        const prov = effectiveProviders.find((p) => p.id === providerId);
        void prov; // 基础 add：模型 id 直接当 model 名（OpenAI 兼容惯例）
        ws.addModel({ id: trimmed, model: trimmed }).catch(() => {});
        return true;
      }
      let ok = true;
      setProviders((ps) =>
        ps.map((p) => {
          if (p.id !== providerId) return p;
          if (p.models.some((m) => m.id === trimmed)) {
            ok = false;
            return p;
          }
          return {
            ...p,
            models: [...p.models, { ...model(trimmed, "手动添加", [], [], 128_000, 8_000), manual: true }],
          };
        }),
      );
      return ok;
    },
    [ws, effectiveProviders],
  );

  const updateModel = useCallback(
    (providerId: string, oldId: string, patch: ModelPatch) => {
      const newId = patch.id.trim();
      const name = patch.name.trim();
      if (!newId || !name) return false;
      if (ws) {
        // 后端更新：以现有条目为底，套 patch（保留 base_url/api_key 等）
        const entry = ws.models().models.find((m) => m.id === oldId);
        if (entry) {
          ws.updateModel({ ...entry, id: newId, display_name: name }).catch(() => {});
        }
        if (settings.model === oldId) set({ model: newId });
        return true;
      }
      let ok = true;
      setProviders((ps) =>
        ps.map((p) => {
          if (p.id !== providerId) return p;
          if (p.models.some((m) => m.id === newId && m.id !== oldId)) {
            ok = false;
            return p;
          }
          return {
            ...p,
            models: p.models.map((m) =>
              m.id === oldId
                ? { ...m, id: newId, name, desc: patch.desc, tags: [...patch.tags], efforts: [...patch.efforts], contextWindow: patch.contextWindow, maxOutput: patch.maxOutput }
                : m,
            ),
          };
        }),
      );
      // 当前默认模型被改了 ID——同步重指向，chip 不悬空
      if (ok && settings.model === oldId) set({ model: newId });
      return ok;
    },
    [settings.model, set, ws],
  );

  const removeModel = useCallback(
    (providerId: string, modelId: string) => {
      if (ws) {
        ws.removeModel(modelId).catch(() => {});
        return;
      }
      setProviders((ps) =>
        ps.map((p) => (p.id === providerId ? { ...p, models: p.models.filter((m) => m.id !== modelId) } : p)),
      );
    },
    [ws],
  );

  const addCustomProvider = useCallback(
    ({ name, baseUrl, models }: { name: string; baseUrl: string; models: string[] }) => {
      if (ws) {
        // 壳模式：逐条注册到后端（provider 概念由 base_url 分组自然涌现）
        for (const m of models.map((x) => x.trim()).filter(Boolean)) {
          ws.addModel({ id: m, model: m, base_url: baseUrl, display_name: name + " · " + m }).catch(() => {});
        }
        return;
      }
      setProviders((ps) => [
        ...ps,
        {
          id: "custom-" + (name.toLowerCase().replace(/[^a-z0-9]+/g, "-") || "custom"),
          name,
          tagline: baseUrl.replace(/^https?:\/\//, ""),
          connected: true,
          enabled: true,
          color: "#8e6fbe",
          fetching: false,
          fetchedAt: "表单声明",
          custom: true,
          models: models
            .map((m) => m.trim())
            .filter((m) => m.length > 0)
            .map((m) => ({ ...model(m, "自定义模型", ["工具"], [], 128_000, 8_000) })),
        },
      ]);
    },
    [ws],
  );

  const disconnectProvider = useCallback(
    (id: string) => {
      // 壳模式下断开 = 删除该 host 分组的全部模型（后端事实源）
      if (ws) {
        const prov = effectiveProviders.find((p) => p.id === id);
        for (const m of prov?.models ?? []) ws.removeModel(m.id).catch(() => {});
        return;
      }
      setProviders((ps) => {
        const target = ps.find((p) => p.id === id);
        if (target?.custom) return ps.filter((p) => p.id !== id);
        return ps.map((p) =>
          p.id === id ? { ...p, connected: false, enabled: false, models: [], fetching: false, fetchedAt: undefined } : p,
        );
      });
    },
    [ws, effectiveProviders],
  );

  // 选中默认模型 → 同步后端角色绑定（壳模式）
  useEffect(() => {
    if (!ws || !settings.model) return;
    const known = ws.models().models.some((m) => m.id === settings.model);
    if (known && ws.models().roles["default"] !== settings.model) {
      ws.setRole("default", settings.model).catch(() => {});
    }
  }, [ws, settings.model]);

  const value = useMemo(
    () => ({ settings, set, providers: effectiveProviders, setProviderEnabled, setModelVisible, connectProvider, fetchModels, addModel, updateModel, removeModel, addCustomProvider, disconnectProvider }),
    [settings, effectiveProviders, set, setProviderEnabled, setModelVisible, connectProvider, fetchModels, addModel, updateModel, removeModel, addCustomProvider, disconnectProvider],
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useSettings() {
  return useContext(Ctx);
}
