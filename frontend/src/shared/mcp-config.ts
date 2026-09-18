// MCP 配置导入：接受 YAML 或 JSON（mcpServers 事实形态——Claude Desktop /
// Cursor 的 JSON 配置与常见的 YAML 片段都直接贴）。
//
//   mcpServers:            # 键可省——顶层直接是服务器映射也接受
//     filesystem:
//       command: npx       # stdio：可执行文件
//       args: ["-y", "…"]  # 可选，数组
//       env: { K: V }      # 可选，映射
//     remote:
//       url: https://…/sse # sse：端点
//
// 传输方式按字段推断（有 command = stdio；有 url = sse）——与真实
// 配置一致（配置里不写 transport，写的是字段本身）。
import { parse as parseYaml } from "yaml";
import type { McServerSpec } from "./agents";

export interface McConfigImportResult {
  ok: boolean;
  servers?: McServerSpec[];
  /** 已存在被跳过的服务器名（不报错——重新贴配置是常见工作流）。 */
  skipped?: string[];
  error?: string;
}

const asStringRecord = (v: unknown): Record<string, string> | null => {
  if (typeof v !== "object" || v === null || Array.isArray(v)) return null;
  const out: Record<string, string> = {};
  for (const [k, val] of Object.entries(v)) {
    if (typeof val !== "string") return null;
    out[k] = val;
  }
  return out;
};

/** 解析并校验导入文本；existingIds 用于跳过已存在（重贴配置 = 更新语义外的心智）。 */
export function parseMcpConfig(text: string, existingIds: Set<string>): McConfigImportResult {
  // YAML 是 JSON 的严格超集——先试 JSON（报错更准），失败再按 YAML 解析
  let doc: unknown;
  try {
    doc = JSON.parse(text);
  } catch {
    try {
      doc = parseYaml(text);
    } catch {
      return { ok: false, error: "解析失败——不是合法的 JSON 或 YAML" };
    }
  }
  if (typeof doc !== "object" || doc === null || Array.isArray(doc)) {
    return { ok: false, error: "顶层必须是对象" };
  }
  const d = doc as Record<string, unknown>;
  let entries: Record<string, unknown>;
  if (d.mcpServers !== undefined) {
    if (typeof d.mcpServers !== "object" || d.mcpServers === null || Array.isArray(d.mcpServers)) {
      return { ok: false, error: "mcpServers 必须是映射" };
    }
    entries = d.mcpServers as Record<string, unknown>;
    if (Object.keys(entries).length === 0) return { ok: false, error: "没有服务器条目" };
  } else {
    // 裸映射：所有值都应是对象（服务器定义）
    const values = Object.values(d);
    if (values.length === 0) return { ok: false, error: "没有服务器条目" };
    if (!values.every((v) => typeof v === "object" && v !== null && !Array.isArray(v))) {
      return { ok: false, error: "缺 mcpServers 键，或顶层不是服务器映射" };
    }
    entries = d;
  }

  const out: McServerSpec[] = [];
  const skipped: string[] = [];
  for (const [name, raw] of Object.entries(entries)) {
    if (!name.trim()) return { ok: false, error: "服务器名不能为空" };
    if (existingIds.has(name)) {
      skipped.push(name);
      continue;
    }
    const s = raw as Record<string, unknown>;
    const hasCommand = typeof s.command === "string" && s.command.trim() !== "";
    const hasUrl = typeof s.url === "string" && s.url.trim() !== "";
    if (hasCommand && hasUrl) {
      return { ok: false, error: `${name}：command 与 url 不能同时有（stdio / sse 二选一）` };
    }
    if (!hasCommand && !hasUrl) {
      return { ok: false, error: `${name}：缺 command（stdio）或 url（sse）` };
    }
    let args: string[] = [];
    if (s.args !== undefined) {
      if (!Array.isArray(s.args) || !s.args.every((a) => typeof a === "string")) {
        return { ok: false, error: `${name}：args 必须是字符串数组` };
      }
      args = s.args;
    }
    let env: Record<string, string> = {};
    if (s.env !== undefined) {
      const parsed = asStringRecord(s.env);
      if (!parsed) return { ok: false, error: `${name}：env 必须是字符串映射` };
      env = parsed;
    }
    if (s.transport !== undefined && s.transport !== "stdio" && s.transport !== "sse") {
      return { ok: false, error: `${name}：transport 只接受 stdio / sse（一般不用写——按字段推断）` };
    }
    out.push({
      id: name,
      desc: typeof s.description === "string" && s.description.trim() ? s.description.trim() : "导入的 MCP 服务器",
      transport: hasCommand ? "stdio" : "sse",
      command: hasCommand ? (s.command as string).trim() : "",
      args,
      env,
      url: hasUrl ? (s.url as string).trim() : "",
      enabled: true,
      custom: true,
    });
  }
  if (out.length === 0) {
    return { ok: false, error: "全部服务器已存在，跳过", skipped };
  }
  return { ok: true, servers: out, skipped };
}
