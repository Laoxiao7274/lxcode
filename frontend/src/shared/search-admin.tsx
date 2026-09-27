// 网页搜索渠道域（设置面板）。
//
// live 模式（source.searchAdmin 存在）= 后端是唯一事实源：渠道清单由后端
// 内置适配器给出（前端不持有渠道列表——新增渠道只改后端，UI 自动出现），
// 配置写入 config/search.json 并广播 search.changed。
// demo 模式 = 内存演示渠道（浏览器里无后端也能看 UI）。
//
// 语义：搜索工具默认走主渠道，失败自动降级其它就绪渠道；零配置渠道
// （opt_in）必须用户手动启用才生效——「技术上能跑」不等于「想用它」。
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import type { AgentSource, SearchChannel, SearchChannelsSnapshot, SearchTestResult } from "./types";

/** 演示渠道（无后端时的 UI 骨架；与后端内置清单无关，只是形状一致）。 */
const DEMO_CHANNELS: SearchChannel[] = [
  {
    // 刻意默认就绪的零配置渠道：不填任何配置也能搜（走免配置通道），
    // 但**仍然接受** key（accepts_key）——有 key 走直连 API。
    id: "exa", label: "Exa", desc: "面向 AI 的语义搜索；不填 API Key 也能用（走免配置通道，有免费额度）",
    doc_url: "https://dashboard.exa.ai/api-keys", env_var: "EXA_API_KEY",
    needs_key: false, accepts_key: true, needs_url: false, default_ready: true,
    category: "free", category_label: "开箱可用",
    option_specs: [
      {
        key: "mcp_url", label: "免配置 MCP 端点", env_var: "EXA_MCP_URL",
        default: "https://mcp.exa.ai/mcp",
        hint: "留空时用官方端点；只有不填 API Key 时才会走这里。",
      },
    ],
    options: { mcp_url: "https://mcp.exa.ai/mcp" },
    // 磁盘上没有条目（用户什么都没配）——options 里的 mcp_url 是**默认值**，
    // 不是用户填的，所以 stored 必须是 false（否则空卡片上会出现「清除」按钮）。
    stored: false,
    api_key: "", enabled: true, primary: false, configured: true,
  },
  {
    id: "tavily", label: "Tavily", desc: "为 LLM 设计的搜索 API——结果结构化，Agent 友好",
    doc_url: "https://app.tavily.com", env_var: "TAVILY_API_KEY",
    needs_key: true, accepts_key: true, needs_url: false,
    category: "general", category_label: "通用搜索 API",
    api_key: "tavily-demo-8f3k2a9c", enabled: true, primary: true, configured: true, stored: true,
  },
  {
    id: "brave", label: "Brave Search", desc: "独立索引的搜索 API，隐私友好",
    doc_url: "https://brave.com/search/api/", env_var: "BRAVE_API_KEY",
    needs_key: true, accepts_key: true, needs_url: false,
    category: "general", category_label: "通用搜索 API",
    api_key: "", enabled: true, primary: false, configured: false, stored: false,
  },
  {
    id: "searxng", label: "SearXNG", desc: "自建元搜索引擎——无需 key，填实例地址",
    env_var: "", needs_key: false, accepts_key: false, needs_url: true,
    category: "free", category_label: "开箱可用",
    base_url: "", enabled: true, primary: false, configured: false, stored: false,
  },
  {
    id: "duckduckgo", label: "DuckDuckGo", desc: "免 key 抓取公开搜索页（稳定性低于正式 API，需手动启用）",
    doc_url: "https://duckduckgo.com/", env_var: "",
    needs_key: false, accepts_key: false, needs_url: false, opt_in: true,
    category: "free", category_label: "开箱可用",
    api_key: "", enabled: true, primary: false, configured: false, stored: false,
  },
  {
    // 带私有设置项的渠道：演示「面板按声明渲染输入项」这条链路
    // （后端同款形态见 brightdata 的 SERP zone）。
    id: "brightdata", label: "Bright Data", desc: "经 SERP zone 代理真实 Google 结果页（按次计费），需 zone 名 + API key",
    doc_url: "https://brightdata.com/cp/setting/users", env_var: "BRIGHTDATA_API_KEY",
    needs_key: true, accepts_key: true, needs_url: false,
    category: "serp", category_label: "SERP 代理",
    option_specs: [
      {
        key: "zone", label: "SERP zone", placeholder: "my_serp_zone",
        hint: "Bright Data 控制台里的 serp 类型 zone 名（只允许字母/数字/-/_）",
        env_var: "BRIGHTDATA_SERP_ZONE", required: true,
      },
    ],
    options: {},
    api_key: "", enabled: true, primary: false, configured: false, stored: false,
  },
];

interface SearchAdminValue {
  /** 渠道快照（含未配置的——用户要能看到「还能配什么」）。 */
  snapshot: SearchChannelsSnapshot;
  /** live 模式（后端是事实源）；false = 演示数据。 */
  live: boolean;
  /** 最近一次操作错误（表单内联提示）。 */
  error: string | null;
  clearError(): void;
  save(id: string, patch: { apiKey?: string; baseUrl?: string; options?: Record<string, string>; enabled?: boolean }): Promise<boolean>;
  remove(id: string): Promise<boolean>;
  setPrimary(id: string): Promise<boolean>;
  test(id: string, query?: string): Promise<SearchTestResult | null>;
}

