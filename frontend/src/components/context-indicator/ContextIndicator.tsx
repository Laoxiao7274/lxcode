// 上下文用量指示器：composer 里的环形 + 百分比 chip；点开弹层显示整体
// 上下文使用明细（分类占比条 + 图例 + 剩余/上限）。开合/点外/Esc 由 usePopover 承担。
//
// 数据来自后端测量（store 的 context——chat.done / chat.history 携带）：
// used 优先是 provider 回报的真实 prompt_tokens，四个分类是估算拆分（后端已
// 归一，分类之和 == used）。未知（后端刚重启/刚切会话）显示中性态「—」，
// 不编数字——假数据比没有数据更坏。
import { usePopover } from "../../shared/popover";
import { kfmtTokens } from "../../shared/format";
import type { ContextUsage } from "../../shared/types";

/** 分类占比条的配色（与设计 token 对齐：越靠前的部分越"固定"）。 */
const SEGMENT_COLORS = {
  system: "#0d0d0d",
  tool_results: "#6e6e80",
  messages: "#a9a9b5",
  reasoning: "#d4d4d8",
} as const;

const SEGMENT_LABELS: Array<[keyof typeof SEGMENT_COLORS, string]> = [
  ["system", "系统提示"],
  ["tool_results", "工具结果"],
  ["messages", "对话消息"],
  ["reasoning", "思考链"],
];

export function ContextIndicator({ usage, onCompact, busy = false }: {
  usage: ContextUsage | null;
  /** 手动压缩入口（空闲才可用——后端 busy 时会拒绝）。 */
  onCompact?: () => void;
  busy?: boolean;
}) {
  const { open, toggle, rootRef } = usePopover();

  const used = usage?.used ?? 0;
  const total = usage?.window ?? 0;
  // 窗口未知（模型没配 context_window）时不给百分比——编一个上限会让
  // 「还剩多少」变成假信息
  const known = usage !== null && used > 0 && total > 0;
  const pct = known ? Math.min(100, Math.round((used / total) * 100)) : 0;
  const remain = known ? Math.max(0, total - used) : 0;
  const segments = SEGMENT_LABELS
    .map(([key, label]) => ({ label, tokens: usage?.[key] ?? 0, color: SEGMENT_COLORS[key] }))
    .filter((s) => s.tokens > 0);

  return (
    <div className="ctx-wrap" ref={rootRef}>
      <button
        type="button"
        className={"ctx-chip" + (open ? " on" : "")}
        onClick={toggle}
        aria-expanded={open}
        aria-label={known ? `上下文已用 ${pct}%` : "上下文用量未知"}
      >
        <svg width="16" height="16" viewBox="0 0 20 20" aria-hidden="true">
          <circle cx="10" cy="10" r="8" fill="none" stroke="var(--border-strong)" strokeWidth="2.5" />
          <circle
            cx="10" cy="10" r="8" fill="none"
            stroke={pct > 80 ? "var(--danger)" : "var(--fg-muted)"}
            strokeWidth="2.5"
            strokeLinecap="round"
            strokeDasharray={`${(pct / 100) * 50.27} 50.27`}
            transform="rotate(-90 10 10)"
          />
        </svg>
        <span className="ctx-pct">{known ? `${pct}%` : "—"}</span>
      </button>
      {open && (
        <div className="ctx-pop" role="dialog" aria-label="上下文使用" data-pop>
          <div className="ctx-pop-head">
            <span className="ctx-pop-title">上下文使用</span>
            <span className="ctx-pop-pct">{known ? `${pct}%` : "—"}</span>
          </div>
          {known ? (
            <>
              <div className="ctx-bar" role="img" aria-label={`已使用 ${pct}%`}>
                {segments.map((s) => (
                  <span
                    key={s.label}
                    className="ctx-bar-seg"
                    style={{ width: `${(s.tokens / total) * 100}%`, background: s.color }}
                  />
                ))}
              </div>
              <div className="ctx-legend">
                {segments.map((s) => (
                  <div className="ctx-legend-row" key={s.label}>
                    <span className="ctx-dot" style={{ background: s.color }} aria-hidden />
                    <span className="ctx-legend-label">{s.label}</span>
                    <span className="ctx-legend-val">{kfmtTokens(s.tokens)}</span>
                  </div>
                ))}
                <div className="ctx-legend-row rest">
                  <span className="ctx-dot rest" aria-hidden />
                  <span className="ctx-legend-label">剩余</span>
                  <span className="ctx-legend-val">{kfmtTokens(remain)}</span>
                </div>
              </div>
              <div className="ctx-foot">
                已用 <b>{kfmtTokens(used)}</b> · 窗口上限 {kfmtTokens(total)} tokens
              </div>
              <div className="ctx-hint">
                分类为估算拆分（后端按固定密度折算）；总量优先取上一次请求的真实用量
              </div>
            </>
          ) : (
            <div className="ctx-foot">
              还没有可测量的请求——发一条消息后这里会显示真实占用。
              <br />
              （模型未配置 context_window 时也无法算出占比）
            </div>
          )}
          {onCompact && (
            <div className="ctx-actions">
              <button
                type="button"
                className="ctx-compact-btn"
                data-ctx="compact"
                onClick={onCompact}
                disabled={busy}
                title={busy ? "生成中不能压缩（先停止）" : "把早期历史压成一份摘要，腾出上下文"}
              >
                立即压缩历史
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
