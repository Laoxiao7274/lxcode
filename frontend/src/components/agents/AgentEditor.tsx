// 组装编辑器：紧凑分节表单（身份/模型/工具/上下文/委派/权限）+ 右侧
// 「预览卡 + 紧凑详情面板」。chip 点击 = 选中 + 焦点（面板跟随）；
// 点面板整卡打开完整文档弹窗（DocDialog 复用组件）。表单控件一律用
// components/form 套件；gsap 分节交错入场（motionAllowed 门控）。
// 紧凑详情面板在 ./DetailPanel（只读展示组件）。
import { useEffect, useRef, useState } from "react";
import {
  AGENT_COLORS,
  MAIN_TOOL,
  useAgents,
  useModelLabel,
  type AgentDef,
} from "../../shared/agents";
import { useSettings } from "../../shared/settings";
import { defaultProtocol } from "../../shared/agent-protocol";
import { Markdown } from "../../shared/markdown";
import { useEnterRef } from "../../shared/anim";
import { staggerIn } from "../../shared/motion";
import { Button, Chips, ColorPicker, Select, Segmented, Textarea, TextInput, type SelectGroup } from "../form";
import { DocDialog, type Focus } from "../catalog/DocDialog";
import { AgentCard } from "./AgentCard";
import { DetailPanel } from "./DetailPanel";

const APPROVAL_OPTS = [
  { value: "confirm" as const, label: "确认", hint: "低危自动执行，高危弹确认门（默认）" },
  { value: "auto" as const, label: "自动", hint: "高危也自动执行——仅隔离环境使用" },
  { value: "strict" as const, label: "只读", hint: "变更类工具直接拒绝，错误回填模型" },
];

