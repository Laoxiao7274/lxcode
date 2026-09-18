// 软件更新块（设置 · 通用分区底部）：版本号 + 检查/下载/安装流。
// 状态来自 shared/update.tsx 的 UpdateProvider——与页面级 UpdateToast
// 共用（自动检查发现新版本时 Toast 先弹，这里可看详情与继续操作）。
import { Button } from "../form";
import { humanBytes } from "../../shared/connections";
import { useUpdate } from "../../shared/update";

export function UpdateBlock() {
  const { current, phase, manifest, progress, check, download, restart } = useUpdate();

  return (
    <div className="set-update" data-phase={phase}>
      <div className="set-row set-row-block">
        <div className="set-update-head">
          <div className="set-row-label">软件更新</div>
          <span className="set-update-ver mono">lxcode {current}</span>
        </div>

        {phase === "idle" && (
          <div className="set-update-actions">
            <Button variant="ghost" data-su="check" onClick={check}>检查更新</Button>
          </div>
        )}
        {phase === "checking" && (
          <div className="set-update-status">
            <span className="mset-spinner" />
            正在检查更新…
          </div>
        )}
        {phase === "latest" && (
          <div className="set-update-status ok">✓ 已是最新版本</div>
        )}
        {manifest && (phase === "available" || phase === "downloading" || phase === "ready") && (
          <div className="set-update-panel">
            {phase === "available" && (
              <>
                <div className="set-update-new">
                  <span className="set-update-dot" />
                  新版本 <span className="mono">{manifest.version}</span>
                  <span className="set-update-size">{humanBytes(manifest.size)}</span>
                </div>
                <ul className="set-update-notes">
                  {manifest.notes.map((n) => <li key={n}>{n}</li>)}
                </ul>
                <div className="set-update-actions">
                  <Button variant="primary" data-su="download" onClick={download}>
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
                  <span className="mono">{progress}%</span>
                </div>
                <div className="set-update-hint">下载中……校验后自动准备安装</div>
              </>
            )}
            {phase === "ready" && (
              <>
                <div className="set-update-status ok">✓ 更新已就绪（{manifest.version}）——重启 lxcode 后生效</div>
                <div className="set-update-actions">
                  <Button variant="primary" data-su="restart" onClick={restart}>
                    立即重启
                  </Button>
                  <span className="set-update-hint">稍后重启也行——退出时自动完成替换</span>
                </div>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
