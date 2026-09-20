import { useRef, useState } from "react";
import { AgentPicker } from "../agents/AgentPicker";
import { PermPicker } from "../perm-picker";
import { ModelPicker } from "../model-picker";
import { ContextIndicator } from "../context-indicator";
import { SlashPalette, type SlashCommand } from "./SlashPalette";
import { useEnterRef } from "../../shared/anim";

/** 输入区（Codex 式）：busy 时输入框保留（可预输入），发送钮变停止。
 *  斜杠命令：输入以 / 开头时上方弹命令面板（关键字过滤 + 键盘导航 +
 *  Enter/Tab 补全——面板开着时 Enter 不发送）。 */
export function Composer({
  busy,
  disabled,
  onSend,
  onCancel,
  commands = [],
}: {
  busy: boolean;
  disabled?: boolean;
  onSend: (text: string) => void;
  onCancel: () => void;
  /** 斜杠命令集（App 注入——页面导航；选择器聚焦命令由 piBar 控件自身
   *  的打开态承载，/model 等 = 聚焦后打开对应选择器的实现放命令集里）。 */
  commands?: SlashCommand[];
}) {
  const [value, setValue] = useState("");
  const taRef = useRef<HTMLTextAreaElement>(null);
  // 「生成中」状态行挂载即上浮淡入（busy 翻转时才挂载/卸载）
  const busyRowRef = useEnterRef<HTMLDivElement>({ opacity: 0, y: 6 }, { opacity: 1, y: 0, duration: 0.26, ease: "power2.out", clearProps: "transform,opacity" });
  const canSend = value.trim().length > 0 && !busy && !disabled && !value.startsWith("/");

  // 斜杠面板：输入以 / 开头（单行——/ 出现在行中不算命令）时开
  const slashOpen = !disabled && value.startsWith("/") && !value.includes("\n");
  const slashQuery = slashOpen ? value.replace(/^\/+/, "") : "";

  const submit = () => {
    if (!canSend) return;
    onSend(value.trim());
    setValue("");
    requestAnimationFrame(() => taRef.current?.focus());
  };

  // 面板选中：清输入、跑动作、回焦输入框
  const pick = (c: SlashCommand) => {
    setValue("");
    c.run();
    requestAnimationFrame(() => taRef.current?.focus());
  };

  const onKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (slashOpen) return; // 面板接管键盘（palette 在 window capture 层处理）
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  return (
    <div className="composer-zone">
      <div className="composer-inner">
        {/* busy 状态行：浮在输入框上方（生成中 + 停止入口在按钮位） */}
        {busy && (
          <div className="busy-row" ref={busyRowRef}>
            <span className="mset-spinner" aria-hidden />
            <span className="busy-text">生成中</span>
          </div>
        )}
        <div className="pi">
          {slashOpen && (
            <SlashPalette
              query={slashQuery}
              commands={commands}
              onPick={pick}
              onClose={() => setValue("")}
            />
          )}
          <textarea
            ref={taRef}
            className="piInput"
            placeholder={busy ? "生成中… 可以先输入下一条（完成后发送）" : "让智能体构建、审查或解释点什么…"}
            rows={1}
            value={value}
            disabled={disabled}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={onKey}
          />
          <div className="piBar">
            <AgentPicker />
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
