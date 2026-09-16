// PlanBar
import { useState } from "react";
import type { TodoItem } from "../../shared/types";

export function PlanBar({ todos }: { todos: TodoItem[] }) {
  const [open, setOpen] = useState(false);
  if (todos.length === 0) return null;

  const done = todos.filter((t) => t.status === "done").length;
  const active = todos.find((t) => t.status === "active");
  const total = todos.length;

  return (
    <div className="plan-bar" role="status">
      <button type="button" className="plan-summary" aria-expanded={open} onClick={() => setOpen(!open)}>
        <span className="plan-count">{done}/{total}</span>
        <span className="plan-progress" aria-hidden>
          <span className="plan-progress-fill" style={{ width: `${(done / total) * 100}%` }} />
        </span>
        {active ? (
          <span className="plan-active">{active.content}</span>
        ) : (
          <span className="plan-done">全部完成</span>
        )}
        <span className="plan-chevron">{open ? "▾" : "▸"}</span>
      </button>
      {open && (
        <div className="plan-detail">
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