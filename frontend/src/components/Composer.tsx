import { useRef, useState } from "react";
import { Orb } from "../aicss/Orb";

/** 输入区：v3 简化形态（textarea + 快捷键提示 + 发送），busy 时让位给 orb。 */
export function Composer({
  busy,
  disabled,
  onSend,
  onCancel,
}: {
  busy: boolean;
  disabled?: boolean;
  onSend: (text: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState("");
  const taRef = useRef<HTMLTextAreaElement>(null);
  const canSend = value.trim().length > 0 && !busy && !disabled;

  const submit = () => {
    if (!canSend) return;
    onSend(value.trim());
    setValue("");
    requestAnimationFrame(() => taRef.current?.focus());
  };

  return (
    <div className="composer-zone">
      <div className="composer-inner">
        {busy ? (
          <div className="composer-busy">
            <Orb variant="S1" size={18} label="生成中" pill />
            <span style={{ flex: 1 }} />
            <button
              type="button"
              className="sidebar-btn"
              style={{ width: "auto", padding: "4px 12px", marginBottom: 0 }}
              onClick={onCancel}
            >
              停止
            </button>
          </div>
        ) : (
          <div className="pi">
            <textarea
              ref={taRef}
              className="piInput"
              placeholder="给智能体一个任务…"
              rows={1}
              value={value}
              disabled={disabled}
              onChange={(e) => setValue(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  submit();
                }
              }}
            />
            <div className="piBar">
              <span className="piTips">
                <kbd>Enter</kbd> 发送
                <kbd>Shift+Enter</kbd> 换行
              </span>
              <button type="button" className="piSend" disabled={!canSend} onClick={submit}>
                发送
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
