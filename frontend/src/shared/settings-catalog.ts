// 仅演示模式的目录；不代表远端实际模型或已验证的能力。
import type { CatalogModel, CatalogModelList, CatalogProviderList } from "./types";
import type { ProviderMeta, ModelMeta } from "./settings-models";
export const demoModel = (id: string): ModelMeta => ({ id, name: id, desc: "演示模型",
  tags: ["工具"], efforts: [], contextWindow: 128_000, maxOutput: 8_000, visible: true });
export const CONNECTABLE_PROVIDERS = [
  { id: "openai", name: "OpenAI", tagline: "演示目录", color: "#0d0d0d" },
  { id: "anthropic", name: "Anthropic", tagline: "演示目录", color: "#d97757" },
  { id: "qwen", name: "Qwen", tagline: "演示目录", color: "#615ced" },
  { id: "openrouter", name: "OpenRouter", tagline: "演示目录", color: "#8e8ea0" },
  { id: "lmstudio", name: "LM Studio", tagline: "演示目录", color: "#b48c5f" },
];
/** 演示模式的连接配置（假地址：演示不会真去连它）。 */
const demoConn = (id: string) => ({ baseUrl: `https://api.${id}.example.com`, apiKey: "", format: "openai" });
export function demoProviders(): ProviderMeta[] {
  return [{ id: "myt", name: "MYT 网关", tagline: "演示数据，不连接模型", connected: true,
    enabled: true, color: "#10a37f", fetching: false, ...demoConn("myt"),
    models: [demoModel("MYT"), demoModel("MYT-Deep")] },
    ...CONNECTABLE_PROVIDERS.map((p) => ({ ...p, connected: false, enabled: false, fetching: false, ...demoConn(p.id), models: [] }))];
}

// ---- 演示目录（后端 model.catalog.* 的载荷形状）----
//
// 演示模式没有后端也不联网，所以这里把内存里的演示提供商映射成同一套载荷形状：
// 好处是**连接提供商的二级流程只有一条代码路径**（live 与 demo 共用），
// 而不是在 UI 里到处写 `live ? ... : ...` 的分叉。

/** 演示厂商清单（api 是假地址：演示模式不会真去连它）。 */
export function demoCatalogProviderList(): CatalogProviderList {
  return {
    fetched_at: new Date().toISOString(),
    providers: CONNECTABLE_PROVIDERS.map((p) => ({
      id: p.id,
      name: p.name,
      api: `https://api.${p.id}.example.com`,
      env: [`${p.id.toUpperCase()}_API_KEY`],
      format: "openai",
      model_count: 2,
    })),
  };
}

/** 演示厂商的模型明细。 */
export function demoCatalogModelList(provider: string): CatalogModelList {
  const name = CONNECTABLE_PROVIDERS.find((p) => p.id === provider)?.name ?? provider;
  const models: CatalogModel[] = [
    { id: `${provider}-chat`, name: `${name} Chat`, context_window: 128_000, max_output_tokens: 8_000,
      tools: true, reasoning: true, released: "2026-01-01" },
    { id: `${provider}-mini`, name: `${name} Mini`, context_window: 64_000, max_output_tokens: 4_000,
      tools: true, vision: true, released: "2026-02-01" },
  ];
  return { fetched_at: new Date().toISOString(), provider, models };
}