const Ctx = createContext<SearchAdminValue | null>(null);

export function SearchAdminProvider({ source, children }: { source: AgentSource; children: ReactNode }) {
  const admin = source.searchAdmin;
  const [snapshot, setSnapshot] = useState<SearchChannelsSnapshot>(
    () => admin?.channels() ?? { channels: DEMO_CHANNELS, primary: "tavily", ready: true },
  );
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!admin) {
      setSnapshot({ channels: DEMO_CHANNELS, primary: "tavily", ready: true });
      return;
    }
    setSnapshot(admin.channels());
    return admin.onChannelsChanged(() => setSnapshot(admin.channels()));
  }, [admin]);

  /** 统一的操作包装：错误内联展示，不抛给调用方（表单不该崩）。 */
  const run = useCallback(async (action: () => Promise<unknown>): Promise<boolean> => {
    setError(null);
    try {
      await action();
      return true;
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      return false;
    }
  }, []);

  const save = useCallback(
    (id: string, patch: { apiKey?: string; baseUrl?: string; options?: Record<string, string>; enabled?: boolean }) => {
      if (admin) return run(() => admin.saveChannel(id, patch));
      // 演示模式：本地推导就绪态（与后端 channelReady 同语义）。
      setSnapshot((s) => ({
        ...s,
        channels: s.channels.map((c) => {
          if (c.id !== id) return c;
          const next = {
            ...c,
            api_key: patch.apiKey ?? c.api_key,
            base_url: patch.baseUrl ?? c.base_url,
            options: patch.options ?? c.options,
            enabled: patch.enabled ?? c.enabled,
          };
          const hasKey = !next.needs_key || (next.api_key ?? "").trim() !== "";
          const hasURL = !next.needs_url || (next.base_url ?? "").trim() !== "";
          // 必填设置项缺一不可——与后端 base.Configured 同一条判定。
          const hasRequired = (next.option_specs ?? [])
            .filter((spec) => spec.required)
            .every((spec) => (next.options?.[spec.key] ?? "").trim() !== "");
          // 保存过就是「磁盘上有条目」——与后端 present 同语义。
          return { ...next, configured: next.enabled && hasKey && hasURL && hasRequired, stored: true };
        }),
      }));
      return Promise.resolve(true);
    },
    [admin, run],
  );

  const remove = useCallback(
    (id: string) => {
      if (admin) return run(() => admin.removeChannel(id));
      // 演示：清空配置并让主渠道回落（与后端 RemoveChannel 同语义）。
      // 设置项复位成**声明默认值**（不是空对象）：后端删条目后
      // effectiveChannel 会重新解析出默认值，这里必须同款，否则演示态与
      // live 态的面板长得不一样（默认值消失了）。
      setSnapshot((s) => {
        const channels = s.channels.map((c) => {
          if (c.id !== id) return c;
          const defaults: Record<string, string> = {};
          for (const spec of c.option_specs ?? []) {
            if (spec.default) defaults[spec.key] = spec.default;
          }
          const hasKey = !c.needs_key;
          const hasURL = !c.needs_url;
          return {
            ...c, api_key: "", base_url: "", options: defaults, primary: false, stored: false,
            configured: c.enabled && hasKey && hasURL,
          };
        });
        const primary = s.primary === id ? channels.find((c) => c.configured)?.id : s.primary;
        return { ...s, channels, primary };
      });
      return Promise.resolve(true);
    },
    [admin, run],
  );

  const setPrimary = useCallback(
    (id: string) => {
      if (admin) return run(() => admin.setPrimary(id));
      setSnapshot((s) => ({ ...s, primary: id, channels: s.channels.map((c) => ({ ...c, primary: c.id === id })) }));
      return Promise.resolve(true);
    },
    [admin, run],
  );

  const test = useCallback(
    async (id: string, query?: string): Promise<SearchTestResult | null> => {
      if (!admin) {
        setError("演示模式没有真实渠道可测——启动后端后这里会打真实请求");
        return null;
      }
      setError(null);
      try {
        return await admin.testChannel(id, query);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        return null;
      }
    },
    [admin],
  );

  const value = useMemo<SearchAdminValue>(
    () => ({
      snapshot,
      live: Boolean(admin),
      error,
      clearError: () => setError(null),
      save,
      remove,
      setPrimary,
      test,
    }),
    [snapshot, admin, error, save, remove, setPrimary, test],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useSearchAdmin(): SearchAdminValue {
  const v = useContext(Ctx);
  if (!v) throw new Error("useSearchAdmin 必须在 SearchAdminProvider 内使用");
  return v;
}

/** 就绪渠道数（分组头与摘要用）。 */
export function readyCount(channels: SearchChannel[]): number {
  return channels.filter((c) => c.configured).length;
}

/** 按分类分组（保持后端给的分类顺序；空分类不出现）。 */
export function groupByCategory(channels: SearchChannel[]): Array<{ id: string; label: string; items: SearchChannel[] }> {
  const order: string[] = [];
  const map = new Map<string, SearchChannel[]>();
  for (const c of channels) {
    const key = c.category || "other";
    if (!map.has(key)) {
      map.set(key, []);
      order.push(key);
    }
    map.get(key)!.push(c);
  }
  return order.map((id) => ({
    id,
    label: map.get(id)![0].category_label || id,
    items: map.get(id)!,
  }));
}
