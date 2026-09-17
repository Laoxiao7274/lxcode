// 仅演示模式的目录；不代表远端实际模型或已验证的能力。
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
export function demoProviders(): ProviderMeta[] {
  return [{ id: "myt", name: "MYT 网关", tagline: "演示数据，不连接模型", connected: true,
    enabled: true, color: "#10a37f", fetching: false, models: [demoModel("MYT"), demoModel("MYT-Deep")] },
    ...CONNECTABLE_PROVIDERS.map((p) => ({ ...p, connected: false, enabled: false, fetching: false, models: [] }))];
}
