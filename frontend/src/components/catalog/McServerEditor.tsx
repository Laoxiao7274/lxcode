// MCP 服务器编辑器弹窗：既有服务器的编辑（命令/参数/环境变量/端点）。
// 注册不在这里——走「添加服务器」的配置粘贴（贴合事实工作流）。
// 建模对齐 MCP 事实标准（mcpServers 配置形态）：
//   stdio = command + args（逐个参数，不经 shell）+ env（API key 等）
//   sse   = url（远程事件流端点）
import { useEffect, useRef, useState } from "react";
import { useAgents, type McServerSpec } from "../../shared/agents";
import { useEscape } from "../../shared/popover";
import { staggerIn } from "../../shared/motion";
import { Button, Segmented, TextInput, Textarea } from "../form";

const TRANSPORT_OPTS = [
  { value: "stdio" as const, label: "本地命令", hint: "command + args 直起进程" },
  { value: "sse" as const, label: "远程 SSE", hint: "事件流端点 URL" },
];

/** env 对象 ↔ 文本（KEY=VALUE 每行一个）。 */
const envToText = (env: Record<string, string>) =>
  Object.entries(env).map(([k, v]) => `${k}=${v}`).join("\n");
const textToEnv = (text: string): Record<string, string> => {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const idx = line.indexOf("=");
    if (idx <= 0) continue;
    out[line.slice(0, idx).trim()] = line.slice(idx + 1).trim();
  }
  return out;
};

export function McServerEditor({
  initial,
  onSave,
  onCancel,
}: {
  initial: McServerSpec;
  onSave: (server: McServerSpec) => void;
  onCancel: () => void;
}) {
  const [server, setServer] = useState<McServerSpec>(initial);
  // 参数/环境变量以文本编辑（每行一个），保存时解析
  const [argsText, setArgsText] = useState(initial.args.join("\n"));
  const [envText, setEnvText] = useState(envToText(initial.env));
  const { mcpServers } = useAgents();
  const formRef = useRef<HTMLDivElement>(null);

  useEscape(true, onCancel);

  useEffect(() => {
    const el = formRef.current;
    if (!el) return;
    staggerIn(el.querySelectorAll(".cg-field"), { each: 0.045 });
  }, []);

  const id = server.id.trim();
  const idTaken = id !== "" && mcpServers.some((s) => s.id === id && s.id !== initial.id);
  const launchReady = server.transport === "stdio" ? server.command.trim() !== "" : server.url.trim() !== "";
  const savable = id !== "" && !idTaken && server.desc.trim() !== "" && launchReady;

  const set = <K extends keyof McServerSpec>(key: K, value: McServerSpec[K]) =>
    setServer((d) => ({ ...d, [key]: value }));

  return (
    <div
      className="ag-doc-mask"
      role="dialog"
      aria-modal="true"
      aria-label={`编辑 ${server.id}`}
      onPointerDown={(e) => e.target === e.currentTarget && onCancel()}
    >
      <div className="ag-doc">
        <div className="ag-doc-head">
          <span className="dd-icon" aria-hidden="true">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
            </svg>
          </span>
          <span className="ag-doc-title">编辑 · {server.id}</span>
          <button type="button" className="ag-doc-close" onClick={onCancel} aria-label="关闭">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="ag-doc-body">
          <div ref={formRef}>
            <div className="cg-field">
              <span className="cg-field-label">服务器名</span>
              <TextInput
                id="cg-server-id"
                className="cg-id-input"
                value={server.id}
                onChange={(v) => set("id", v)}
                placeholder="如：github（唯一——工具的 server 字段指向它）"
                aria-label="服务器名"
              />
              {idTaken && <div className="ag-warn">服务器名已存在——必须唯一。</div>}
            </div>
            <div className="cg-field">
              <span className="cg-field-label">描述</span>
              <TextInput
                value={server.desc}
                onChange={(v) => set("desc", v)}
                placeholder="这个服务器提供什么能力"
                aria-label="描述"
              />
            </div>
            <div className="cg-field">
              <span className="cg-field-label">传输方式</span>
              <Segmented options={TRANSPORT_OPTS} value={server.transport} onChange={(v) => set("transport", v)} ariaLabel="传输方式" />
            </div>
            {server.transport === "stdio" ? (
              <>
                <div className="cg-field">
                  <span className="cg-field-label">命令</span>
                  <TextInput
                    className="cg-id-input"
                    value={server.command}
                    onChange={(v) => set("command", v)}
                    placeholder="可执行文件，如 npx / uvx / node"
                    aria-label="命令"
                  />
                  <div className="cg-field-hint">进程直起（不经 shell）——可执行文件本身，不带参数。</div>
                </div>
                <div className="cg-field">
                  <span className="cg-field-label">参数</span>
                  <Textarea
                    className="mc-args-input"
                    value={argsText}
                    onChange={setArgsText}
                    placeholder={"每行一个参数，如：\n-y\n@modelcontextprotocol/server-filesystem\n/"}
                    ariaLabel="参数"
                  />
                  <div className="cg-field-hint">逐个传给命令——不经 shell 解析，引号按字面算。</div>
                </div>
                <div className="cg-field">
                  <span className="cg-field-label">环境变量</span>
                  <Textarea
                    className="mc-env-input"
                    value={envText}
                    onChange={setEnvText}
                    placeholder={"KEY=VALUE 每行一个（可选），如：\nGITHUB_TOKEN=ghp_xxx"}
                    ariaLabel="环境变量"
                  />
                </div>
              </>
            ) : (
              <div className="cg-field">
                <span className="cg-field-label">端点 URL</span>
                <TextInput
                  className="cg-id-input"
                  value={server.url}
                  onChange={(v) => set("url", v)}
                  placeholder="https://example.com/mcp/sse"
                  aria-label="端点 URL"
                />
              </div>
            )}
          </div>
          <div className="ag-edit-actions ti-foot">
            <Button variant="ghost" data-cg="cancel" onClick={onCancel}>
              取消
            </Button>
            <Button
              variant="primary"
              data-cg="save"
              disabled={!savable}
              onClick={() =>
                onSave({
                  ...server,
                  id,
                  desc: server.desc.trim(),
                  command: server.command.trim(),
                  url: server.url.trim(),
                  args: argsText.split("\n").map((s) => s.trim()).filter(Boolean),
                  env: textToEnv(envText),
                  custom: true,
                })
              }
            >
              保存
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
