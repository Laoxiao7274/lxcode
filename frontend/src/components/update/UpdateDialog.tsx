// UpdateDialog —— 统一的更新弹窗（两条「发现新版本」路径的唯一提示形态）。
//
// 入口（同一份状态，见 shared/update.tsx）：
//   ① 启动检查 / 设置页「检查更新」→ check() 发现新版本 → dialogOpen=true；
//   ② 壳主进程周期检查推送 update:available → onAvailable → dialogOpen=true。
// 原来的右下角 Toast 已删除——不允许两套提示形态并存。
//
// 阶段：available（版本/说明/大小 + 下载）→ downloading（百分比 + 已下载/总量 +
// 进度条）→ ready（安装会重启应用）→ installing（已排程，即将重启）。
// 失败：error + 按失败步骤重试（检查/下载/安装）。
// 关闭语义见 closeHint：下载中关闭 = 后台继续，完成时重新打开引导安装。
import { useRef } from "react";
import { Button } from "../form";
import { humanBytes } from "../../shared/connections";
import { useEscape } from "../../shared/popover";
import { useEnterRef } from "../../shared/anim";
import { useUpdate } from "../../shared/update";
import { bytesText, canClose, closeHint, dialogTitle, percentOf, retryLabel, stageText } from "./update-dialog-model";

