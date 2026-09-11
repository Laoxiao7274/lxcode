import { useRef, useState } from "react";
import { Orb } from "../aicss/Orb";
import { PopoverMenu } from "./PopoverMenu";
import { useSettings, MODELS, EFFORTS, APPROVALS, type Settings } from "../settings";

const shieldIcon = (size = 11) => (
  <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z" />
  </svg>
);

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
  const { settings, set } = useSettings();
  const canSend = value.trim().length > 0 && !busy && !disabled;

  const submit = () => {
    if (!canSend) return;
    onSend(value.trim());
    setValue("");
    requestAnimationFrame(() => taRef.current?.focus());
  };

  const approvalLabel = APPROVALS.find((a) => a.id === settings.approval)!.label;

  return (
    <div className="composer-zone">
      <div className="composer-inner">
        {/* busy 状态行：浮在输入框上方（生成中 + 停止入口在按钮位） */}
        {busy && (
          <div className="busy-row">
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
            <PopoverMenu
              width={230}
              trigger={() => (
                <button type="button" className="plus-btn" aria-label="添加" title="添加">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M12 5v14M5 12h14" />
                  </svg>
                </button>
              )}
              options={[
                { id: "ctx", label: "引用当前项目", hint: "~/gs/lxcode", onSelect: () => {} },
                { id: "file", label: "附加文件…", onSelect: () => {} },
                { id: "shot", label: "附加截图…", onSelect: () => {} },
              ]}
            />
            <PopoverMenu
              width={260}
              trigger={() => (
                <button type="button" className="perm-chip" title="高危操作确认模式">
                  {shieldIcon()}
                  {approvalLabel}
                </button>
              )}
              options={APPROVALS.map((a) => ({
                id: a.id,
                label: a.label,
                hint: a.hint,
                icon: shieldIcon(13),
                selected: settings.approval === a.id,
                onSelect: () => set({ approval: a.id as Settings["approval"] }),
              }))}
            />
            <PopoverMenu
              width={280}
              title="模型与推理强度"
              trigger={() => (
                <button type="button" className="model-chip" title="模型与推理强度">
                  {settings.model} · {settings.effort === "low" ? "低" : settings.effort === "medium" ? "中" : "高"}
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="m6 9 6 6 6-6" />
                  </svg>
                </button>
              )}
              options={[
                ...MODELS.map((m) => ({
                  id: m.id,
                  label: m.id,
                  hint: m.desc,
                  selected: settings.model === m.id,
                  onSelect: () => set({ model: m.id }),
                })),
                ...EFFORTS.map((e) => ({
                  id: "effort-" + e.id,
                  label: "推理强度 · " + e.label,
                  hint: e.hint,
                  selected: settings.effort === e.id,
                  onSelect: () => set({ effort: e.id }),
                })),
              ]}
            />
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
