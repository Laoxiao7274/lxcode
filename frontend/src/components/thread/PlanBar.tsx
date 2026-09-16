// PlanBar —— 输入区上方的任务计划条（Codex 的 task sidebar 暴露计划）：
// 当前进度（第 N/M 步）+ active 项文案，点开看全清单。
// 计划常驻可见——「现在干到哪了」不需要在消息流里翻找。
// 动效：挂载上浮、进度条 tween 到位、清单展开交错浮现（motionAllowed 门控）。
import { useEffect, useRef, useState } from "react";
import { gsap } from "gsap";
import type { TodoItem } from "../../shared/types";
import { motionAllowed } from "../../shared/motion";

export function PlanBar({ todos }: { todos: TodoItem[] }) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const fillRef = useRef<HTMLSpanElement>(null);
  const detailRef = useRef<HTMLDivElement>(null);
  const prevDone = useRef(-1);

  if (todos.length === 0) return null;

  const done = todos.filter((t) => t.status === "done").length;
  const active = todos.find((t) => t.status === "active");
  const total = todos.length;

  // 挂载：整条上浮淡入；进度条从 0 tween 到当前值（后续变化也 tween——
  // 进度感来自运动而不只是数字变化）
  useEffect(() => {
    if (!rootRef.current) return;
    if (prevDone.current === -1 && motionAllowed()) {
      gsap.fromTo(rootRef.current, { opacity: 0, y: 6 }, { opacity: 1, y: 0, duration: 0.26, ease: "power2.out", clearProps: "transform,opacity" });
    }
    const pct = (done / total) * 100;
    if (fillRef.current) {
      if (motionAllowed()) {
        gsap.to(fillRef.current, { width: pct + "%", duration: 0.5, ease: "power2.out" });
      } else {
        gsap.set(fillRef.current, { width: pct + "%" });
      }
    }
    prevDone.current = done;
  }, [done, total]);

  // 清单展开：条目交错浮现（收起即时——高度由内容撑）
  useEffect(() => {
    if (!open || !detailRef.current) return;
    const items = detailRef.current.querySelectorAll<HTMLElement>(".plan-item");
    if (items.length && motionAllowed()) {
      gsap.fromTo(items, { opacity: 0, x: -5 }, { opacity: 1, x: 0, duration: 0.22, ease: "power2.out", stagger: 0.035, clearProps: "transform,opacity" });
    }
  }, [open]);

  return (
    <div className="plan-bar" role="status" ref={rootRef}>
      <button type="button" className="plan-summary" aria-expanded={open} onClick={() => setOpen(!open)}>
        <span className="plan-count">{done}/{total}</span>
        <span className="plan-progress" aria-hidden>
          <span className="plan-progress-fill" ref={fillRef} />
        </span>
        {active ? (
          <span className="plan-active">{active.content}</span>
        ) : (
          <span className="plan-done">全部完成</span>
        )}
        <span className="plan-chevron">{open ? "▾" : "▸"}</span>
      </button>
      {open && (
        <div className="plan-detail" ref={detailRef}>
          {todos.map((t) => (
            <div key={t.content} className={"plan-item " + t.status}>
              <span className="plan-dot" aria-hidden />
              {t.content}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
