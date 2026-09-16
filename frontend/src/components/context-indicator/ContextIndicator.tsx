// 上下文用量指示器：composer 里的环形 + 百分比 chip；点开弹层显示整体
// 上下文使用明细（分类占比条 + 图例 + 剩余/上限）。开合/点外/Esc 由 usePopover 承担。
// mock 数据——真实接入 = chat.history 的 usageTokens/contextWindow + 分类统计。
import { usePopover } from "../../shared/popover";
import { kfmtTokens } from "../../shared/format";

interface Segment {
  label: string;
  tokens: number;
  color: string;
}

const TOTAL = 128_000;
const SEGMENTS: Segment[] = [
  { label: "系统提示", tokens: 4_200, color: "#0d0d0d" },
  { label: "工具结果", tokens: 12_800, color: "#6e6e80" },
  { label: "对话消息", tokens: 15_100, color: "#a9a9b5" },
  { label: "思考链", tokens: 2_100, color: "#d4d4d8" },
];

export function ContextIndicator() {
  const { open, toggle, rootRef } = usePopover();

  const used = SEGMENTS.reduce((s, x) => s + x.tokens, 0);
  const pct = Math.round((used / TOTAL) * 100);
  const remain = TOTAL - used;

  return (
    <div className="ctx-wrap" ref={rootRef}>
      <button
        type="button"
        className={"ctx-chip" + (open ? " on" : "")}
        onClick={toggle}
        aria-expanded={open}
        aria-label={`上下文已用 ${pct}%`}
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
        <span className="ctx-pct">{pct}%</span>
      </button>
      {open && (
        <div className="ctx-pop" role="dialog" aria-label="上下文使用" data-pop>
          <div className="ctx-pop-head">
            <span className="ctx-pop-title">上下文使用</span>
            <span className="ctx-pop-pct">{pct}%</span>
          </div>
          <div className="ctx-bar" role="img" aria-label={`已使用 ${pct}%`}>
            {SEGMENTS.map((s) => (
              <span
                key={s.label}
                className="ctx-bar-seg"
                style={{ width: `${(s.tokens / TOTAL) * 100}%`, background: s.color }}
              />
            ))}
          </div>
          <div className="ctx-legend">
            {SEGMENTS.map((s) => (
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
            已用 <b>{kfmtTokens(used)}</b> · 窗口上限 {kfmtTokens(TOTAL)} tokens
          </div>
          <div className="ctx-hint">接近上限时会自动压缩早期对话（约 90% 触发）</div>
        </div>
      )}
    </div>
  );
}
