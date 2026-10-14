// 软件更新块（设置 · 通用分区底部）：版本号 + 检查/下载/安装流。
// 状态来自 shared/update.tsx 的 UpdateProvider——与页面级 UpdateToast
// 共用（自动检查发现新版本时 Toast 先弹，这里可看详情与继续操作）。
//
// 视觉契约（2026-12 样式重做）：六种状态（idle/checking/latest/available/
// downloading/ready）+ error 各有明确的视觉形态，不共用模糊样式；「检查更新」
// 按钮在**所有状态下可见可点**（检查中禁用防连点，检查完恢复）——它消失过
// 一次（旧版只在 idle 渲染），测试钉住这条。呈现拆成纯展示的
// UpdateBlockView（可静态渲染钉断言）+ useUpdate 连接器。
import { useState, type ReactNode } from "react";
import { Button } from "../form";
import { humanBytes } from "../../shared/connections";
import { relTime, useUpdate } from "../../shared/update";

/** 状态徽记：每种 phase 一个图标 + 色（纯展示；点不出交互）。 */
function PhaseMark({ phase }: { phase: string }) {
  if (phase === "checking" || phase === "downloading") {
    return <span className="set-update-mark spin" aria-hidden="true" />;
  }
  if (phase === "latest") {
    return (
      <span className="set-update-mark ok" aria-hidden="true">
        <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
      </span>
    );
  }
  if (phase === "available") {
    return <span className="set-update-mark info" aria-hidden="true" />;
  }
  if (phase === "ready") {
    return (
      <span className="set-update-mark ok" aria-hidden="true">
        <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
      </span>
    );
  }
  return <span className="set-update-mark" aria-hidden="true" />;
}

/** 状态行：mark + 一句话。tone 决定字色（中性/ok/info/warn）。 */
function StatusLine({ tone, children }: { tone: "neutral" | "ok" | "info" | "warn"; children: ReactNode }) {
  return (
    <div className={"set-update-status " + tone} role="status">
      <PhaseMark phase={tone === "warn" ? "error" : tone} />
      <span>{children}</span>
    </div>
  );
}

export function UpdateBlockView({ current, phase, manifest, progress, checkedAt, error, now, onCheck, onDownload, onRestart, children }: {
  current: string;
  phase: string;
  manifest: { version: string; size: number; notes: string[] } | null;
  progress: number;
  checkedAt: number | null;
  error: string | null;
  /** 相对时间的基准（测试注入；真实链路 = 渲染时刻）。 */
  now: number;
  onCheck: () => void;
  onDownload: () => void;
  onRestart: () => void;
  /** 预留扩展位（测试挂断言锚点用）。 */
  children?: ReactNode;
}) {
  // notes 摘要+展开：默认只露前 3 条，长说明不把区块撑爆
  const [notesOpen, setNotesOpen] = useState(false);
  const checking = phase === "checking";
  const shownNotes = manifest && manifest.notes.length > 0
    ? (notesOpen ? manifest.notes : manifest.notes.slice(0, 3))
    : [];
  const hiddenNotes = manifest ? manifest.notes.length - shownNotes.length : 0;

  return (
    <div className="set-update" data-phase={phase}>
      {/* 头：标题 + 当前版本 + 时间戳 + 检查按钮——按钮恒在，任何状态都可点 */}
      <div className="set-row set-row-block set-update-head">
        <div className="set-update-head-top">
          <div className="set-update-head-text">
            <div className="set-row-label">软件更新</div>
            <div className="set-update-meta">
              <span className="set-update-ver mono">lxcode {current}</span>
              {checkedAt !== null && phase !== "checking" && (
                <>
                  <span className="set-update-meta-sep">·</span>
                  <span className="set-update-meta-time">{relTime(checkedAt, now)}检查</span>
                </>
              )}
            </div>
          </div>
          <Button
            variant="ghost"
            data-su="check"
            onClick={onCheck}
            disabled={checking}
            aria-busy={checking || undefined}
          >
            {checking && <span className="mset-spinner set-update-btn-spin" aria-hidden={true} />}
            {checking ? "检查中…" : "检查更新"}
          </Button>
        </div>
      </div>

      {/* 状态区：每种状态一个明确形态 */}
      {error && (
        <div className="set-update-error" role="alert">
          <span className="set-update-mark err" aria-hidden="true">
            <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round"><path d="M18 6 6 18M6 6l12 12" /></svg>
          </span>
          <span className="set-update-error-text">{error}</span>
          <button type="button" className="set-update-retry" onClick={onCheck}>重试</button>
        </div>
      )}

      {phase === "checking" && (
        <StatusLine tone="neutral">正在检查更新…</StatusLine>
      )}

      {phase === "latest" && (
        <StatusLine tone="ok">已是最新版本</StatusLine>
      )}

      {manifest && (phase === "available" || phase === "downloading" || phase === "ready") && (
        <div className="set-update-panel" data-tone={phase === "ready" ? "ok" : "info"}>
          {phase === "available" && (
            <>
              <div className="set-update-new">
                <span className="set-update-dot" aria-hidden="true" />
                新版本 <span className="mono">{manifest.version}</span>
                <span className="set-update-size">{humanBytes(manifest.size)}</span>
              </div>
              {shownNotes.length > 0 && (
                <ul className="set-update-notes">
                  {shownNotes.map((n) => <li key={n}>{n}</li>)}
                </ul>
              )}
              {hiddenNotes > 0 && (
                <button type="button" className="set-update-notes-toggle" onClick={() => setNotesOpen(true)}>
                  展开其余 {hiddenNotes} 条
                </button>
              )}
              <div className="set-update-actions">
                <Button variant="primary" data-su="download" onClick={onDownload}>
                  下载并安装（{humanBytes(manifest.size)}）
                </Button>
              </div>
            </>
          )}
          {phase === "downloading" && (
            <>
              <div className="set-update-progress-row">
                <div className="set-update-progress">
                  <div className="set-update-progress-bar" style={{ width: `${progress}%` }} />
                </div>
                <span className="mono set-update-progress-num">{progress}%</span>
              </div>
              <div className="set-update-hint">下载中……校验后自动准备安装</div>
            </>
          )}
          {phase === "ready" && (
            <>
              <StatusLine tone="ok">更新已就绪（{manifest.version}）——重启 lxcode 后生效</StatusLine>
              <div className="set-update-actions">
                <Button variant="primary" data-su="restart" onClick={onRestart}>立即重启</Button>
                <span className="set-update-hint inline">稍后重启也行——退出时自动完成替换</span>
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}

export function UpdateBlock() {
  const { current, phase, manifest, progress, checkedAt, error, check, download, restart } = useUpdate();
  return (
    <UpdateBlockView
      current={current}
      phase={phase}
      manifest={manifest}
      progress={progress}
      checkedAt={checkedAt}
      error={error}
      now={Date.now()}
      onCheck={check}
      onDownload={download}
      onRestart={restart}
    />
  );
}
