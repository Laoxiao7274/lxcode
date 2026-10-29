// 模块导入弹窗：粘贴或选择文件（固定格式 v1 的 JSON）→ 校验 → 入目录。
// 与工具导入同款形态；文件上传走隐藏 input[type=file] 读文本。
import { useRef, useState } from "react";
import { useAgents, type ContextModuleSpec } from "../../shared/agents";
import { parseModuleImport } from "../../shared/module-import";
import { useEscape } from "../../shared/popover";
import { Button, Textarea } from "../form";
import { IconChevronDown } from "../icons";

const FORMAT_EXAMPLE = `{
  "version": 1,
  "modules": [{
    "id": "deploy-checklist",
    "desc": "一句话摘要",
    "kind": "process",
    "body": "# 标题\\n\\n正文（markdown）"
  }]
}`;

const KIND_HINT = { process: "模板（process）", skill: "技能（skill）" } as const;

export function ModuleImportDialog({
  kind,
  onClose,
  onImport,
}: {
  /** 入口页签的默认类型（与新建同源：模板页签导入默认 process）。 */
  kind: ContextModuleSpec["kind"];
  onClose: () => void;
  onImport: (mods: ContextModuleSpec[]) => void;
}) {
  useEscape(true, onClose);
  const [text, setText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [showFormat, setShowFormat] = useState(true);
  const fileRef = useRef<HTMLInputElement>(null);
  const { modules } = useAgents();

  const submit = () => {
    const parsed = parseModuleImport(text, new Set(modules.map((m) => m.id)));
    if (!parsed.ok) {
      setError(parsed.error ?? "导入失败");
      return;
    }
    onImport(parsed.modules ?? []);
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
      aria-label={`导入${KIND_HINT[kind]}`}
      onPointerDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="ag-doc">
        <div className="ag-doc-head">
          <span className="ag-doc-title">导入{KIND_HINT[kind]}</span>
          <button type="button" className="ag-doc-close" onClick={onClose} aria-label="关闭">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="ag-doc-body">
          <div className="ti-note">
            固定格式 v1 · id 拓展内唯一 · kind 取 process（模板）/ skill（技能）——校验通过后作为「自定义」条目入拓展。
          </div>
          <Textarea
            className="ti-input"
            value={text}
            onChange={(v) => {
              setText(v);
              setError(null);
            }}
            placeholder='粘贴 JSON…（{"version":1,"modules":[…]}）'
            ariaLabel="导入内容"
          />
          {error && <div className="ag-warn" role="alert">{error}</div>}
          <div className="ti-actions">
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
            <Button variant="ghost" data-cg="pick-file" onClick={() => fileRef.current?.click()}>
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
        </div>
      </div>
    </div>
  );
}
