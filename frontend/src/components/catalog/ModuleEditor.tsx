// 模块编写器弹窗：自定义技能/模板的创建与编辑——「缺流程就补一个」的作者面。
// 弹窗形态（宽版 720px：正文编辑要空间，左表单右实时预览的格局保留）。
// 类型由入口定死（新建模板/新建技能分开口）——两类语义不同（模板单选
// 注入、技能多选注入），不在表单里切换。
import { useEffect, useRef, useState } from "react";
import { useAgents, type ContextModuleSpec } from "../../shared/agents";
import { Markdown } from "../../shared/markdown";
import { useEscape } from "../../shared/popover";
import { Button, TextInput, Textarea } from "../form";
import { staggerIn } from "../../shared/motion";

const KIND_LABEL: Record<ContextModuleSpec["kind"], { name: string; hint: string }> = {
  process: { name: "模板", hint: "工作方式 · Agent 单选注入" },
  skill: { name: "技能", hint: "领域知识与方法 · Agent 多选注入" },
};

export function ModuleEditor({
  initial,
  isNew,
  onSave,
  onCancel,
}: {
  initial: ContextModuleSpec;
  isNew: boolean;
  onSave: (mod: ContextModuleSpec) => void;
  onCancel: () => void;
}) {
  const [mod, setMod] = useState<ContextModuleSpec>(initial);
  const { modules } = useAgents();
  const formRef = useRef<HTMLDivElement>(null);

  useEscape(true, onCancel);

  // 分节交错入场（弹窗每次挂载播一次）
  useEffect(() => {
    const el = formRef.current;
    if (!el) return;
    staggerIn(el.querySelectorAll(".cg-field, .cg-body-input"), { each: 0.045 });
  }, []);

  const id = mod.id.trim();
  const idTaken = id !== "" && modules.some((x) => x.id === id && x.id !== initial.id);
  const savable = id !== "" && !idTaken && mod.desc.trim() !== "";

  const set = <K extends keyof ContextModuleSpec>(key: K, value: ContextModuleSpec[K]) =>
    setMod((d) => ({ ...d, [key]: value }));

  return (
    <div
      className="ag-doc-mask"
      role="dialog"
      aria-modal="true"
      aria-label={isNew ? `新建${KIND_LABEL[mod.kind].name}` : `编辑 ${mod.id}`}
      onPointerDown={(e) => e.target === e.currentTarget && onCancel()}
    >
      <div className="ag-doc ag-doc-wide">
        <div className="ag-doc-head">
          <span className="ag-doc-title">
            {isNew ? `新建${KIND_LABEL[mod.kind].name}` : `编辑 · ${mod.id}`}
          </span>
          <div className="ag-detail-pills">
            <span className="ag-pill src">{KIND_LABEL[mod.kind].name}</span>
          </div>
          <button type="button" className="ag-doc-close" onClick={onCancel} aria-label="关闭">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="ag-doc-body">
          <div className="ag-edit" ref={formRef}>
            <div className="ag-form">
              <section className="ag-sec">
                <div className="ag-sec-title">基本信息</div>
                <div className="cg-field">
                  <span className="cg-field-label">id</span>
                  <TextInput
                    id="cg-mod-id"
                    className="cg-id-input"
                    value={mod.id}
                    onChange={(v) => set("id", v)}
                    placeholder="如：deploy-checklist（目录内唯一）"
                    aria-label="模块 id"
                  />
                  {idTaken && <div className="ag-warn">id 已存在——目录条目的 id 必须唯一。</div>}
                </div>
                <div className="cg-field">
                  <span className="cg-field-label">摘要</span>
                  <TextInput
                    value={mod.desc}
                    onChange={(v) => set("desc", v)}
                    placeholder="Agent 靠它判断何时用——chips 的 tooltip 与卡片副文"
                    aria-label="摘要"
                  />
                </div>
                <div className="cg-field">
                  <span className="cg-field-label">类型</span>
                  <div className="cg-type-row">
                    <span className="ag-pill src">{KIND_LABEL[mod.kind].name}</span>
                    <span className="cg-type-hint">{KIND_LABEL[mod.kind].hint}</span>
                  </div>
                </div>
              </section>

              <section className="ag-sec">
                <div className="ag-sec-title">正文（markdown）</div>
                <Textarea
                  className="cg-body-input"
                  value={mod.body}
                  onChange={(v) => set("body", v)}
                  placeholder={"# 标题\n\n正文——实际注入 Agent 上下文的完整内容"}
                  ariaLabel="模块正文"
                />
              </section>

              <div className="ag-edit-actions">
                <Button variant="ghost" data-cg="cancel" onClick={onCancel}>
                  取消
                </Button>
                <Button
                  variant="primary"
                  data-cg="save"
                  disabled={!savable}
                  onClick={() => onSave({ ...mod, id, desc: mod.desc.trim(), custom: true })}
                >
                  {isNew ? "保存并添加" : "保存"}
                </Button>
              </div>
            </div>

            <aside className="ag-preview">
              <div className="ag-preview-label">预览</div>
              <div className="cg-preview-box">
                <div className="ag-detail-title">{mod.id || "（id）"}</div>
                <div className="ag-detail-pills">
                  <span className="ag-pill src">{KIND_LABEL[mod.kind].name}</span>
                  <span className="ag-pill src">自定义</span>
                </div>
                <div className="ag-detail-desc">{mod.desc || "（摘要）"}</div>
                <div className="ag-detail-md">
                  <Markdown text={mod.body || "（正文预览——左侧编写后展示）"} />
                </div>
              </div>
              <div className="ag-preview-hint">保存后进入目录；Agent 组装的 chips 里即时可选。</div>
            </aside>
          </div>
        </div>
      </div>
    </div>
  );
}
