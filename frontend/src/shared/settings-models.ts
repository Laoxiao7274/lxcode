import type { ModelEntry } from "./types";

export type EffortId = "minimal" | "low" | "medium" | "high";
/** 全部推理档位（固定 4 档——后端协议值域：minimal/low/medium/high）。 */
export const EFFORT_IDS: EffortId[] = ["minimal", "low", "medium", "high"];
export interface ModelMeta {
  id: string; name: string; desc: string; tags: string[]; efforts: EffortId[];
  contextWindow: number; maxOutput: number; visible: boolean; manual?: boolean;
}
export interface ProviderMeta {
  id: string; name: string; tagline: string; connected: boolean; enabled: boolean;
  color: string; models: ModelMeta[]; fetching: boolean; fetchedAt?: string; custom?: boolean;
}
export interface ModelPatch {
  id: string; name: string; desc: string; tags: string[];
  contextWindow: number; maxOutput: number;
}

// 按完整端点分组，而非 host：同 host 的不同代理路径不共享凭据或协议。
export function providerKey(baseUrl: string): string {
  try { return new URL(baseUrl).href; } catch { return baseUrl; }
}

export function mapModels(models: ModelEntry[]): ProviderMeta[] {
  const groups = new Map<string, ProviderMeta>();
  for (const m of models) {
    const id = providerKey(m.base_url);
    let host = m.base_url || "无效端点";
    try { host = new URL(m.base_url).host; } catch { /* 旧配置坏 URL 仍可展示供用户修复。 */ }
    let p = groups.get(id);
    if (!p) {
      p = { id, name: host, tagline: m.base_url, connected: true, enabled: true,
        color: "#615ced", fetching: false, fetchedAt: "已配置", models: [] };
      groups.set(id, p);
    }
    p.models.push({ id: m.id, name: m.display_name || m.model || m.id,
      desc: m.format === "anthropic" ? "Anthropic 格式" : "OpenAI 兼容",
      tags: [m.capabilities?.tools ? "工具" : "", m.capabilities?.vision ? "视觉" : "",
        m.capabilities?.reasoning ? "推理" : ""].filter(Boolean),
      // 档位派生自能力声明：未声明 reasoning 的模型整个强度入口隐藏——
      // 对非推理端点传 effort 参数会 400，选择器只在真实生效处出现。
      efforts: m.capabilities?.reasoning ? [...EFFORT_IDS] : [],
      contextWindow: m.context_window || 128_000,
      maxOutput: m.max_output_tokens || 8_000, visible: m.enabled });
  }
  return [...groups.values()];
}

export function modelForProvider(models: ModelEntry[], providerId: string, modelId: string): ModelEntry {
  const id = modelId.trim();
  if (!id) throw new Error("模型 ID 不能为空");
  if (models.some((m) => m.id === id)) throw new Error("模型 ID 已存在");
  const template = models.find((m) => providerKey(m.base_url) === providerId);
  if (!template) throw new Error("提供商不存在，请刷新模型列表");
  // 仅继承连接配置；模型专属能力/上下文不得从不同模型猜测。
  return { id, model: id, base_url: template.base_url, api_key: template.api_key,
    format: template.format, enabled: true };
}

export function applyModelPatch(entry: ModelEntry, patch: ModelPatch): ModelEntry {
  if (patch.id.trim() !== entry.id) throw new Error("后端不支持修改模型 ID，请新增模型");
  return { ...entry, display_name: patch.name.trim(), context_window: patch.contextWindow,
    max_output_tokens: patch.maxOutput, capabilities: { ...entry.capabilities,
      tools: patch.tags.includes("工具"), vision: patch.tags.includes("视觉"),
      reasoning: patch.tags.includes("推理") } };
}
