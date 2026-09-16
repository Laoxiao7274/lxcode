import { useRef, useState } from "react";
import { Orb } from "../../aicss/Orb";
import { PermPicker } from "../perm-picker";
import { ModelPicker } from "../model-picker";
import { ContextIndicator } from "../context-indicator";
import { useEnterRef } from "../../shared/anim";

/** 输入区（Codex 式）：busy 时输入框保留（可预输入），发送钮变停止。 */
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
  // 「生成中」状态行挂载即上浮淡入（busy 翻转时才挂载/卸载）
  const busyRowRef = useEnterRef<HTMLDivElement>({ opacity: 0, y: 6 }, { opacity: 1, y: 0, duration: 0.26, ease: "power2.out", clearProps: "transform,opacity" });
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
        {/* busy 状态行：浮在输入框上方（生成中 + 停止入口在按钮位） */}
        {busy && (
          <div className="busy-row" ref={busyRowRef}>
            <Orb variant="S1" size={16} />
            <span className="busy-text">生成中</span>
          </div>
        )}
        <div className="pi">
          <textarea
            ref={taRef}
            className="piInput"
            placeholder={busy ? "生成中… 可以先输入下一条（完成后发送）" : "让智能体构建、审查或解释点什么…"}
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
            <PermPicker />
            <ModelPicker />
            <ContextIndicator />
            <span className="piTips" />
            {busy ? (
              <button type="button" className="send-btn stop" onClick={onCancel} aria-label="停止生成" title="停止生成">
                <svg width="11" height="11" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                  <rect x="5" y="5" width="14" height="14" rx="2.5" />
                </svg>
              </button>
            ) : (
              <button
                type="button"
                className="send-btn"
                disabled={!canSend}
                onClick={submit}
                aria-label="发送"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M12 19V5" />
                  <path d="m5 12 7-7 7 7" />
                </svg>
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
