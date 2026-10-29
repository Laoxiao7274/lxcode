// @aicss/react 0.1.3 (MIT) vendor 改造：从自动步进演示改为受控组件。
// items 由调用方（todo.updated 事件）驱动；滚动数字/状态图标/折叠原样。
import styles from "./TodoList.module.css";
import { useEffect, useRef, useState } from "react";

export interface TodoItem {
  content: string;
  status: "pending" | "active" | "done";
}

const cls = (base: string, on?: boolean) => base + (on ? " " + styles.on : "");
const CheckIcon = ({ on }: { on?: boolean }) => (
  <svg className={cls(styles.todoIcon, on)} viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
    <path d="M9 12.75 11.25 15 15 9.75M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
  </svg>
);
const ArrowIcon = ({ on }: { on?: boolean }) => (
  <svg className={cls(styles.todoIcon + " " + styles.strong, on)} viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
    <path d="m12.75 15 3-3m0 0-3-3m3 3h-7.5M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
  </svg>
);
const DashedIcon = ({ on }: { on?: boolean }) => (
  <svg className={cls(styles.todoIcon, on)} viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
    <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" strokeWidth="1.8" strokeDasharray="1.8 3.6" strokeLinecap="round" />
  </svg>
);

// 数字位滚动（0/5 → 1/5 时旧数字滚上新数字）
const RollDigit = ({ char }: { char: string }) => {
  const prev = useRef(char);
  const [roll, setRoll] = useState<{ from: string; to: string } | null>(null);
  const [up, setUp] = useState(false);
  useEffect(() => {
    if (char === prev.current) return;
    const from = prev.current;
    prev.current = char;
    setRoll({ from, to: char });
    setUp(false);
    const raf = requestAnimationFrame(() => requestAnimationFrame(() => setUp(true)));
    const done = setTimeout(() => setRoll(null), 380);
    return () => { cancelAnimationFrame(raf); clearTimeout(done); };
  }, [char]);
  if (!roll) return <span className={styles.rollDigit}>{char}</span>;
  return (
    <span className={styles.rollDigit}>
      <span className={cls(styles.rollInner, up)}>
        <span>{roll.from}</span>
        <span>{roll.to}</span>
      </span>
    </span>
  );
};
/** 一段「图形 + 数字」：图标色随状态（done 绿勾 / active 渐变箭头 / pending 虚线）。 */
const CountSegment = ({ kind, value }: { kind: "done" | "active" | "pending"; value: string }) => (
  <span className={styles.todoSeg + " " + styles["seg" + kind.charAt(0).toUpperCase() + kind.slice(1)]}>
    {kind === "done" ? <CheckIcon on /> : kind === "active" ? <ArrowIcon on /> : <DashedIcon on />}
    <RollingCount value={value} />
  </span>
);
/** DSH TodoDock 头部三段计数：✓ N · ▶ M · ○ K（段间点分隔；为 0 的段仍显示——
 *  三段恒在，一眼看到「还有什么没做」而不是猜缺席段的含义）。 */
const RollingCount = ({ value, done, active, pending }: { value?: string; done?: string; active?: string; pending?: string }) => {
  if (done !== undefined && active !== undefined && pending !== undefined) {
    return (
      <span className={styles.rollCount} role="status" aria-label={`完成 ${done} · 进行 ${active} · 待办 ${pending}`}>
        <CountSegment kind="done" value={done} />
        <span className={styles.todoSegDot} aria-hidden="true">·</span>
        <CountSegment kind="active" value={active} />
        <span className={styles.todoSegDot} aria-hidden="true">·</span>
        <CountSegment kind="pending" value={pending} />
      </span>
    );
  }
  return (
    <span className={styles.rollCount} aria-label={value ?? ""}>
      {(value ?? "").split("").map((c, i) => <RollDigit key={i} char={c} />)}
    </span>
  );
};
const FilledCheckIcon = () => (
  <svg className={styles.todoHeadCheck} viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
    <path fillRule="evenodd" clipRule="evenodd" d="M2.25 12c0-5.385 4.365-9.75 9.75-9.75s9.75 4.365 9.75 9.75-4.365 9.75-9.75 9.75S2.25 17.385 2.25 12Zm13.36-1.814a.75.75 0 1 0-1.22-.872l-3.236 4.53L9.53 12.22a.75.75 0 0 0-1.06 1.06l2.25 2.25a.75.75 0 0 0 1.14-.094l3.75-5.25Z" fill="currentColor" />
  </svg>
);