export function AgentEditor({
  initial,
  isNew,
  onSave,
  onCancel,
}: {
  initial: AgentDef;
  isNew: boolean;
  onSave: (def: AgentDef) => void;
  onCancel: () => void;
}) {
  const [def, setDef] = useState<AgentDef>(initial);
  const [focus, setFocus] = useState<Focus | null>(null);
  const [docOpen, setDocOpen] = useState(false);
  const { agents, modules, tools } = useAgents();
  const { providers } = useSettings();
  const modelLabel = useModelLabel(def.model);
  const formRef = useRef<HTMLDivElement>(null);

  // 分节交错入场（编辑器每次挂载播一次——取消/保存返回再进会重播）
  useEffect(() => {
    const el = formRef.current;
    if (!el) return;
    staggerIn(el.querySelectorAll(".ag-sec"), { each: 0.03 });
  }, []);

  const set = <K extends keyof AgentDef>(key: K, value: AgentDef[K]) =>
    setDef((d) => ({ ...d, [key]: value }));

  // 模型选项与输入区 ModelPicker 同源：已连接且启用提供商下的可见模型
  const modelGroups = providers
    .filter((p) => p.connected && p.enabled)
    .map((p) => ({ name: p.name, models: p.models.filter((m) => m.visible) }))
    .filter((g) => g.models.length > 0);
  const modelInList = modelGroups.some((g) => g.models.some((m) => m.id === def.model));

  // Select 的分组载荷：提供商名做组标题；当前绑定不在列表时置顶一组保留它
  const selectGroups: SelectGroup[] = modelGroups.map((g) => ({
    group: g.name,
    options: g.models.map((m) => ({ value: m.id, label: m.name, desc: m.desc })),
  }));
  if (!modelInList && def.model) {
    selectGroups.unshift({
      group: "当前绑定",
      options: [{ value: def.model, label: modelLabel, desc: "不在可用列表——可改选" }],
    });
  }

  // Chips 的载荷（工具/上下文模块——名称在面上，说明进 tooltip）；
  // 工具按来源分组（内置 / 第三方 / MCP / 自定义）——MCP 是独立接入通道，
  // 与外部二进制分开展示；组内操作同一个白名单（def.tools）
  const toToolChip = (t: { id: string; desc: string; risk: "low" | "high" }) => ({
    value: t.id,
    label: t.id,
    desc: `${t.desc}（${t.risk === "high" ? "高危" : "低危"}）`,
    highRisk: t.risk === "high",
  });
  const builtinToolChips = tools.filter((t) => !t.custom && t.source === "builtin").map(toToolChip);
  const binaryToolChips = tools.filter((t) => !t.custom && t.source === "binary").map(toToolChip);
  const mcpToolChips = tools.filter((t) => !t.custom && t.source === "mcp").map(toToolChip);
  const customToolChips = tools.filter((t) => t.custom).map(toToolChip);
  // 上下文模块 chips 载荷（拓展状态——自建条目即时出现在这里）
  const toModuleChip = (m: { id: string; desc: string; kind: "process" | "skill" }) => ({
    value: m.id,
    label: m.id,
    desc: `${m.desc}（${m.kind === "process" ? "模板" : "技能"}）`,
  });
  const processChips = modules.filter((m) => m.kind === "process").map(toModuleChip);
  const skillChips = modules.filter((m) => m.kind === "skill").map(toModuleChip);

  // 委派名单载荷（仅主 Agent 用）：全部子 Agent，停用的在 tooltip 标注
  const subAgents = agents.filter((a) => !a.isMain);
  const subAgentChips = subAgents.map((a) => ({
    value: a.id,
    label: a.name,
    desc: a.enabled ? a.desc : `${a.desc}（已停用）`,
    dot: a.color,
  }));

  const savable = def.name.trim().length > 0;

  return (
    <div className="ag-page">
      <div className="ag-edit-head">
        <button type="button" className="ag-back" onClick={onCancel}>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M15 18l-6-6 6-6" />
          </svg>
          返回名单
        </button>
        <div className="ag-edit-actions">
          <Button variant="ghost" data-ag="cancel" onClick={onCancel}>
            取消
          </Button>
          <Button
            variant="primary"
            disabled={!savable}
            onClick={() =>
              onSave({
                ...def,
                name: def.name.trim(),
                desc: def.desc.trim(),
                // 协议与内置默认相同（或被清空）→ 不存拷贝，运行时用默认
                protocol:
                  def.protocol !== undefined && def.protocol.trim() !== "" && def.protocol.trim() !== defaultProtocol(def.isMain === true).trim()
                    ? def.protocol.trim()
                    : undefined,
              })
            }
          >
            {isNew ? "保存并注册" : "保存"}
          </Button>
        </div>
      </div>

      <div className="ag-edit">
        <div className="ag-form" ref={formRef}>
          <section className="ag-sec">
            <div className="ag-sec-title">身份</div>
            <div className="ag-id-row">
              <TextInput
                id="ag-name"
                className="ag-name-input"
                value={def.name}
                onChange={(v) => set("name", v)}
                placeholder="名称（如：代码 Agent）"
              />
              {!def.isMain && (
                <ColorPicker colors={AGENT_COLORS} value={def.color} onChange={(c) => set("color", c)} ariaLabel="标识色" />
              )}
            </div>
            <TextInput
              className="ag-desc-input"
              value={def.desc}
              onChange={(v) => set("desc", v)}
              placeholder="职责描述——这个 Agent 擅长什么、边界在哪"
            />
          </section>

          <section className="ag-sec">
            <div className="ag-sec-title">模型</div>
            <Select
              value={def.model}
              onChange={(v) => set("model", v)}
              groups={selectGroups}
              placeholder="选择模型"
              ariaLabel="选择模型"
            />
            {!modelInList && def.model && (
              <div className="ag-warn">当前绑定「{modelLabel}」不在可用列表——可改选。</div>
            )}
          </section>

          <section className="ag-sec">
            <div className="ag-sec-title">
              工具<span className="ag-count">{def.tools.length}</span>
            </div>
            {def.isMain ? (
              <div className="ag-locked">
                <span className="ag-lock-name">{MAIN_TOOL.id}</span>
                <span className="ag-lock-desc">{MAIN_TOOL.desc}——主 Agent 不直接执行任务。</span>
              </div>
            ) : (
              <>
                <Chips
                  options={builtinToolChips}
                  value={def.tools}
                  onChange={(tools) => set("tools", tools)}
                  focusedValue={focus?.kind === "tool" ? focus.id : null}
                  onFocus={(id) => setFocus({ kind: "tool", id })}
                  ariaLabel="内置工具"
                />
                {binaryToolChips.length > 0 && (
                  <>
                    <div className="ag-chip-label">第三方 / 插件</div>
                    <Chips
                      options={binaryToolChips}
                      value={def.tools}
                      onChange={(tools) => set("tools", tools)}
                      focusedValue={focus?.kind === "tool" ? focus.id : null}
                      onFocus={(id) => setFocus({ kind: "tool", id })}
                      ariaLabel="第三方工具"
                    />
                  </>
                )}
                {mcpToolChips.length > 0 && (
                  <>
                    <div className="ag-chip-label">MCP</div>
                    <Chips
                      options={mcpToolChips}
                      value={def.tools}
                      onChange={(tools) => set("tools", tools)}
                      focusedValue={focus?.kind === "tool" ? focus.id : null}
                      onFocus={(id) => setFocus({ kind: "tool", id })}
                      ariaLabel="MCP 工具"
                    />
                  </>
                )}
                {customToolChips.length > 0 && (
                  <>
                    <div className="ag-chip-label">自定义</div>
                    <Chips
                      options={customToolChips}
                      value={def.tools}
                      onChange={(tools) => set("tools", tools)}
                      focusedValue={focus?.kind === "tool" ? focus.id : null}
                      onFocus={(id) => setFocus({ kind: "tool", id })}
                      ariaLabel="自定义工具"
                    />
                  </>
                )}
              </>
            )}
          </section>

          <section className="ag-sec">
            <div className="ag-sec-title">
              上下文<span className="ag-count">{(def.workflow ? 1 : 0) + def.skills.length}</span>
            </div>
            <div className="ag-chip-label">
              协议{def.protocol ? " · 已定制" : ""}
              {def.protocol !== undefined && def.protocol.trim() !== "" && (
                <button
                  type="button"
                  className="ag-protocol-reset"
                  title="清掉定制，回到内置默认协议"
                  onClick={() => set("protocol", undefined)}
                >
                  恢复默认
                </button>
              )}
            </div>
            <Textarea
              className="ag-protocol-input"
              value={def.protocol ?? defaultProtocol(def.isMain === true)}
              onChange={(v) => set("protocol", v)}
              placeholder="调度/执行的底层规则……"
              ariaLabel="协议"
            />
            <div className="ag-hint">
              协议是上下文的第一层（拼在模板、技能与自定义段之前）——预填内置默认，可整段定制；
              改动前先看清默认规则在承担什么。
            </div>
            <div className="ag-chip-label">模板</div>
            <Chips
              exclusive
              options={processChips}
              value={def.workflow ? [def.workflow] : []}
              onChange={(v) => set("workflow", v[0] ?? "")}
              focusedValue={focus?.kind === "module" ? focus.id : null}
              onFocus={(id) => setFocus({ kind: "module", id })}
              ariaLabel="模板（单选）"
            />
            <div className="ag-hint">单选——工作方式是完整单元；缺合适的模板就补一个，不靠多个拼装。</div>
            <div className="ag-chip-label">技能</div>
            <Chips
              options={skillChips}
              value={def.skills}
              onChange={(skills) => set("skills", skills)}
              focusedValue={focus?.kind === "module" ? focus.id : null}
              onFocus={(id) => setFocus({ kind: "module", id })}
              ariaLabel="技能"
            />
            <div className="ag-chip-label">自定义段</div>
            <Textarea
              value={def.prompt}
              onChange={(v) => set("prompt", v)}
              placeholder="这个 Agent 私有的补充约定——特殊流程、边界、口吻（拼在协议与模块之后）"
              ariaLabel="自定义上下文"
            />
          </section>

          <section className="ag-sec">
            <div className="ag-sec-title">
              委派<span className="ag-count">{def.isMain ? def.delegates.length : 0}</span>
            </div>
            {def.isMain ? (
              subAgents.length === 0 ? (
                <div className="ag-empty-inline">名单里还没有子 Agent——先组装一个</div>
              ) : (
                <>
                  <Chips
                    options={subAgentChips}
                    value={def.delegates}
                    onChange={(delegates) => set("delegates", delegates)}
                    ariaLabel="默认委派名单"
                  />
                  <div className="ag-hint">
                    默认可分派给勾选的子 Agent（停用的不参与）。会话里可临时收窄——输入区 Agent 菜单的「可委派」。
                  </div>
                </>
              )
            ) : (
              <div className="ag-locked">
                <span className="ag-lock-name">不可委派</span>
                <span className="ag-lock-desc">子 Agent 是纯执行者——调度权只在主 Agent，委派深度恒为一级。</span>
              </div>
            )}
          </section>

          <section className="ag-sec">
            <div className="ag-sec-title">权限默认</div>
            <Segmented
              options={APPROVAL_OPTS}
              value={def.approval}
              onChange={(v) => set("approval", v)}
              ariaLabel="权限默认"
            />
          </section>
        </div>

        <aside className="ag-preview">
          <div className="ag-preview-label">预览</div>
          <AgentCard agent={def} preview />
          <DetailPanel focus={focus} onOpen={() => setDocOpen(true)} />
          <div className="ag-preview-prompt">{def.prompt || "（自定义上下文预览——左侧填写后展示）"}</div>
          <div className="ag-preview-hint">保存后进入名单；输入区可选择它干活。</div>
        </aside>
      </div>

      {focus && docOpen && <DocDialog focus={focus} onClose={() => setDocOpen(false)} />}
    </div>
  );
}
