// 设置视图（Codex 式）：全屏覆盖 + 左侧分区导航 + 右侧当前分区内容。
// 行组件在 ./rows，导航图标在 ./icons，提供商卡在 ./ProviderBlock，
// 两个二级弹窗在 ./ConnectProviderDialog / ./ModelEditDialog。
import { useEffect, useRef, useState, type ReactElement } from "react";
import { gsap } from "gsap";
import { useSettings, EFFORTS, APPROVALS } from "../../shared/settings";
import { approvalPick } from "../../shared/approval";
import { Button, Segmented } from "../form";
import type { AgentSource, ApprovalMode, SessionMeta, TodoPromptMode } from "../../shared/types";
import { motionAllowed, staggerIn, enterEase } from "../../shared/motion";
import { useEscape } from "../../shared/popover";
import { ApprovalConfirmDialog } from "../perm-picker/ApprovalConfirmDialog";
import { ConnectProviderDialog } from "./ConnectProviderDialog";
import { ModelEditDialog } from "./ModelEditDialog";
import { ProviderBlock } from "./ProviderBlock";
import { SearchSection } from "./SearchSection";
import { UpdateBlock } from "./UpdateBlock";
import { Section, SegRow, SelectRow, ToggleRow, ValueRow } from "./rows";
import { CubeIcon, GearIcon, SearchIcon, SunIcon } from "./icons";

type SectionId = "general" | "appearance" | "models" | "search";

const SECTIONS: { id: SectionId; label: string; icon: ReactElement }[] = [
  { id: "general", label: "通用", icon: <GearIcon /> },
  { id: "appearance", label: "外观", icon: <SunIcon /> },
  { id: "models", label: "模型", icon: <CubeIcon /> },
  { id: "search", label: "网页搜索", icon: <SearchIcon /> },
];

/** 任务清单模板三档（后端 behavior.json 的 todoPrompt——内置模板，不靠模型
 *  自觉判断任务简不简单）。hint 是各档的一句话含义（选中后显示在分段下方）。 */
const TODO_PROMPT_MODES: { value: TodoPromptMode; label: string; hint: string }[] = [
  { value: "strict", label: "严格", hint: "需要工具的任务必须先建 3~7 条清单，两三步也要建，边做边更新" },
  { value: "normal", label: "常规", hint: "按任务复杂度建清单，一两步的小任务直接做完汇报" },
  { value: "off", label: "关闭", hint: "不注入清单指令，模型自行决定是否使用 todo" },
];