export function TodoList({ items }: { items: TodoItem[] }) {
  const [collapsed, setCollapsed] = useState(false);
  const n = items.length;
  const doneCount = items.filter((it) => it.status === "done").length;
  const activeCount = items.filter((it) => it.status === "active").length;
  const pendingCount = n - doneCount - activeCount;
  const running = activeCount > 0;
  const allDone = n > 0 && doneCount === n;
  const pct = Math.round((doneCount / Math.max(n, 1)) * 100);

  // todo 契约（批②）：有 active 项时自动展开一次（用户在看进度的场景里，
  // 展开的收益大于打扰）；全 done 自动收起。折叠态本身用户可随时手动覆盖。
  useEffect(() => {
    if (running) setCollapsed(false);
    else if (allDone) setCollapsed(true);
  }, [running, allDone]);

  return (
    <div className={styles.todo}>
      <button
        type="button"
        className={styles.todoHead}
        aria-expanded={!collapsed}
        aria-label="展开或折叠任务清单"
        onClick={() => setCollapsed((c) => !c)}
      >
        <span className={styles.todoHeadIcon}>
          {allDone ? (
            <FilledCheckIcon />
          ) : running ? (
            <span className={styles.todoHeadPie} style={{ ["--todo-pie" as string]: pct + "%" }} aria-hidden="true">
              <svg className={styles.todoHeadPieRing} viewBox="0 0 24 24">
                <circle cx="12" cy="12" r="10.5" fill="none" stroke="currentColor" strokeWidth="2.2" strokeDasharray="2.2 4.4" strokeLinecap="round" />
              </svg>
            </span>
          ) : (
            <svg className={styles.todoListIcon} viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M13 5h8" />
              <path d="M13 12h8" />
              <path d="M13 19h8" />
              <path d="m3 17 2 2 4-4" />
              <path d="m3 7 2 2 4-4" />
            </svg>
          )}
          <svg className={styles.todoChevron} viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
            <path d="m19.5 8.25-7.5 7.5-7.5-7.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </span>
        <span className={styles.todoTitle}>任务清单</span>
        {/* DSH TodoDock 三段计数：✓ N · ▶ M · ○ K（active 段强调色）。
            全 done 时整头显示「完成 N 项」。 */}
        <span className={styles.todoCount}>
          {allDone ? (
            <span className={styles.todoCountDone}>完成 {n} 项</span>
          ) : (
            <RollingCount
              value={`${doneCount}`}
              done={`${doneCount}`}
              active={`${activeCount}`}
              pending={`${pendingCount}`}
            />
          )}
        </span>
      </button>

      <div className={styles.todoCollapsible + (collapsed ? " " + styles.isCollapsed : "")}>
        <div className={styles.todoInner}>
          <ul className={styles.todoList}>
            {items.map((it, i) => {
              const done = it.status === "done";
              const active = it.status === "active";
              return (
                <li
                  key={i}
                  className={styles.todoItem + (done ? " " + styles.done : active ? " " + styles.active : "")}
                  style={{ ["--i" as string]: i }}
                >
                  <span className={styles.todoIconWrap}>
                    <DashedIcon on={!done && !active} />
                    <ArrowIcon on={active} />
                    <CheckIcon on={done} />
                  </span>
                  <span className={styles.todoLabel} data-label={it.content}>{it.content}</span>
                </li>
              );
            })}
          </ul>
        </div>
      </div>
    </div>
  );
}
