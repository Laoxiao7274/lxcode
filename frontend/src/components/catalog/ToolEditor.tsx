// 工具编写器弹窗：自定义工具的创建与编辑——表单生成，不让人写 JSON。
// 固定格式 v1 的 JSON 只是分发通道（导入/导出文件）；手工创建走表单。
// 参数面是动态行（名称/类型/必填/说明）；右侧实时预览复用 ToolDocBody。
// 来源不让用户选：来源是系统级事实（执行通道声明——导入按文件声明、
// MCP 由服务器生成、表单创建固定走外部接入通道），选了没有后果的
// 字段是伪配置。
import { useEffect, useRef, useState } from "react";
import { useAgents, type ToolParam, type ToolSpec } from "../../shared/agents";
import { useEscape } from "../../shared/popover";
import { staggerIn } from "../../shared/motion";
import { Button, Segmented, TextInput, Textarea } from "../form";
import { ToolDocBody } from "./DocDialog";

const RISK_OPTS = [
  { value: "low" as const, label: "低危", hint: "自动执行" },
  { value: "high" as const, label: "高危", hint: "确认门" },
];

/** 表单创建的工具固定走外部接入通道（进程边界——后端化时插件机制）。 */
const FORM_SOURCE: ToolSpec["source"] = "binary";

const blankParam = (): ToolParam => ({ name: "", type: "string" });

