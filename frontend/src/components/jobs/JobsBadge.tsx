// 顶栏角标 + 面板（后台任务的全局入口）。
//
// 角标只在「有任务」时显示数字：运行中的数量优先（那才是需要你管的），
// 没有运行中的就显示总数（还能回看结束的任务与它的输出）。
// 入口常驻（没有任务时也点得开）——不然用户永远不知道这里有个面板。
//
// 弹层行为（开合 / 点外 / Esc / gsap 退场）走 usePopover，与 ContextIndicator
// 同一套；面板挂 data-pop 是 usePopover 的约定。
import { useJobs } from "../../shared/jobs-admin";
import { usePopover } from "../../shared/popover";
import { JobsPanel } from "./JobsPanel";

export function JobsBadge() {
  const { jobs, active } = useJobs();
  const { open, toggle, rootRef } = usePopover();
  const count = active > 0 ? active : jobs.length;
  const label = active > 0 ? `后台任务：${active} 个运行中` : `后台任务：${jobs.length} 个`;

  return (
    <div className="jobs-wrap" ref={rootRef}>
      <button
        type="button"
        className={"jobs-badge" + (open ? " on" : "")}
        data-active={active > 0 ? "true" : undefined}
        onClick={toggle}
        aria-expanded={open}
        aria-label={label}
        title={label}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <rect x="3" y="4" width="18" height="16" rx="2.5" />
          <path d="m7 9 2.5 2.5L7 14" />
          <path d="M12.5 14.5H17" />
        </svg>
        {count > 0 && <span className="jobs-count" data-active={active > 0 ? "true" : undefined}>{count}</span>}
      </button>
      {open && (
        <div className="job-pop" role="dialog" aria-label="后台任务" data-pop>
          <JobsPanel />
        </div>
      )}
    </div>
  );
}
