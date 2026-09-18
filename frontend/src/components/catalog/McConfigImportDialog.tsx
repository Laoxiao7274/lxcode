// MCP 配置导入弹窗：粘贴或选择文件（YAML / JSON）→ 校验 → 入拓展。
// 与工具/模块导入同款形态；mcpServers 事实形态直接贴（Claude Desktop /
// Cursor 的 JSON、常见 YAML 片段）；已存在的服务器跳过并汇总展示。
import { useRef, useState } from "react";
import { useAgents, type McServerSpec } from "../../shared/agents";
import { parseMcpConfig } from "../../shared/mcp-config";
import { useEscape } from "../../shared/popover";
import { Button, Textarea } from "../form";
import { IconChevronDown } from "../icons";

const FORMAT_EXAMPLE = `# YAML（mcpServers 键可省——顶层直接是映射也接受）
mcpServers:
  filesystem:
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/"]
    env:
      ROOT: /tmp
  remote:
    url: https://example.com/mcp/sse

# JSON 同样接受（Claude Desktop / Cursor 配置直接贴）`;

export function McConfigImportDialog({
  onClose,
  onImport,
}: {
  onClose: () => void;
  onImport: (servers: McServerSpec[]) => void;
}) {
  useEscape(true, onClose);
  const [text, setText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [showFormat, setShowFormat] = useState(true);
  const [result, setResult] = useState<{ added: number; skipped: string[] } | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const { mcpServers } = useAgents();

  const submit = () => {
    const parsed = parseMcpConfig(text, new Set(mcpServers.map((s) => s.id)));
    if (!parsed.ok) {
      setError(parsed.error ?? "导入失败");
      return;
    }
    onImport(parsed.servers ?? []);
    // 有跳过的先展示汇总再收（全部导入则直接关闭）
    const skipped = parsed.skipped ?? [];
    if (skipped.length > 0) setResult({ added: parsed.servers?.length ?? 0, skipped });
    else onClose();
  };

  const onFile = (file: File) => {
    void file.text().then((content) => {
      setText(content);
      setError(null);
    });
  };

  return (
    <div
      className="ag-doc-mask"
      role="dialog"
      aria-modal="true"
      aria-label="导入 MCP 配置"
      onPointerDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="ag-doc">
        <div className="ag-doc-head">
          <span className="ag-doc-title">导入 MCP 配置</span>
          <button type="button" className="ag-doc-close" onClick={onClose} aria-label="关闭">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="ag-doc-body">
          {result ? (
            <>
              <div className="ag-detail-desc">已导入 {result.added} 个服务器。</div>
              <div className="ag-warn">
                跳过 {result.skipped.length} 个已存在：{result.skipped.join("、")}——重贴配置不会覆盖，需要更新请编辑该服务器。
              </div>
              <div className="ag-edit-actions ti-foot">
                <Button variant="primary" data-cg="done" onClick={onClose}>
                  完成
                </Button>
              </div>
            </>
          ) : (
            <>
              <div className="ti-note">
                YAML 或 JSON（mcpServers 事实形态——Claude Desktop / Cursor 配置直接贴）；传输按字段推断（command = stdio / url = sse）。
              </div>
              <Textarea
                className="ti-input"
                value={text}
                onChange={(v) => {
                  setText(v);
                  setError(null);
                }}
                placeholder="mcpServers: …（粘贴配置）"
                ariaLabel="导入内容"
              />
              {error && <div className="ag-warn" role="alert">{error}</div>}
              <div className="ti-actions ti-actions-lead">
                <input
                  ref={fileRef}
                  type="file"
                  accept=".yaml,.yml,.json,application/json"
                  className="ti-file-hidden"
                  onChange={(e) => {
                    const f = e.target.files?.[0];
                    if (f) onFile(f);
                    e.target.value = "";
                  }}
                />
                <Button variant="primary" data-cg="pick-file" onClick={() => fileRef.current?.click()}>
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M13 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V10Z" />
                    <path d="M13 3v7h7" />
                  </svg>
                  选择文件
                </Button>
                <button
                  type="button"
                  className="ti-format-toggle"
                  data-open={showFormat}
                  aria-expanded={showFormat}
                  onClick={() => setShowFormat((v) => !v)}
                >
                  格式示例
                  <IconChevronDown size={10} />
                </button>
              </div>
              {showFormat && (
                <div className="ti-format">
                  <pre>{FORMAT_EXAMPLE}</pre>
                </div>
              )}
              <div className="ag-edit-actions ti-foot">
                <Button variant="ghost" onClick={onClose}>
                  取消
                </Button>
                <Button variant="primary" data-cg="do-import" disabled={!text.trim()} onClick={submit}>
                  导入
                </Button>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
