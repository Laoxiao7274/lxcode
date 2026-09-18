// 模块（技能/模板）目录导入/导出格式 v1（固定契约——与工具导入对称；
// 后端化时同一格式做模块分发）：
//
//   {
//     "version": 1,
//     "modules": [{
//       "id": "deploy-checklist",     // 必填，目录内唯一
//       "desc": "一句话摘要",          // 必填
//       "kind": "process" | "skill",  // 必填（模板 / 技能）
//       "body": "# markdown 正文"      // 必填（注入 Agent 上下文的完整内容）
//     }]
//   }
//
// 校验纯函数（可独立测试）；错误消息面向用户并定位到条目下标。
import type { ContextModuleSpec } from "./agents";

export interface ModuleImportResult {
  ok: boolean;
  modules?: ContextModuleSpec[];
  error?: string;
}

/** 解析并校验导入文本；existingIds 用于查重（当前目录的全部 id）。 */
export function parseModuleImport(text: string, existingIds: Set<string>): ModuleImportResult {
  let doc: unknown;
  try {
    doc = JSON.parse(text);
  } catch {
    return { ok: false, error: "JSON 解析失败——检查格式与引号/逗号" };
  }
  if (typeof doc !== "object" || doc === null) return { ok: false, error: "顶层必须是对象" };
  const d = doc as { version?: unknown; modules?: unknown };
  if (d.version !== 1) return { ok: false, error: "version 必须是 1" };
  if (!Array.isArray(d.modules)) return { ok: false, error: "modules 必须是数组" };
  if (d.modules.length === 0) return { ok: false, error: "modules 不能为空" };

  const seen = new Set(existingIds);
  const out: ContextModuleSpec[] = [];
  for (let i = 0; i < d.modules.length; i++) {
    const at = `modules[${i}]`;
    const m = d.modules[i];
    if (typeof m !== "object" || m === null) return { ok: false, error: `${at} 必须是对象` };
    const e = m as Record<string, unknown>;
    if (typeof e.id !== "string" || !e.id.trim()) return { ok: false, error: `${at}.id 不能为空` };
    if (seen.has(e.id)) return { ok: false, error: `${at}.id 已存在: ${e.id}（目录内唯一）` };
    if (typeof e.desc !== "string" || !e.desc.trim()) return { ok: false, error: `${at}.desc 不能为空` };
    if (e.kind !== "process" && e.kind !== "skill") return { ok: false, error: `${at}.kind 必须是 process（模板）或 skill（技能）` };
    if (typeof e.body !== "string" || !e.body.trim()) return { ok: false, error: `${at}.body 不能为空` };

    seen.add(e.id);
    out.push({ id: e.id.trim(), desc: e.desc.trim(), kind: e.kind, body: e.body, custom: true });
  }
  return { ok: true, modules: out };
}

/** 序列化为导出文件（v1 固定格式；只导出用户自建条目——内置是种子）。 */
export function serializeModuleExport(modules: ContextModuleSpec[]): string {
  return JSON.stringify(
    {
      version: 1,
      modules: modules.map((m) => ({ id: m.id, desc: m.desc, kind: m.kind, body: m.body })),
    },
    null,
    2,
  );
}

/** 触发浏览器下载（data-lake：Blob + a[download]——浏览器与壳通用）。 */
export function downloadJson(filename: string, text: string): void {
  const blob = new Blob([text], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}