export function ToolEditor({
  initial,
  isNew,
  onSave,
  onCancel,
}: {
  initial: ToolSpec;
  isNew: boolean;
  onSave: (tool: ToolSpec) => void;
  onCancel: () => void;
}) {
  const [tool, setTool] = useState<ToolSpec>(initial);
  // 参数面独立编辑（行级增删改），保存时清洗合并
  const [params, setParams] = useState<ToolParam[]>(initial.params ?? []);
  const packageRef = useRef<HTMLInputElement>(null);
  const { tools } = useAgents();
  const formRef = useRef<HTMLDivElement>(null);

  useEscape(true, onCancel);

  // 分节交错入场（弹窗每次挂载播一次）
  useEffect(() => {
    const el = formRef.current;
    if (!el) return;
    staggerIn(el.querySelectorAll(".cg-field, .te-param, .te-doc"), { each: 0.04 });
  }, []);

  const id = tool.id.trim();
  const idTaken = id !== "" && tools.some((t) => t.id === id && t.id !== initial.id);
  // 工具要有可执行载体：运行命令必填（程序包可选——不上传则要求命令在 PATH）
  const savable = id !== "" && !idTaken && tool.desc.trim() !== "" && (tool.command ?? "").trim() !== "";

  const set = <K extends keyof ToolSpec>(key: K, value: ToolSpec[K]) =>
    setTool((d) => ({ ...d, [key]: value }));
  const setParam = (i: number, patch: Partial<ToolParam>) =>
    setParams((ps) => ps.map((p, j) => (j === i ? { ...p, ...patch } : p)));
  const removeParam = (i: number) => setParams((ps) => ps.filter((_, j) => j !== i));

  // 预览用清洗后的形态（空名行剔除）
  const cleaned = params.filter((p) => p.name.trim() !== "");
  const draft: ToolSpec = { ...tool, id, params: cleaned.length > 0 ? cleaned : undefined };

  return (
    <div
      className="ag-doc-mask"
      role="dialog"
      aria-modal="true"
      aria-label={isNew ? "新建工具" : `编辑工具 ${tool.id}`}
      onPointerDown={(e) => e.target === e.currentTarget && onCancel()}
    >
      <div className="ag-doc ag-doc-wide">
        <div className="ag-doc-head">
          <span className="ag-doc-title">{isNew ? "新建工具" : `编辑 · ${tool.id}`}</span>
          <div className="ag-detail-pills">
            <span className={"ag-pill " + (tool.risk === "high" ? "risk-high" : "risk-low")}>
              {tool.risk === "high" ? "高危" : "低危"}
            </span>
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
                    id="cg-tool-id"
                    className="cg-id-input"
                    value={tool.id}
                    onChange={(v) => set("id", v)}
                    placeholder="如：deploy-check（拓展内唯一）"
                    aria-label="工具 id"
                  />
                  {idTaken && <div className="ag-warn">id 已存在——拓展条目的 id 必须唯一。</div>}
                </div>
                <div className="cg-field">
                  <span className="cg-field-label">说明</span>
                  <TextInput
                    value={tool.desc}
                    onChange={(v) => set("desc", v)}
                    placeholder="一句话说明（chips 的 tooltip 与卡片副文）"
                    aria-label="说明"
                  />
                </div>
              </section>

              <section className="ag-sec">
                <div className="ag-sec-title">属性</div>
                <div className="cg-field">
                  <span className="cg-field-label">风险</span>
                  <Segmented options={RISK_OPTS} value={tool.risk} onChange={(v) => set("risk", v)} ariaLabel="风险" />
                </div>
              </section>

              <section className="ag-sec">
                <div className="ag-sec-title">执行</div>
                <div className="cg-field">
                  <span className="cg-field-label">运行命令</span>
                  <TextInput
                    className="cg-id-input"
                    value={tool.command ?? ""}
                    onChange={(v) => set("command", v)}
                    placeholder="如：rg {pattern} {path}——参数用 {名称} 占位"
                    aria-label="运行命令"
                  />
                  <div className="cg-field-hint">
                    调用时按参数填充模板后执行（后端化时 spawn 进程）；
                    命令在 PATH 或程序包内。
                  </div>
                </div>
                <div className="cg-field">
                  <span className="cg-field-label">命令示例</span>
                  <TextInput
                    className="cg-id-input"
                    value={tool.example ?? ""}
                    onChange={(v) => set("example", v)}
                    placeholder="如：rg {pattern} --json {path}"
                    aria-label="命令示例"
                  />
                </div>
                <div className="cg-field">
                  <span className="cg-field-label">程序包（可选）</span>
                  <div className="te-package-row">
                    <input
                      ref={packageRef}
                      type="file"
                      accept=".zip,.exe,.tar.gz,.tgz"
                      className="ti-file-hidden"
                      onChange={(e) => {
                        const f = e.target.files?.[0];
                        set("packageFile", f ? f.name : "");
                        e.target.value = "";
                      }}
                    />
                    <Button variant="ghost" data-cg="pick-package" onClick={() => packageRef.current?.click()}>
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                        <path d="M13 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V10Z" />
                        <path d="M13 3v7h7" />
                      </svg>
                      选择文件
                    </Button>
                    {tool.packageFile ? (
                      <span className="te-package-name" title={tool.packageFile}>
                        {tool.packageFile}
                        <button type="button" className="te-package-clear" onClick={() => set("packageFile", "")} aria-label="移除程序包">
                          <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
                            <path d="M18 6 6 18M6 6l12 12" />
                          </svg>
                        </button>
                      </span>
                    ) : (
                      <span className="te-package-hint">zip / exe——不上传则要求命令在 PATH</span>
                    )}
                  </div>
                </div>
              </section>

              <section className="ag-sec">
                <div className="ag-sec-title">
                  参数<span className="ag-count">{cleaned.length}</span>
                </div>
                {params.map((p, i) => (
                  <div className="te-param" key={i}>
                    <div className="te-param-row">
                      <TextInput
                        className="te-param-name"
                        value={p.name}
                        onChange={(v) => setParam(i, { name: v })}
                        placeholder="名称"
                        aria-label={`参数 ${i + 1} 名称`}
                      />
                      <TextInput
                        className="te-param-type"
                        value={p.type}
                        onChange={(v) => setParam(i, { type: v })}
                        placeholder="类型"
                        aria-label={`参数 ${i + 1} 类型`}
                      />
                      <label className="te-param-req" title="调用时必填">
                        <input
                          type="checkbox"
                          checked={p.required === true}
                          onChange={(e) => setParam(i, { required: e.target.checked })}
                        />
                        必填
                      </label>
                      <button type="button" className="te-param-del" onClick={() => removeParam(i)} aria-label="移除参数">
                        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden="true">
                          <path d="M18 6 6 18M6 6l12 12" />
                        </svg>
                      </button>
                    </div>
                    <TextInput
                      className="te-param-desc"
                      value={p.desc ?? ""}
                      onChange={(v) => setParam(i, { desc: v })}
                      placeholder="说明（可选）"
                      aria-label={`参数 ${i + 1} 说明`}
                    />
                  </div>
                ))}
                <button type="button" className="te-param-add" onClick={() => setParams((ps) => [...ps, blankParam()])}>
                  + 添加参数
                </button>
              </section>

              <section className="ag-sec">
                <div className="ag-sec-title">文档（markdown）</div>
                <Textarea
                  className="te-doc"
                  value={tool.doc ?? ""}
                  onChange={(v) => set("doc", v)}
                  placeholder="扩展文档——详情层的完整说明（可选）"
                  ariaLabel="工具文档"
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
                  onClick={() =>
                    onSave({
                      ...tool,
                      id,
                      desc: tool.desc.trim(),
                      custom: true,
                      source: isNew ? FORM_SOURCE : tool.source,
                      command: (tool.command ?? "").trim(),
                      example: (tool.example ?? "").trim(),
                      ...(cleaned.length > 0 ? { params: cleaned } : {}),
                    })
                  }
                >
                  {isNew ? "保存并添加" : "保存"}
                </Button>
              </div>
            </div>

            <aside className="ag-preview">
              <div className="ag-preview-label">预览</div>
              <div className="cg-preview-box">
                <div className="ag-detail-title">{tool.id || "（id）"}</div>
                <div className="ag-detail-pills">
                  <span className={"ag-pill " + (tool.risk === "high" ? "risk-high" : "risk-low")}>
                    {tool.risk === "high" ? "高危" : "低危"}
                  </span>
                  <span className="ag-pill src">
                    {tool.source === "builtin" ? "内置" : tool.source === "binary" ? "外部二进制" : "MCP"}
                  </span>
                  <span className="ag-pill src">自定义</span>
                </div>
                <ToolDocBody tool={draft} />
              </div>
              <div className="ag-preview-hint">保存后进入拓展；Agent 组装的 chips 里即时可选。</div>
            </aside>
          </div>
        </div>
      </div>
    </div>
  );
}
