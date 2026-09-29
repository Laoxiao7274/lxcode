// 任务输出视图（时间线卡片与顶栏面板共用）。
//
// 折叠态 = 快照里的 output_tail 末尾几行（job.started/settled 直接带，
// 不必往返后端）；展开 = 按需 job.log 读**全量**（落盘日志，任务结束后仍可读
// ——这是本设计相对纯内存缓冲的硬收益：dev server 关掉之后还能回头看它报了什么）。
//
// **刻意不轮询**：契约里没有「输出增量」事件，job.log 也没有游标参数（每次都是
// 全量）。给一个跑了几小时的 dev server 每 3s 拉一次全量日志，比让用户点一下
// 贵得多。用户点「查看输出」就是显式意图，拉一次即够。
import { useState } from "react";
import type { JobInfo } from "../../shared/types";
import { isJobActive, tailOf } from "../../shared/jobs";
import { useJobs } from "../../shared/jobs-admin";
import { Button } from "../form";

/** 折叠态的预览行数（4 行够看出任务在干什么，又不至于把卡片撑成日志墙）。 */
const PREVIEW_LINES = 4;
/** 展开态的渲染上限：全量日志可能几十万行，整份塞进 DOM 会把页面拖死。
 *  超出时只渲染末尾——看日志永远是看最后发生了什么。 */
const MAX_RENDER_LINES = 2000;

export function JobOutput({ job }: { job: JobInfo }) {
  const { log } = useJobs();
  const [loaded, setLoaded] = useState<{ key: string; data: string; truncated: boolean } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // 任务快照换代（settle）后，先前拉的全量日志可能已经过时——用 finished_at
  // 当缓存键，换代即作废（否则展开区停在一个「差最后几行」的旧副本上）。
  const key = `${job.id}:${job.finished_at}`;
  const full = loaded && loaded.key === key ? loaded : null;
  const open = full !== null;

  const text = full ? full.data : job.output_tail;
  const { lines, clipped, total } = tailOf(text, open ? MAX_RENDER_LINES : PREVIEW_LINES);
  const hidden = Math.max(0, total - lines.length);
  const active = isJobActive(job);

  /** 拉一次全量日志（展开与刷新是同一个动作——都是「现在给我看全部」）。 */
  const fetchLog = () => {
    setBusy(true);
    setError(null);
    log(job.id)
      .then((r) => setLoaded({ key, data: r.data, truncated: r.truncated }))
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  const toggle = () => {
    if (open) setLoaded(null);
    else fetchLog();
  };

  return (
    <div className="job-output" data-open={open ? "true" : undefined}>
      {lines.length > 0 ? (
        <div className="job-out-lines">
          {clipped && <div className="job-out-more">…前面还有 {hidden} 行</div>}
          {lines.map((line, i) => (
            <div className="job-out-line" key={i}>{line}</div>
          ))}
        </div>
      ) : (
        <div className="job-out-empty">{active ? "（还没有输出）" : "（无输出）"}</div>
      )}
      <div className="job-out-foot">
        {(total > 0 || active) && (
          <>
            {/* 展开态且任务还在跑：给一次手动刷新——不自动轮询（见头注） */}
            {open && active && (
              <Button variant="ghost" className="job-out-btn" onClick={fetchLog} disabled={busy}>
                刷新
              </Button>
            )}
            <Button variant="ghost" className="job-out-btn" onClick={toggle} disabled={busy}>
              {busy ? "读取中…" : open ? "收起" : "查看输出"}
            </Button>
          </>
        )}
        {full?.truncated && <span className="job-out-note">日志过大，后端已截断</span>}
        {!open && clipped && <span className="job-out-note">显示末尾 {lines.length} 行</span>}
        {job.output_path && (
          <span className="job-out-path mono" title={`落盘日志：${job.output_path}`}>{job.output_path}</span>
        )}
      </div>
      {error && <div className="job-out-err">{error}</div>}
    </div>
  );
}
