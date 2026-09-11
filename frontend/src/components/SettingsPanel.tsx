// 设置视图（Codex 式）：全屏覆盖 + 左侧分区导航（9 分区，Back to app
// 返回条目）+ 右侧当前分区内容。截图证据：openai/codex issue #17596。
import { useEffect, useState, type ReactElement, type ReactNode } from "react";
import { useSettings, EFFORTS, APPROVALS, MODELS, type Settings } from "../settings";

type SectionId =
  | "general" | "appearance" | "configuration" | "personalization"
  | "mcp" | "git" | "environments" | "worktrees" | "archived";

const SECTIONS: { id: SectionId; label: string; icon: ReactElement }[] = [
  { id: "general", label: "通用", icon: <GearIcon /> },
  { id: "appearance", label: "外观", icon: <SunIcon /> },
  { id: "configuration", label: "配置", icon: <ShieldIcon /> },
  { id: "personalization", label: "个性化", icon: <ClockIcon /> },
  { id: "mcp", label: "MCP 服务器", icon: <LinkIcon /> },
  { id: "git", label: "Git", icon: <GitIcon /> },
  { id: "environments", label: "环境", icon: <WinIcon /> },
  { id: "worktrees", label: "工作树", icon: <BranchIcon /> },
  { id: "archived", label: "归档任务", icon: <BoxIcon /> },
];

export function SettingsPanel({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { settings, set } = useSettings();
  const [section, setSection] = useState<SectionId>("general");

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

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
        <div className="settings-content-inner">
          {section === "general" && (
            <Section title="通用" desc="命令输出在对话里的展示量与生成时的电源行为。">
              <ToggleRow
                label="命令输出完整展示"
                hint="关闭时工具结果默认折叠为摘要行"
                checked={settings.showFullOutput}
                onChange={(v) => set({ showFullOutput: v })}
              />
              <ToggleRow
                label="生成时阻止休眠"
                hint="有任务运行时保持屏幕常亮"
                checked={settings.keepAwake}
                onChange={(v) => set({ keepAwake: v })}
              />
              <ToggleRow
                label="Enter 发送（关闭则 Cmd+Enter 多行）"
                hint="单行输入模式"
                checked={settings.enterToSend}
                onChange={(v) => set({ enterToSend: v })}
              />
            </Section>
          )}

          {section === "appearance" && (
            <Section title="外观" desc="主题与界面字体。字体选择作用于全局，含终端块。">
              <ValueRow label="主题" value="浅色" hint="跟随系统 / 浅色 / 深色" />
              <ValueRow label="界面字体" value="Inter" hint="13px" />
              <ValueRow label="代码字体" value="JetBrains Mono" hint="11px" />
            </Section>
          )}

          {section === "configuration" && (
            <Section title="配置" desc="模型、推理强度与高危操作确认模式。高级选项编辑 config.toml。">
              <ValueRow label="模型" value={settings.model} hint={MODELS.find((m) => m.id === settings.model)?.desc} />
              <SegRow label="推理强度">
                <div className="panel-seg">
                  {EFFORTS.map((e) => (
                    <button key={e.id} type="button" className={"seg-btn" + (settings.effort === e.id ? " on" : "")} onClick={() => set({ effort: e.id as Settings["effort"] })}>
                      {e.label}
                    </button>
                  ))}
                </div>
              </SegRow>
              <ValueRow label="高危操作" value={APPROVALS.find((a) => a.id === settings.approval)?.label ?? ""} hint={APPROVALS.find((a) => a.id === settings.approval)?.hint} />
              <ValueRow label="配置文件" value="config.toml" hint="高级选项（超时/上下文上限等）" />
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

          {section === "mcp" && (
            <Section title="MCP 服务器" desc="经 Model Context Protocol 接入外部工具。配置对 CLI 与 IDE 扩展同样生效。">
              <PlaceholderRow label="尚无已连接的服务器" hint="接入后在此管理（推荐服务器 / OAuth 授权）" />
            </Section>
          )}

          {section === "git" && (
            <Section title="Git" desc="分支命名与提交信息生成方式。">
              <ValueRow label="默认分支" value="main" hint="新任务的初始分支" />
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
            <Section title="归档任务" desc="归档的会话，可恢复继续。">
              <ArchivedRow title="前后台分离的协议层评审" date="昨天" />
              <ArchivedRow title="选型：Tauri 壳的边界" date="上周" />
            </Section>
          )}
        </div>
      </div>
      </div>
    </div>
  );
}

/* ---------- 行组件 ---------- */

function Section({ title, desc, children }: { title: string; desc: string; children: ReactNode }) {
  return (
    <section>
      <h3 className="set-sec-title">{title}</h3>
      <p className="set-sec-desc">{desc}</p>
      <div className="set-sec-body">{children}</div>
    </section>
  );
}

function ToggleRow({ label, hint, checked, onChange }: { label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="set-row">
      <div className="set-row-text">
        <div className="set-row-label">{label}</div>
        {hint && <div className="set-row-hint">{hint}</div>}
      </div>
      <span
        className={"toggle" + (checked ? " on" : "")}
        role="switch"
        aria-checked={checked}
        tabIndex={0}
        onClick={() => onChange(!checked)}
        onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onChange(!checked); } }}
      >
        <span className="toggle-knob" />
      </span>
    </div>
  );
}

function ValueRow({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="set-row">
      <div className="set-row-text">
        <div className="set-row-label">{label}</div>
        {hint && <div className="set-row-hint">{hint}</div>}
      </div>
      <span className="set-row-value">{value}</span>
    </div>
  );
}

function SegRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="set-row">
      <div className="set-row-text">
        <div className="set-row-label">{label}</div>
      </div>
      {children}
    </div>
  );
}

function PlaceholderRow({ label, hint }: { label: string; hint?: string }) {
  return (
    <div className="set-row">
      <div className="set-row-text">
        <div className="set-row-label">{label}</div>
        {hint && <div className="set-row-hint">{hint}</div>}
      </div>
    </div>
  );
}

function ArchivedRow({ title, date }: { title: string; date: string }) {
  return (
    <div className="archived-row">
      <span className="archived-title">{title}</span>
      <span className="archived-date">{date}</span>
      <button type="button" className="archived-restore">恢复</button>
    </div>
  );
}

/* ---------- 导航图标（内联 SVG，1.6 stroke） ---------- */

function GearIcon() {
  return <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.01a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h.01a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.01a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/></svg>;
}
function SunIcon() {
  return <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/></svg>;
}
function ShieldIcon() {
  return <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/><path d="m9 12 2 2 4-4"/></svg>;
}
function ClockIcon() {
  return <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/></svg>;
}
function LinkIcon() {
  return <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M9 17H7A5 5 0 0 1 7 7h2"/><path d="M15 7h2a5 5 0 1 1 0 10h-2"/><path d="M8 12h8"/></svg>;
}
function GitIcon() {
  return <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><line x1="6" x2="6" y1="3" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>;
}
function WinIcon() {
  return <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="14" rx="2"/><path d="M7 14h4"/></svg>;
}
function BranchIcon() {
  return <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M6 3v12"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>;
}
function BoxIcon() {
  return <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M21 8v13H3V8"/><path d="M1 3h22v5H1z"/><path d="M10 12h4"/></svg>;
}
