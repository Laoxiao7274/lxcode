// 工具目录导入格式 v1（固定契约——后端化时同一格式做插件的分发载荷）：
//
//   {
//     "version": 1,
//     "tools": [{
//       "id": "my-tool",              // 必填，目录内唯一
//       "desc": "一句话说明",           // 必填
//       "risk": "low" | "high",        // 必填（风险分级）
//       "source": "builtin" | "binary" | "mcp",  // 必填（来源）
//       "params": [{ "name": "path", "type": "string", "required": true, "desc": "…" }],
//       "doc": "markdown 扩展文档（可选）"
//     }]
//   }
//
// 校验纯函数（可独立测试）；错误消息面向用户并定位到条目下标。
import type { ToolSpec } from "./agents";

export interface ToolImportResult {
  ok: boolean;
  tools?: ToolSpec[];
  error?: string;
}

/** 解析并校验导入文本；existingIds 用于查重（当前目录的全部 id）。 */
export function parseToolImport(text: string, existingIds: Set<string>): ToolImportResult {
  let doc: unknown;
  try {
    doc = JSON.parse(text);
  } catch {
    return { ok: false, error: "JSON 解析失败——检查格式与引号/逗号" };
  }
  if (typeof doc !== "object" || doc === null) return { ok: false, error: "顶层必须是对象" };
  const d = doc as { version?: unknown; tools?: unknown };
  if (d.version !== 1) return { ok: false, error: "version 必须是 1" };
  if (!Array.isArray(d.tools)) return { ok: false, error: "tools 必须是数组" };
  if (d.tools.length === 0) return { ok: false, error: "tools 不能为空" };

  const seen = new Set(existingIds);
  const out: ToolSpec[] = [];
  for (let i = 0; i < d.tools.length; i++) {
    const at = `tools[${i}]`;
    const t = d.tools[i];
    if (typeof t !== "object" || t === null) return { ok: false, error: `${at} 必须是对象` };
    const e = t as Record<string, unknown>;
    if (typeof e.id !== "string" || !e.id.trim()) return { ok: false, error: `${at}.id 不能为空` };
    if (seen.has(e.id)) return { ok: false, error: `${at}.id 已存在: ${e.id}（目录内唯一）` };
    if (typeof e.desc !== "string" || !e.desc.trim()) return { ok: false, error: `${at}.desc 不能为空` };
    if (e.risk !== "low" && e.risk !== "high") return { ok: false, error: `${at}.risk 必须是 low 或 high` };
    if (e.source !== "builtin" && e.source !== "binary" && e.source !== "mcp") {
      return { ok: false, error: `${at}.source 必须是 builtin / binary / mcp` };
    }
    if (e.doc !== undefined && typeof e.doc !== "string") return { ok: false, error: `${at}.doc 必须是字符串` };

    let params: ToolSpec["params"];
    if (e.params !== undefined) {
      if (!Array.isArray(e.params)) return { ok: false, error: `${at}.params 必须是数组` };
      params = [];
      for (let j = 0; j < e.params.length; j++) {
        const p = e.params[j];
        if (typeof p !== "object" || p === null) return { ok: false, error: `${at}.params[${j}] 必须是对象` };
        const pe = p as Record<string, unknown>;
        if (typeof pe.name !== "string" || !pe.name) return { ok: false, error: `${at}.params[${j}].name 不能为空` };
        params.push({
          name: pe.name,
          type: typeof pe.type === "string" ? pe.type : "string",
          required: pe.required === true,
          ...(typeof pe.desc === "string" ? { desc: pe.desc } : {}),
        });
      }
    }

    seen.add(e.id);
    out.push({
      id: e.id.trim(),
      desc: e.desc.trim(),
      risk: e.risk,
      source: e.source,
      ...(params ? { params } : {}),
      ...(typeof e.doc === "string" && e.doc ? { doc: e.doc } : {}),
      custom: true,
    });
  }
  return { ok: true, tools: out };
}
