// 上下文用量指示器：composer 里的环形 + 百分比 chip；点开弹层显示整体
// 上下文使用明细（分类占比条 + 图例 + 剩余/上限）。开合/点外/Esc 由 usePopover 承担。
//
// 数据来自后端测量（store 的 context——chat.done / chat.history 携带）：
// used 优先是 provider 回报的真实 prompt_tokens，四个分类是估算拆分（后端已
// 归一，分类之和 == used）。未知（后端刚重启且库里没有测量/刚切会话）显示中性态「—」，
// 不编数字——假数据比没有数据更坏。
//
// **估算与真实必须看得出区别**：后端 estimated 位为真时这个数字是按固定密度折算的
//（对中文还是低估），所以百分比后面带「估」字、并给一句明说"不是真实用量"的说明——
// 用户看不出区别就会拿它做预算判断。判定在 shared/context-usage.ts（纯函数，有测试钉住）。
import { usePopover } from "../../shared/popover";
import { Button } from "../form";
import { kfmtTokens } from "../../shared/format";
import { contextUsageDisplay } from "../../shared/context-usage";
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

  const d = contextUsageDisplay(usage);
  const used = usage?.used ?? 0;
  const total = usage?.window ?? 0;
  // 窗口未知（模型没配 context_window）时不给百分比——编一个上限会让
  // 「还剩多少」变成假信息
  const known = d.known;
  const pct = d.pct;
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
        aria-label={d.title}
        title={d.title}
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
        {/* 估算值带「估」后缀（contextUsageDisplay 定的口径）：这是用户唯一能在
            不展开弹层时看到区别的地方 */}
        <span className="ctx-pct">{d.pctText}</span>
      </button>
      {open && (
        <div className="ctx-pop" role="dialog" aria-label="上下文使用" data-pop>
          <div className="ctx-pop-head">
            <span className="ctx-pop-title">上下文使用</span>
            <span className="ctx-pop-pct">{d.pctText}</span>
          </div>
          {known ? (
            <>
              <div className="ctx-bar" role="img" aria-label={d.estimated ? `已使用约 ${pct}%（估算）` : `已使用 ${pct}%`}>
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
                已用 <b>{kfmtTokens(used)}</b>{d.estimated ? "（估）" : ""} · 窗口上限 {kfmtTokens(total)} tokens
              </div>
              <div className="ctx-hint">
                {d.estimated
                  ? "总量是估算值（这次请求没拿到真实用量，或本会话没有真实测量）：按固定密度折算，对中文偏低——别拿它做精确预算"
                  : "分类为估算拆分（后端按固定密度折算）；总量取上一次请求的真实用量"}
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
              {/* 走表单套件的 Button（.fd-btn-g），不裸写原生 button——
                  裸写就是 OS 默认皮肤（这条曾经漏了 CSS，渲染成灰色系统按钮） */}
              <Button
                className="ctx-compact"
                data-ctx="compact"
                onClick={onCompact}
                disabled={busy}
                title={busy ? "生成中不能压缩（先停止）" : "把早期历史压成一份摘要，腾出上下文"}
              >
                立即压缩历史
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
