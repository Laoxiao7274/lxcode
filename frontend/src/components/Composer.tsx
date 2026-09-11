import { useRef, useState } from "react";
import { Orb } from "../aicss/Orb";

/** 输入区（Codex 式）：大输入框 + 左下权限徽标 + 模型名 + 圆形发送。 */
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
              className="stop-btn"
              onClick={onCancel}
              aria-label="停止生成"
            >
              <svg width="10" height="10" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                <rect x="5" y="5" width="14" height="14" rx="2" />
              </svg>
              停止
            </button>
          </div>
        ) : (
          <div className="pi">
            <textarea
              ref={taRef}
              className="piInput"
              placeholder="让智能体构建、审查或解释点什么…"
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
              <button type="button" className="plus-btn" aria-label="添加" title="添加">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M12 5v14M5 12h14" />
                </svg>
              </button>
              <button type="button" className="perm-chip" title="高危操作会先征求你的同意">
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z" />
                </svg>
                确认后执行
              </button>
              <button type="button" className="model-chip" title="模型与推理强度">
                MYT · medium
                <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="m6 9 6 6 6-6" />
                </svg>
              </button>
              <span className="piTips" />
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
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
