// 设置视图（Codex 式）：全屏覆盖 + 左侧分区导航 + 右侧当前分区内容。
// 行组件在 ./rows，导航图标在 ./icons，提供商卡在 ./ProviderBlock，
// 两个二级弹窗在 ./ConnectProviderDialog / ./ModelEditDialog。
import { useEffect, useRef, useState, type ReactElement } from "react";
import { gsap } from "gsap";
import { useSettings, EFFORTS, APPROVALS } from "../../shared/settings";
import type { AgentSource, SessionMeta } from "../../shared/types";
import { motionAllowed, staggerIn, enterEase } from "../../shared/motion";
import { useEscape } from "../../shared/popover";
import { ConnectProviderDialog } from "./ConnectProviderDialog";
import { ModelEditDialog } from "./ModelEditDialog";
import { ProviderBlock } from "./ProviderBlock";
import { ArchivedRow, PlaceholderRow, Section, SegRow, SelectRow, InputRow, ToggleRow, ValueRow } from "./rows";
import { BoxIcon, BranchIcon, ClockIcon, CubeIcon, GearIcon, GitIcon, SunIcon, WinIcon } from "./icons";

type SectionId =
  | "general" | "appearance" | "models" | "personalization"
  | "git" | "environments" | "worktrees" | "archived";

const SECTIONS: { id: SectionId; label: string; icon: ReactElement }[] = [
  { id: "general", label: "通用", icon: <GearIcon /> },
  { id: "appearance", label: "外观", icon: <SunIcon /> },
  { id: "models", label: "模型", icon: <CubeIcon /> },
  { id: "personalization", label: "个性化", icon: <ClockIcon /> },
  { id: "git", label: "Git", icon: <GitIcon /> },
  { id: "environments", label: "环境", icon: <WinIcon /> },
  { id: "worktrees", label: "工作树", icon: <BranchIcon /> },
  { id: "archived", label: "归档任务", icon: <BoxIcon /> },
];

