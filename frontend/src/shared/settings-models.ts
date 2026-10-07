import type { CatalogModel, ModelEntry } from "./types";

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
  // 连接配置（分组内首个条目的值）。磁盘上这三个字段是**逐模型**存的——没有
  // 提供商实体，所以「改提供商的 key」= 把这一组条目的连接配置一起改掉。
  baseUrl: string; apiKey: string; format: string;
}
export interface ModelPatch {
  id: string; name: string; desc: string; tags: string[];
  contextWindow: number; maxOutput: number;
}
/** 提供商级连接配置（三个字段都是组内共享的，改一次写全组）。 */
export interface ProviderPatch {
  baseUrl: string; apiKey: string; format: string;
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
        color: "#615ced", fetching: false, fetchedAt: "已配置", models: [],
        baseUrl: m.base_url, apiKey: m.api_key ?? "", format: m.format || "openai" };
      groups.set(id, p);
    }
    p.models.push({ id: m.id, name: m.display_name || m.model || m.id,
      desc: m.format === "anthropic" ? "Anthropic 格式" : "OpenAI 兼容",
      tags: [m.capabilities?.tools ? "工具" : "", m.capabilities?.vision ? "视觉" : "",
        m.capabilities?.reasoning ? "推理" : ""].filter(Boolean),
      // 档位派生自能力声明：未声明 reasoning 的模型整个强度入口隐藏——
      // 对非推理端点传 effort 参数会 400，选择器只在真实生效处出现。
      efforts: m.capabilities?.reasoning ? [...EFFORT_IDS] : [],
      // 未配 = 0 = 未知（**不编一个 128k**）：显示层用 kfmtLimit 说「未知」，
      // 而窗口未知时压缩不触发——编一个数会让用户以为压缩在保护他。
      contextWindow: m.context_window ?? 0,
      maxOutput: m.max_output_tokens ?? 0, visible: m.enabled });
  }
  return [...groups.values()];
}

/** 目录模型 → 注册表条目的元数据（拿不到就不填，绝不猜）。
 *
 *  输出上限不小于窗口时**不填它**：后端 validate 硬拒这种组合（`internal/config/types.go:87`
 *  ——输入+输出会超限），而目录里 14%（932/6554）的条目恰好如此——数据源自己把两者
 *  报成相等，照抄会让这些模型整条加不进去。留空 = 未知，由端点自己决定。 */
export function catalogMetadata(
  m: CatalogModel,
): Pick<ModelEntry, "context_window" | "max_output_tokens" | "capabilities"> {
  const ctx = m.context_window ?? 0;
  const out = m.max_output_tokens ?? 0;
  return {
    context_window: ctx || undefined,
    max_output_tokens: out > 0 && (ctx === 0 || out < ctx) ? out : undefined,
    capabilities: {
      tools: Boolean(m.tools),
      vision: Boolean(m.vision),
      json_output: Boolean(m.json_output),
      reasoning: Boolean(m.reasoning),
    },
  };
}

/** 目录模型的能力标签。措辞与 mapModels 逐字一致——候选行与已注册行必须能对上，
 *  否则用户勾选时看到「工具/推理」，加进去却变成别的字。 */
export function catalogTags(m: CatalogModel): string[] {
  return [m.tools ? "工具" : "", m.vision ? "视觉" : "", m.reasoning ? "推理" : ""].filter(Boolean);
}

export function modelForProvider(models: ModelEntry[], providerId: string, modelId: string): ModelEntry {  const id = modelId.trim();
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

/** 校验提供商连接配置，返回人话错误（null = 通过）。
 *
 *  判据与后端 `config.ModelConfig.validate` 一致（`internal/config/types.go:75-83`）：
 *  前端先拦是为了在按钮旁就地报错，而不是等一趟往返再显示到面板顶部的错误块。
 *  非空校验也在这：后端允许空 base_url 吗？不允许（scheme+host 都要有）。 */
export function validateProviderPatch(patch: ProviderPatch): string | null {
  const raw = patch.baseUrl.trim();
  if (!raw) return "Base URL 不能为空";
  let u: URL;
  try { u = new URL(raw); } catch { return "Base URL 不是合法地址"; }
  if (!/^https?:$/.test(u.protocol) || !u.host) return "Base URL 必须是带 http(s) 的完整地址";
  if (patch.format !== "openai" && patch.format !== "anthropic") {
    return `格式必须是 openai 或 anthropic：${patch.format}`;
  }
  return null;
}

/** 把提供商级连接配置套到一个模型条目上：**只动这三个字段**。
 *
 *  窗口/输出/能力/显示名/启停一律原样——改 key 不该动模型配置（那正是
 *  「移除再重加」最伤人的地方：所有模型的窗口与能力都得重配一遍）。 */
export function applyProviderPatch(entry: ModelEntry, patch: ProviderPatch): ModelEntry {
  return { ...entry, base_url: patch.baseUrl.trim(), api_key: patch.apiKey.trim(), format: patch.format };
}