export function UpdateDialog() {
  const {
    current, phase, manifest, progress, downloaded, total, stage,
    error, failAction, dialogOpen, closeDialog, download, install, check, retry,
  } = useUpdate();
  const panelEnter = useEnterRef<HTMLDivElement>();
  const closable = canClose(phase);
  useEscape(dialogOpen && closable, closeDialog);
  // 防重复提交：安装按钮点一次就定格（重启前连点不该排两次程）
  const installFired = useRef(false);

  if (!dialogOpen) return null;
  const pct = percentOf(downloaded, total, progress);
  const showProgress = phase === "downloading" || phase === "installing";
  const showVersion = manifest && (phase === "available" || phase === "downloading" || phase === "ready" || phase === "installing");

  return (
    <div
      className="proj-add-mask upd-mask"
      role="dialog"
      aria-modal="true"
      aria-label="软件更新"
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Escape" && closable) closeDialog();
      }}
      onPointerDown={(e) => {
        e.stopPropagation();
        // 点遮罩关闭：安装中不关（应用即将重启）
        if (e.target === e.currentTarget && closable) closeDialog();
      }}
      onClick={(e) => e.stopPropagation()}
    >
      <div className="proj-add upd-dialog" ref={panelEnter} data-phase={phase}>
        <div className="proj-add-head">
          <span className="proj-add-title">{dialogTitle(phase)}</span>
          <button
            type="button"
            className="proj-add-close"
            aria-label={closable ? "关闭" : "安装进行中，无法关闭"}
            title={closeHint(phase)}
            onClick={closable ? closeDialog : undefined}
            disabled={!closable}
            data-ud="close"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="m18 6-12 12M6 6l12 12" />
            </svg>
          </button>
        </div>

        <div className="proj-add-body upd-body">
          {/* 版本信息：新版本 / 当前版本 / 包大小 */}
          {showVersion && manifest && (
            <div className="upd-vers">
              <div className="upd-vers-row">
                <span className="upd-vers-label">新版本</span>
                <span className="upd-vers-new mono">{manifest.version}</span>
                {manifest.size > 0 && <span className="upd-vers-size">{humanBytes(manifest.size)}</span>}
              </div>
              <div className="upd-vers-row">
                <span className="upd-vers-label">当前版本</span>
                <span className="upd-vers-cur mono">{current}</span>
              </div>
            </div>
          )}

          {/* 更新说明（manifest 有才渲染；长列表在框内滚动，不撑爆弹窗） */}
          {showVersion && manifest && manifest.notes.length > 0 && (
            <div className="upd-notes-block">
              <div className="upd-notes-title">更新说明</div>
              <ul className="upd-notes">
                {manifest.notes.map((n) => <li key={n}>{n}</li>)}
              </ul>
            </div>
          )}

          {phase === "checking" && <div className="upd-hint">正在检查更新…</div>}
          {phase === "latest" && <div className="upd-hint">当前已是最新版本。</div>}
          {phase === "available" && !manifest && <div className="upd-hint">发现新版本。</div>}

          {/* 下载进度：百分比 + 已下载/总量 + 进度条 */}
          {showProgress && (
            <div className="upd-progress-block" role="status" aria-live="polite">
              <div className="upd-progress-head">
                <span className="upd-progress-text">
                  {phase === "installing" ? "正在排程安装…" : stageText(stage, pct)}
                </span>
                <span className="mono upd-progress-num">{pct}%</span>
              </div>
              <div className="set-update-progress">
                <div className="set-update-progress-bar" style={{ width: `${pct}%` }} />
              </div>
              <div className="upd-progress-bytes mono">
                {phase === "installing" ? "" : bytesText(downloaded, total)}
              </div>
            </div>
          )}

          {/* 就绪：明确告知安装会重启应用 */}
          {phase === "ready" && (
            <div className="upd-ready">
              安装会关闭并重启应用——重启后即为新版本。已打开的会话与项目不受影响。
            </div>
          )}
          {phase === "installing" && (
            <div className="upd-ready">安装已排程，应用即将重启完成替换…</div>
          )}

          {/* 失败态：可读错误 + 重试 */}
          {error && (
            <div className="set-update-error upd-error" role="alert">
              <span className="set-update-mark err" aria-hidden="true">
                <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round"><path d="M18 6 6 18M6 6l12 12" /></svg>
              </span>
              <span className="set-update-error-text">{error}</span>
            </div>
          )}

          {closable && phase !== "available" && phase !== "ready" && phase !== "downloading" && (
            <div className="upd-hint upd-close-hint">{closeHint(phase)}</div>
          )}
        </div>

        <div className="proj-add-foot upd-foot">
          {error ? (
            <>
              <button type="button" className="proj-add-cancel" onClick={closable ? closeDialog : undefined} disabled={!closable}>
                关闭
              </button>
              <button type="button" className="proj-add-ok" data-ud="retry" onClick={retry}>
                {retryLabel(failAction)}
              </button>
            </>
          ) : phase === "available" ? (
            <>
              <button type="button" className="proj-add-cancel" onClick={closeDialog}>稍后</button>
              <button type="button" className="proj-add-ok" data-ud="download" onClick={() => void download()}>
                立即更新{manifest && manifest.size > 0 ? `（${humanBytes(manifest.size)}）` : ""}
              </button>
            </>
          ) : phase === "downloading" ? (
            <>
              <button type="button" className="proj-add-cancel" onClick={closeDialog} title={closeHint(phase)}>
                后台下载
              </button>
              <button type="button" className="proj-add-ok" data-ud="downloading" disabled aria-busy="true">
                <span className="mset-spinner upd-btn-spin" aria-hidden={true} />
                正在下载…
              </button>
            </>
          ) : phase === "ready" ? (
            <>
              <button type="button" className="proj-add-cancel" onClick={closeDialog}>稍后安装</button>
              <button
                type="button"
                className="proj-add-ok"
                data-ud="install"
                onClick={() => {
                  if (installFired.current) return;
                  installFired.current = true;
                  void install();
                }}
              >
                立即安装
              </button>
            </>
          ) : phase === "installing" ? (
            <button type="button" className="proj-add-ok" data-ud="installing" disabled aria-busy="true">
              <span className="mset-spinner upd-btn-spin" aria-hidden={true} />
              正在安装…
            </button>
          ) : phase === "checking" ? (
            <Button variant="ghost" data-ud="check" disabled aria-busy="true">检查中…</Button>
          ) : (
            <>
              <button type="button" className="proj-add-cancel" onClick={closeDialog}>关闭</button>
              <button type="button" className="proj-add-ok" data-ud="check" onClick={check}>检查更新</button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
