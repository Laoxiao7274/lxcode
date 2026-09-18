// 工具导入弹窗：粘贴或选择文件（固定格式 v1 的 JSON）→ 校验 → 入目录（自定义标记）。
// 格式契约见 shared/tool-import.ts 头注释——后端化时同一格式做插件分发。
import { useRef, useState } from "react";
import { useAgents, type ToolSpec } from "../../shared/agents";
import { parseToolImport } from "../../shared/tool-import";
import { useEscape } from "../../shared/popover";
import { Button, Textarea } from "../form";
import { IconChevronDown } from "../icons";

const FORMAT_EXAMPLE = `{
  "version": 1,
  "tools": [{
    "id": "my-tool",
    "desc": "一句话说明",
    "risk": "low",
    "source": "binary",
    "params": [{ "name": "path", "type": "string", "required": true }],
    "doc": "markdown 扩展文档（可选）"
  }]
}`;

export function ToolImportDialog({
  onClose,
  onImport,
}: {
  onClose: () => void;
  onImport: (tools: ToolSpec[]) => void;
}) {
  useEscape(true, onClose);
  const [text, setText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [showFormat, setShowFormat] = useState(true);
  const fileRef = useRef<HTMLInputElement>(null);
  const { tools } = useAgents();

  const submit = () => {
    const parsed = parseToolImport(text, new Set(tools.map((t) => t.id)));
    if (!parsed.ok) {
      setError(parsed.error ?? "导入失败");
      return;
    }
    onImport(parsed.tools ?? []);
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
      aria-label="导入工具"
      onPointerDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="ag-doc">
        <div className="ag-doc-head">
          <span className="ag-doc-title">导入工具</span>
          <button type="button" className="ag-doc-close" onClick={onClose} aria-label="关闭">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="ag-doc-body">
          <div className="ti-note">
            单个工具用「新建工具」表单；导入用于批量/分发——固定格式 v1，选择文件或在下方粘贴。
          </div>
          <div className="ti-actions ti-actions-lead">
            <input
              ref={fileRef}
              type="file"
              accept=".json,application/json"
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
          <Textarea
            className="ti-input"
            value={text}
            onChange={(v) => {
              setText(v);
              setError(null);
            }}
            placeholder="选择文件后在此预览——也可直接粘贴 JSON"
            ariaLabel="导入内容"
          />
          {error && <div className="ag-warn" role="alert">{error}</div>}
          <div className="ag-edit-actions ti-foot">
            <Button variant="ghost" onClick={onClose}>
              取消
            </Button>
            <Button variant="primary" data-cg="do-import" disabled={!text.trim()} onClick={submit}>
              导入
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
