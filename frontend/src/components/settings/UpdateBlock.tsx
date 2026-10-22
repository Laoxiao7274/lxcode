// 软件更新块（设置 · 通用分区底部）：版本号 + 检查入口 + 状态摘要。
// 状态来自 shared/update.tsx 的 UpdateProvider——**唯一的更新交互在
// UpdateDialog**（两条发现新版本的路径都开它）。这里不再内联第二套
// 下载/进度/重启面板：发现新版本后只给一个「查看更新」入口打开同一个弹窗。
//
// 视觉契约：六种状态（idle/checking/latest/available/downloading/ready）
// + error 各有明确的视觉形态，不共用模糊样式；「检查更新」按钮在**所有状态下
// 可见可点**（检查中禁用防连点，检查完恢复）——它消失过一次（旧版只在 idle
// 渲染），测试钉住这条。呈现拆成纯展示的 UpdateBlockView（可静态渲染钉断言）
// + useUpdate 连接器。
import { type ReactNode } from "react";
import { Button } from "../form";
import { humanBytes } from "../../shared/connections";
import { relTime, useUpdate } from "../../shared/update";

/** 状态徽记：每种 phase 一个图标 + 色（纯展示；点不出交互）。 */
function PhaseMark({ phase }: { phase: string }) {
  if (phase === "checking" || phase === "downloading" || phase === "installing") {
    return <span className="set-update-mark spin" aria-hidden="true" />;
  }
  if (phase === "latest" || phase === "ready") {
    return (
      <span className="set-update-mark ok" aria-hidden="true">
        <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round"><path d="M20 6 9 17l-5-5" /></svg>
      </span>
    );
  }
  if (phase === "available") {
    return <span className="set-update-mark info" aria-hidden="true" />;
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

export function UpdateBlockView({ current, phase, manifest, progress, checkedAt, error, now, onCheck, onOpen, children }: {
  current: string;
  phase: string;
  manifest: { version: string; size: number; notes: string[] } | null;
  progress: number;
  checkedAt: number | null;
  error: string | null;
  /** 相对时间的基准（测试注入；真实链路 = 渲染时刻）。 */
  now: number;
  onCheck: () => void;
  /** 打开统一更新弹窗（唯一交互入口，不再内联下载/安装面板）。 */
  onOpen: () => void;
  /** 预留扩展位（测试挂断言锚点用）。 */
  children?: ReactNode;
}) {
  const checking = phase === "checking";
  const hasUpdate = phase === "available" || phase === "downloading" || phase === "ready" || phase === "installing";

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

      {manifest && hasUpdate && (
        <div className="set-update-panel" data-tone={phase === "available" ? "info" : "ok"}>
          <div className="set-update-new">
            <span className="set-update-dot" aria-hidden="true" />
            新版本 <span className="mono">{manifest.version}</span>
            <span className="set-update-size">{humanBytes(manifest.size)}</span>
          </div>
          <div className="set-update-hint">
            {phase === "available" && "下载、安装都在更新弹窗里完成。"}
            {phase === "downloading" && `正在下载（${progress}%）——进度见更新弹窗。`}
            {phase === "ready" && "下载完成，等待安装——安装会重启应用。"}
            {phase === "installing" && "正在安装，应用即将重启…"}
          </div>
          <div className="set-update-actions">
            <Button variant="primary" data-su="open" onClick={onOpen}>查看更新</Button>
          </div>
        </div>
      )}
    </div>
  );
}

export function UpdateBlock() {
  const { current, phase, manifest, progress, checkedAt, error, check, openDialog } = useUpdate();
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
      onOpen={openDialog}
    />
  );
}