export function SettingsPanel({ open, onClose, source }: { open: boolean; onClose: () => void; source: AgentSource }) {
  const { settings, set, providers, error } = useSettings();
  const [section, setSection] = useState<SectionId>("general");
  const [connectOpen, setConnectOpen] = useState(false);
  const [modelEdit, setModelEdit] = useState<{ providerId: string; modelId: string } | null>(null);
  const contentRef = useRef<HTMLDivElement>(null);

  // 二级弹窗在场时 Escape 归弹窗自己处理
  useEscape(open && !connectOpen && !modelEdit, onClose);

  // 分区切换：内容上浮淡入；模型/归档分区的行列表交错浮现
  useEffect(() => {
    if (!open || !contentRef.current) return;
    const el = contentRef.current;
    if (!motionAllowed()) return;
    gsap.fromTo(el, { opacity: 0, y: 10 }, { opacity: 1, y: 0, duration: 0.28, ease: enterEase, clearProps: "transform,opacity" });
    if (section === "models") {
      staggerIn([...el.querySelectorAll(".mset-provider")], { each: 0.06, delay: 0.06 });
    }
    if (section === "archived") {
      staggerIn([...el.querySelectorAll(".archived-row")], { each: 0.05, delay: 0.06 });
    }
  }, [section, open]);

  // 归档列表：sessions 是活数据，每次渲染现读（memo 会在 unarchive 后失真）
  const archived = source.sessions().filter((s: SessionMeta) => s.archived);

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
              <Section title="通用" desc="模型、推理强度与权限模式随消息发送；输出展示与输入行为即时生效。">
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
                  hint="默认模型（模型管理在「模型」分区）"
                />
                {(() => {
                  const cur = providers.flatMap((p) => p.models).find((m) => m.id === settings.model);
                  const opts = EFFORTS.filter((e) => cur?.efforts.includes(e.id));
                  if (opts.length === 0) return <ValueRow label="推理强度" value="模型默认" hint="当前模型未声明推理能力（在模型配置里勾选「推理」标签开启）" />;
                  return (
                    <SegRow label="推理强度">
                      <div className="panel-seg">
                        {opts.map((e) => (
                          <button key={e.id} type="button" className={"seg-btn" + (settings.effort === e.id ? " on" : "")} onClick={() => set({ effort: e.id })}>
                            {e.label}
                          </button>
                        ))}
                      </div>
                    </SegRow>
                  );
                })()}
                <SegRow label="高危操作">
                  <div className="panel-seg">
                    {APPROVALS.map((a) => (
                      <button key={a.id} type="button" title={a.hint} className={"seg-btn" + (settings.approval === a.id ? " on" : "")} onClick={() => set({ approval: a.id })}>
                        {a.label}
                      </button>
                    ))}
                  </div>
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
              </Section>
            )}

            {section === "appearance" && (
              <Section title="外观" desc="主题与界面字体。字体选择作用于全局，含终端块。">
                <SegRow label="主题">
                  <div className="panel-seg">
                    {([
                      { id: "light", label: "浅色" },
                      { id: "dark", label: "深色" },
                      { id: "system", label: "跟随系统" },
                    ] as const).map((t) => (
                      <button
                        key={t.id}
                        type="button"
                        className={"seg-btn" + (settings.theme === t.id ? " on" : "")}
                        onClick={() => set({ theme: t.id })}
                      >
                        {t.label}
                      </button>
                    ))}
                  </div>
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
                  <button type="button" className="mset-connect-btn" onClick={() => setConnectOpen(true)}>
                    + 连接提供商
                  </button>
                </div>
                {providers.filter((p) => p.connected).map((p) => (
                  <ProviderBlock key={p.id} provider={p} onEditModel={(modelId) => setModelEdit({ providerId: p.id, modelId })} />
                ))}
              </Section>
            )}

            {section === "personalization" && (
              <Section title="个性化" desc="回答的默认语气；自定义指令写入 AGENTS.md。">
                <SegRow label="语气">
                  <div className="panel-seg">
                    {([
                      { id: "friendly", label: "友好" },
                      { id: "pragmatic", label: "务实" },
                      { id: "none", label: "无" },
                    ] as const).map((p) => (
                      <button key={p.id} type="button" className={"seg-btn" + (settings.personality === p.id ? " on" : "")} onClick={() => set({ personality: p.id })}>
                        {p.label}
                      </button>
                    ))}
                  </div>
                </SegRow>
                <ValueRow label="自定义指令" value="AGENTS.md" hint="仓库根目录的守则文件" />
              </Section>
            )}

            {section === "git" && (
              <Section title="Git" desc="分支命名与提交信息生成方式。">
                <InputRow
                  label="默认分支"
                  hint="新任务的初始分支"
                  value={settings.gitBranch}
                  onChange={(v) => set({ gitBranch: v })}
                  placeholder="main"
                  mono
                />
                <ValueRow label="提交信息提示词" value="默认" hint="生成 commit message 的指令" />
              </Section>
            )}

            {section === "environments" && (
              <Section title="环境" desc="任务运行的本地/远程环境。">
                <PlaceholderRow label="本机（默认）" hint="~/gs/lxcode · 直接执行" />
              </Section>
            )}

            {section === "worktrees" && (
              <Section title="工作树" desc="并行任务使用隔离的 Git 工作树。">
                <PlaceholderRow label="未启用" hint="并行任务各自的干净检出" />
              </Section>
            )}

            {section === "archived" && (
              <Section title="归档任务" desc="归档的会话不进侧栏对话列表，可恢复继续。在侧栏会话行的 ⋯ 菜单里归档。">
                {archived.length === 0 && (
                  <div className="sidebar-empty" style={{ padding: "16px 0" }}>没有归档的会话</div>
                )}
                {archived.map((s) => (
                  <ArchivedRow key={s.id} title={s.title} date={s.updatedAt} onRestore={() => source.unarchiveSession(s.id)} />
                ))}
              </Section>
            )}
          </div>
        </div>
      </div>
      {connectOpen && <ConnectProviderDialog onClose={() => setConnectOpen(false)} />}
      {modelEdit &&
        (() => {
          const p = providers.find((x) => x.id === modelEdit.providerId);
          const m = p?.models.find((x) => x.id === modelEdit.modelId);
          return p && m ? <ModelEditDialog provider={p} model={m} onClose={() => setModelEdit(null)} /> : null;
        })()}
    </div>
  );
}