export function SettingsPanel({ open, onClose, source }: { open: boolean; onClose: () => void; source: AgentSource }) {
  const { settings, set, providers, error } = useSettings();
  const [section, setSection] = useState<SectionId>("general");
  const [connectOpen, setConnectOpen] = useState(false);
  const [modelEdit, setModelEdit] = useState<{ providerId: string; modelId: string } | null>(null);
  // 任务清单模板（todo-first 档位）：live 模式后端是唯一事实源（get/changed
  // 驱动 + set 写回）；demo（source 没给 todoPromptAdmin）用本地状态自管。
  const todoAdmin = source.todoPromptAdmin;
  const [todoMode, setTodoMode] = useState<TodoPromptMode>(() => todoAdmin?.mode() ?? "strict");
  useEffect(() => {
    if (!todoAdmin) return;
    setTodoMode(todoAdmin.mode());
    return todoAdmin.onChange(() => setTodoMode(todoAdmin.mode()));
  }, [todoAdmin]);
  const setTodoPrompt = (mode: TodoPromptMode) => {
    if (todoAdmin) void todoAdmin.set(mode);
    else setTodoMode(mode);
  };
  // 新会话默认审批档（behavior.json 的 approval——后端级行为开关）：live 模式
  // 后端是唯一事实源（get 初始化 + settings.defaultApproval.changed 广播对齐 +
  // set 写回落盘）；demo 源同名方法本地自管，形状一致（无后端也能验 UI）。
  const [defApproval, setDefApproval] = useState<ApprovalMode>("confirm");
  useEffect(() => {
    let alive = true;
    source.defaultApproval().then((m) => { if (alive) setDefApproval(m); }).catch(() => { /* 读不到保持缺省 */ });
    const off = source.subscribe((ev) => {
      if (ev.type === "defaultApprovalChanged") setDefApproval(ev.approval);
    });
    return () => { alive = false; off(); };
  }, [source]);
  const setDefaultApproval = (mode: ApprovalMode) => {
    void source.setDefaultApproval(mode).catch(() => { /* 失败静默：下一次 get 会对齐 */ });
  };
  // 默认档切「完全访问」的确认弹窗（与输入区 PermPicker 同一条规则、同一个
  // 弹窗组件——默认档放开的是**以后所有新会话**的执行面，更不能一下就关掉）
  const [confirmAutoOpen, setConfirmAutoOpen] = useState(false);
  const contentRef = useRef<HTMLDivElement>(null);

  // 二级弹窗在场时 Escape 归弹窗自己处理
  useEscape(open && !connectOpen && !modelEdit && !confirmAutoOpen, onClose);

  // 分区切换：内容上浮淡入；模型/搜索分区的行列表交错浮现
  useEffect(() => {
    if (!open || !contentRef.current) return;
    const el = contentRef.current;
    if (!motionAllowed()) return;
    gsap.fromTo(el, { opacity: 0, y: 10 }, { opacity: 1, y: 0, duration: 0.28, ease: enterEase, clearProps: "transform,opacity" });
    if (section === "models") {
      staggerIn([...el.querySelectorAll(".mset-provider")], { each: 0.06, delay: 0.06 });
    }
    if (section === "search") {
      staggerIn([...el.querySelectorAll(".sp-card")], { each: 0.05, delay: 0.06 });
    }
  }, [section, open]);

  if (!open) return null;
  return (
    // 遮罩层只挡点击（不关闭——误触退出很伤体验；退出走 Escape/返回应用）
    <div className="settings-view" role="dialog" aria-label="设置" aria-modal="true">
      <div className="settings-dialog">
        {/* 左侧分区导航 */}
        <nav className="settings-nav">
          <button type="button" className="settings-back" onClick={onClose}>
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M19 12H5" />
              <path d="m12 19-7-7 7-7" />
            </svg>
            返回应用
          </button>
          {SECTIONS.map((s) => (
            <button
              key={s.id}
              type="button"
              className={"settings-nav-item" + (section === s.id ? " active" : "")}
              onClick={() => setSection(s.id)}
            >
              <span className="nav-icon">{s.icon}</span>
              {s.label}
            </button>
          ))}
        </nav>

        {/* 右侧内容区 */}
        <div className="settings-content">
          <div className="settings-content-inner" ref={contentRef}>
            {section === "general" && (
              <Section title="通用" desc="模型与推理强度随消息发送；权限档是会话级状态（当前会话在输入区权限选择器里改，这里只管新会话的默认档）。">
                <SelectRow
                  label="模型"
                  value={settings.model}
                  onChange={(v) => set({ model: v })}
                  options={(() => {
                    const opts = providers
                      .flatMap((p) => p.models.filter((m) => m.visible))
                      .map((m) => ({ value: m.id, label: m.name }));
                    if (settings.model && !opts.some((o) => o.value === settings.model)) {
                      opts.unshift({ value: settings.model, label: settings.model });
                    }
                    return opts;
                  })()}
                  hint="新会话的默认模型（当前会话的模型在输入区的模型选择器里改；模型管理在「模型」分区）"
                />
                {(() => {
                  const cur = providers.flatMap((p) => p.models).find((m) => m.id === settings.model);
                  const opts = EFFORTS.filter((e) => cur?.efforts.includes(e.id));
                  if (opts.length === 0) return <ValueRow label="推理强度" value="模型默认" hint="当前模型未声明推理能力（在模型配置里勾选「推理」标签开启）" />;
                  return (
                    <SegRow label="推理强度">
                      <Segmented options={opts.map((e) => ({ value: e.id, label: e.label, hint: e.hint }))} value={settings.effort} onChange={(v) => set({ effort: v })} ariaLabel="推理强度" />
                    </SegRow>
                  );
                })()}
                <SegRow label="新会话默认审批档">
                  {/* 改的是**新会话**的初始档（后端 behavior.json，set 写盘并广播，
                      立即生效于下一次建会话）——不是当前会话的档位；已有会话各自
                      持有落库的档，在输入区权限选择器里改。切「完全访问」先弹确认
                      弹窗（approvalPick 分流，收权方向不拦）。 */}
                  <Segmented
                    options={APPROVALS.map((a) => ({ value: a.id, label: a.label, hint: a.hint }))}
                    value={defApproval}
                    onChange={(mode) => approvalPick(defApproval, mode, setDefaultApproval, () => setConfirmAutoOpen(true))}
                    ariaLabel="新会话默认审批档"
                  />
                  <div className="set-row-hint">只影响新建的会话；已有会话保持自己的档位（当前会话在输入区权限选择器里改）。</div>
                </SegRow>
                <SegRow label="任务清单">
                  {/* 动手前建清单的内置模板（后端级行为开关，主/子 Agent 同源）：
                      改档即写盘并广播，**下一轮**对话生效（提示词每轮现组装）。 */}
                  <Segmented
                    options={TODO_PROMPT_MODES.map((m) => ({ value: m.value, label: m.label, hint: m.hint }))}
                    value={todoMode}
                    onChange={setTodoPrompt}
                    ariaLabel="任务清单"
                  />
                  <div className="set-row-hint">{TODO_PROMPT_MODES.find((m) => m.value === todoMode)?.hint}</div>
                </SegRow>
                <ToggleRow
                  label="命令输出完整展示"
                  hint="关闭时工具结果默认折叠为摘要行"
                  checked={settings.showFullOutput}
                  onChange={(v) => set({ showFullOutput: v })}
                />
                <ToggleRow
                  label="生成时阻止休眠"
                  hint="生成期间保持唤醒（壳接管后完全生效）"
                  checked={settings.keepAwake}
                  onChange={(v) => set({ keepAwake: v })}
                />
                <ToggleRow
                  label="Enter 发送（关闭则 Cmd+Enter 多行）"
                  hint="单行输入模式"
                  checked={settings.enterToSend}
                  onChange={(v) => set({ enterToSend: v })}
                />
                <ValueRow label="配置文件" value="models.json" hint="路径由后端启动参数决定" />
                <UpdateBlock />
              </Section>
            )}

            {section === "appearance" && (
              <Section title="外观" desc="主题与界面字体。字体选择作用于全局，含终端块。">
                <SegRow label="主题">
                  <Segmented
                    options={[
                      { value: "light", label: "浅色" },
                      { value: "dark", label: "深色" },
                      { value: "system", label: "跟随系统" },
                    ]}
                    value={settings.theme}
                    onChange={(v) => set({ theme: v })}
                    ariaLabel="主题"
                  />
                </SegRow>
                <ValueRow label="界面字体" value="Inter" hint="13px" />
                <ValueRow label="代码字体" value="JetBrains Mono" hint="11px" />
              </Section>
            )}

            {section === "models" && (
              <Section title="模型与提供商" desc="连接提供商、管理模型可见性与默认模型。模型选择器只显示启用的模型。">
                {error && <div className="error-block" role="alert">{error}</div>}
                <div className="mset-toolbar">
                  <span className="mset-count">{providers.filter((p) => p.connected).length} 个已连接</span>
                  <Button variant="primary" data-cg="connect-provider" className="mset-connect-btn" onClick={() => setConnectOpen(true)}>
                    + 连接提供商
                  </Button>
                </div>
                {providers.filter((p) => p.connected).map((p) => (
                  <ProviderBlock key={p.id} provider={p} onEditModel={(modelId) => setModelEdit({ providerId: p.id, modelId })} />
                ))}
              </Section>
            )}

            {section === "search" && <SearchSection />}
          </div>
        </div>
      </div>
      {connectOpen && <ConnectProviderDialog onClose={() => setConnectOpen(false)} />}
      {confirmAutoOpen && (
        <ApprovalConfirmDialog
          onConfirm={() => { setConfirmAutoOpen(false); setDefaultApproval("auto"); }}
          onCancel={() => setConfirmAutoOpen(false)}
        />
      )}
      {modelEdit &&
        (() => {
          const p = providers.find((x) => x.id === modelEdit.providerId);
          const m = p?.models.find((x) => x.id === modelEdit.modelId);
          return p && m ? <ModelEditDialog provider={p} model={m} onClose={() => setModelEdit(null)} /> : null;
        })()}
    </div>
  );
}
