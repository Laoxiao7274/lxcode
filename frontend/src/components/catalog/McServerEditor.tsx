// MCP 服务器编写器弹窗：自定义服务器的创建与编辑（名称/命令/描述）。
// 服务器是接入单元——能力（工具）在后端化时由 MCP 协议握手生成，
// 原型阶段工具目录的 mcp: 条目手工/导入关联（server 字段指回）。
import { useEffect, useRef, useState } from "react";
import { useAgents, type McServerSpec } from "../../shared/agents";
import { useEscape } from "../../shared/popover";
import { staggerIn } from "../../shared/motion";
import { Button, TextInput } from "../form";

export function McServerEditor({
  initial,
  isNew,
  onSave,
  onCancel,
}: {
  initial: McServerSpec;
  isNew: boolean;
  onSave: (server: McServerSpec) => void;
  onCancel: () => void;
}) {
  const [server, setServer] = useState<McServerSpec>(initial);
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
  const savable = id !== "" && !idTaken && server.name.trim() !== "" && server.command.trim() !== "";

  const set = <K extends keyof McServerSpec>(key: K, value: McServerSpec[K]) =>
    setServer((d) => ({ ...d, [key]: value }));

  return (
    <div
      className="ag-doc-mask"
      role="dialog"
      aria-modal="true"
      aria-label={isNew ? "添加 MCP 服务器" : `编辑 ${server.name}`}
      onPointerDown={(e) => e.target === e.currentTarget && onCancel()}
    >
      <div className="ag-doc">
        <div className="ag-doc-head">
          <span className="dd-icon" aria-hidden="true">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
            </svg>
          </span>
          <span className="ag-doc-title">{isNew ? "添加 MCP 服务器" : `编辑 · ${server.name}`}</span>
          <button type="button" className="ag-doc-close" onClick={onCancel} aria-label="关闭">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="ag-doc-body">
          <div ref={formRef}>
            <div className="cg-field">
              <span className="cg-field-label">id</span>
              <TextInput
                id="cg-server-id"
                className="cg-id-input"
                value={server.id}
                onChange={(v) => set("id", v)}
                placeholder="如：github（目录内唯一，工具 server 字段指向它）"
                aria-label="服务器 id"
              />
              {idTaken && <div className="ag-warn">id 已存在——服务器 id 必须唯一。</div>}
            </div>
            <div className="cg-field">
              <span className="cg-field-label">名称</span>
              <TextInput
                value={server.name}
                onChange={(v) => set("name", v)}
                placeholder="显示名（卡片标题）"
                aria-label="名称"
              />
            </div>
            <div className="cg-field">
              <span className="cg-field-label">命令 / URL</span>
              <TextInput
                className="cg-id-input"
                value={server.command}
                onChange={(v) => set("command", v)}
                placeholder="npx -y @modelcontextprotocol/server-xxx 或 https://…/sse"
                aria-label="命令"
              />
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
          </div>
          <div className="ag-edit-actions ti-foot">
            <Button variant="ghost" data-cg="cancel" onClick={onCancel}>
              取消
            </Button>
            <Button
              variant="primary"
              data-cg="save"
              disabled={!savable}
              onClick={() => onSave({ ...server, id, name: server.name.trim(), desc: server.desc.trim(), custom: true })}
            >
              {isNew ? "保存并添加" : "保存"}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
