// 模块编写器：自定义技能/模板的创建与编辑——「缺流程就补一个」的作者面。
// 正文 markdown 实时预览（与注入 Agent 上下文的最终形态一致）。
// 类型由入口页签定死（新建模板/新建技能分开口）——两类语义不同
// （模板单选注入、技能多选注入），不在表单里切换。
// 工具不在此列：工具走导入（固定格式 v1），见 ToolImportDialog。
import { useState } from "react";
import { useAgents, type ContextModuleSpec } from "../../shared/agents";
import { Markdown } from "../../shared/markdown";
import { Button, TextInput, Textarea } from "../form";

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

  const id = mod.id.trim();
  const idTaken = id !== "" && modules.some((x) => x.id === id && x.id !== initial.id);
  const savable = id !== "" && !idTaken && mod.desc.trim() !== "";

  const set = <K extends keyof ContextModuleSpec>(key: K, value: ContextModuleSpec[K]) =>
    setMod((d) => ({ ...d, [key]: value }));

  return (
    <div className="ag-page">
      <div className="ag-edit-head">
        <button type="button" className="ag-back" onClick={onCancel}>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M15 18l-6-6 6-6" />
          </svg>
          返回目录
        </button>
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

      <div className="ag-edit">
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
        </div>

        <aside className="ag-preview">
          <div className="ag-preview-label">预览</div>
          <div className="cg-preview-box">
            <div className="ag-detail-title">{mod.id || "（id）"}</div>
            <div className="ag-detail-pills">
              <span className="ag-pill src">{mod.kind === "process" ? "模板" : "技能"}</span>
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
  );
}
