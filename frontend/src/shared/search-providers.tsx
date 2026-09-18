// 网页搜索域：搜索渠道（多渠道 + 主渠道）——Agent 网页检索工具的后端。
// 预设渠道（Tavily/Brave/Serper/SearXNG）+ 自定义渠道；渠道配 apikey
// （或 SearXNG 填实例地址）即「已配置」；主渠道 = 搜索工具默认使用，
// 失败自动降级其它已配置渠道（后端语义，原型 UI 展示）。
// 原型：内存态 + 演示 key；后端化接真实搜索 API（Tavily 等）与降级链。
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { maskToken } from "./connections";

/** 一个搜索渠道。 */
export interface SearchProvider {
  id: string;
  name: string;
  desc: string;
  /** 获取 apikey 的入口（提示用户去哪配）。 */
  docsUrl?: string;
  /** 需要 apikey（SearXNG 自建不需要——填实例地址）。 */
  needsKey: boolean;
  /** 已配置的 key（空 = 未配置）。 */
  apiKey: string;
  /** 自建实例地址（SearXNG / 自定义渠道）。 */
  baseUrl?: string;
  /** 内置预设（不可删只能不配置）；自定义渠道可删。 */
  preset: boolean;
}

/** 渠道是否已配置（key 或实例地址就绪）。 */
export function isConfigured(p: SearchProvider): boolean {
  return p.needsKey ? p.apiKey.trim() !== "" : (p.baseUrl ?? "").trim() !== "";
}

const PRESETS: SearchProvider[] = [
  { id: "tavily", name: "Tavily", desc: "为 LLM 设计的搜索 API——结果结构化，Agent 友好", docsUrl: "https://app.tavily.com", needsKey: true, apiKey: "tavily-demo-8f3k2a9c", preset: true },
  { id: "brave", name: "Brave Search", desc: "独立索引的搜索 API，隐私友好", docsUrl: "https://brave.com/search/api/", needsKey: true, apiKey: "", preset: true },
  { id: "serper", name: "Serper.dev", desc: "Google 搜索的 API 封装", docsUrl: "https://serper.dev", needsKey: true, apiKey: "", preset: true },
  { id: "searxng", name: "SearXNG", desc: "自建元搜索引擎——无需 key，填实例地址", needsKey: false, baseUrl: "", apiKey: "", preset: true },
];

interface SearchProvidersValue {
  providers: SearchProvider[];
  /** 主渠道（搜索工具默认使用；自动回落首个已配置渠道）。 */
  primaryId: string;
  setPrimary: (id: string) => void;
  /** 更新渠道（key / baseUrl）。 */
  updateProvider: (id: string, patch: Partial<Pick<SearchProvider, "apiKey" | "baseUrl">>) => void;
  /** 添加自定义渠道（自建端点）。 */
  addCustom: (input: { name: string; baseUrl: string; apiKey?: string }) => void;
  /** 删除自定义渠道（预设不可删）。 */
  removeCustom: (id: string) => void;
}

const Ctx = createContext<SearchProvidersValue | null>(null);

export function SearchProvidersProvider({ children }: { children: ReactNode }) {
  const [providers, setProviders] = useState<SearchProvider[]>(PRESETS);
  const [primaryId, setPrimaryId] = useState("tavily");

  const updateProvider = useCallback(
    (id: string, patch: Partial<Pick<SearchProvider, "apiKey" | "baseUrl">>) =>
      setProviders((ps) => ps.map((p) => (p.id === id ? { ...p, ...patch } : p))),
    [],
  );
  const addCustom = useCallback(
    ({ name, baseUrl, apiKey }: { name: string; baseUrl: string; apiKey?: string }) =>
      setProviders((ps) => [...ps, {
        id: "custom-" + name,
        name,
        desc: "自定义搜索渠道",
        needsKey: (apiKey ?? "") !== "",
        apiKey: apiKey ?? "",
        baseUrl,
        preset: false,
      }]),
    [],
  );
  const removeCustom = useCallback(
    (id: string) => {
      setProviders((ps) => ps.filter((p) => p.id !== id));
      setPrimaryId((cur) => (cur === id ? providers.find((p) => p.id !== id && isConfigured(p))?.id ?? "" : cur));
    },
    [providers],
  );

  const value = useMemo(
    () => ({ providers, primaryId, setPrimary: setPrimaryId, updateProvider, addCustom, removeCustom }),
    [providers, primaryId, updateProvider, addCustom, removeCustom],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useSearchProviders(): SearchProvidersValue {
  const v = useContext(Ctx);
  if (!v) throw new Error("useSearchProviders 必须在 SearchProvidersProvider 内使用");
  return v;
}

export { maskToken };
