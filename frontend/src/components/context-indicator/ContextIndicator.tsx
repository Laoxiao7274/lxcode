// 会话用量指示器：composer 里的环形 + 百分比 chip；点开弹层显示
// ① 上下文占用明细（分类占比条 + 图例 + 剩余/上限）
// ② **会话消耗**（累计四桶 + 缓存命中率）——2026-09-30 用户拍板并进这里：
//    它原先是自己一个独立胶囊（「会话消耗」），挤在输入条里放不下，而且让人以为
//    "窗口占用"与"累计消耗"要在两个地方分别看。
// 开合/点外/Esc 由 usePopover 承担。
//
// 标题口径（2026-09-30 用户拍板）：弹层标题叫「会话用量」，里面那一节的标题叫
// 「会话消耗」——两个名字分得开"此刻窗口里有多少"与"整条会话一共花了多少"。
//
// 数据来自后端测量（store 的 context——chat.done / chat.history 携带）：
// used 优先是 provider 回报的真实 prompt_tokens（再按测量之后历史的变化量投影），
// 五个分类是估算拆分（后端已归一，分类之和 == used）。未知（后端刚重启且库里没有测量/
// 刚切会话）显示中性态「—」，不编数字——假数据比没有数据更坏。
//
// **估算与真实必须看得出区别**：后端 estimated 位为真时这个数字是按固定密度折算的
//（对中文还是低估）。区别的出口是**悬停说明 + 无障碍标签**（title / aria-label），
// 可见记号只有 `~`——2026-09-30 用户拍板去掉百分比后面的「估」字，与 DSH 的
// ContextMeter 一致（DSH 也只在读数上带 ~）。判定在 shared/context-usage.ts（纯函数，有测试钉住）。
import { usePopover } from "../../shared/popover";
import { Button } from "../form";
import { kfmtTokens } from "../../shared/format";
import { contextUsageDisplay, contextFiguresText, contextSegments } from "../../shared/context-usage";
import { usageDialogRows, usageTotalLabel } from "../../shared/session-stats";
import type { ContextUsage, SessionStats } from "../../shared/types";

export function ContextIndicator({ usage, stats, onCompact, busy = false, placement = "up" }: {
  usage: ContextUsage | null;
  /** 会话统计（累计消耗）——缺席或没有 token 时这一节整个不渲染（不显示一排 0）。 */
  stats?: SessionStats | null;
  /** 手动压缩入口（空闲才可用——后端 busy 时会拒绝）。 */
  onCompact?: () => void;
  busy?: boolean;
  /** 弹层往哪边开：**由调用方显式指定**，不猜。
   *  composer 里这个 chip 在屏幕底部 → 向上开（默认，"up"）；
   *  子会话页头在页面**顶部** → 必须向下开，否则弹层跑到视口外被裁掉
   *  （2026-09-30 用户报「上下文展示的下拉框跑上面去被遮住了」）。
   *  为什么不做"自动测量剩余空间"：弹层高度在渲染前未知，而触发器在页面的哪一头
   *  调用方**本来就知道**——显式传比运行时猜稳。 */
  placement?: "up" | "down";
}) {
  const { open, toggle, rootRef } = usePopover();

  const d = contextUsageDisplay(usage);
  const used = usage?.used ?? 0;
  const total = usage?.window ?? 0;
  // 窗口未知（模型没配 context_window）时不给百分比——编一个上限会让
  // 「还剩多少」变成假信息
  const known = d.known;
  const pct = d.pct;
  const figures = contextFiguresText(usage);
  const remain = known ? Math.max(0, total - used) : 0;
  // 分类的拆分与配色只有一份实现（shared/context-usage.ts）——组件只负责画
  const segments = contextSegments(usage);

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
        {/* 百分比是**纯数字**（2026-09-30 用户拍板对齐 DSH）：可见的近似记号只有
            `~`（弹层读数与分类行上），"这份数字是估的"走 title/aria-label。
            原先百分比后面挂「估」字，DSH 没有这个记号，chip 也会变宽 */}
        <span className="ctx-pct">{d.pctText}</span>
      </button>
      {open && (
        <div className={"ctx-pop" + (placement === "down" ? " down" : "")} role="dialog" aria-label="会话用量" data-pop>
          <div className="ctx-pop-head">
            <span className="ctx-pop-title">会话用量</span>
            {/* DSH 的 ContextMeter 头部：百分比 + 「~used / window」并排（近似值带 ~，
                口径来自 shared/context-usage.ts 的纯函数） */}
            {figures !== null && <span className="ctx-pop-figures">{figures}</span>}
            <span className="ctx-pop-pct">{d.pctText}</span>
          </div>
          {known ? (
            <>
              <div className="ctx-bar" role="img" aria-label={d.estimated ? `已使用约 ${pct}%（估算）` : `已使用 ${pct}%`}>
                {segments.map((s) => (
                  <span
                    key={s.key}
                    className="ctx-bar-seg"
                    style={{ width: `${(s.tokens / total) * 100}%`, background: s.color }}
                  />
                ))}
              </div>
              <div className="ctx-legend">
                {segments.map((s) => (
                  <div className="ctx-legend-row" key={s.key}>
                    <span className="ctx-dot" style={{ background: s.color }} aria-hidden />
                    <span className="ctx-legend-label">{s.label}</span>
                    {/* ~ = 近似值（分类是估算拆分，后端按固定密度折算后归一到真实总量） */}
                    <span className="ctx-legend-val">~{kfmtTokens(s.tokens)}</span>
                  </div>
                ))}
                <div className="ctx-legend-row rest">
                  <span className="ctx-dot rest" aria-hidden />
                  <span className="ctx-legend-label">剩余</span>
                  <span className="ctx-legend-val">~{kfmtTokens(remain)}</span>
                </div>
              </div>
              <div className="ctx-foot">
                {/* 数字带 ~ = 近似值（DSH 的 ContextMeter 同款记号，也是界面上**唯一**的
                    近似记号）：总量取真实用量、分类是估算拆分，两者都不是精确到个位的读数。
                    「这份数字是估的」不在这里挂字——见下面那行说明 */}
                已用 <b>~{kfmtTokens(used)}</b> · 窗口上限 <b>~{kfmtTokens(total)}</b> tokens
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
          {/* ===== 会话消耗（累计）=====
              独立于上下文占用：窗口未知（模型没配 context_window）时它照样有值，
              所以放在 known 判断**之外**。没有 token 时整节不渲染（不显示一排 0）。 */}
          <SessionUsage stats={stats} />
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

/** 「会话用量」弹层里的**会话消耗**一节（累计四桶 + 缓存命中率）。
 *
 *  为什么单独一个组件：弹层在静态渲染下是关着的（usePopover 的 open 初值为 false，
 *  测试环境没有 DOM 也点不动），抽出来才测得到接线——不然"口径对、组件忘了渲染"
 *  这类回归只能靠肉眼。
 *
 *  没有 token（老会话：本功能上线前落库的消息没有那四列）时返回 null：
 *  显示「0 tok · 缓存命中 0%」是编数字（后端已把那些行单独记进 legacy_tokens）。 */
export function SessionUsage({ stats }: { stats?: SessionStats | null }) {
  if (!stats) return null;
  const rows = usageDialogRows(stats);
  if (rows.length === 0) return null;
  return (
    <div className="ctx-usage">
      <div className="ctx-usage-head">
        <span className="ctx-usage-title">会话消耗</span>
        <span className="ctx-usage-total">{usageTotalLabel(stats)}</span>
      </div>
      <dl className="stats-rows">
        {rows.map((r) => (
          <div className="stats-row" key={r.label}>
            <dt>{r.label}</dt>
            <dd>{r.value}</dd>
          </div>
        ))}
      </dl>
      <div className="ctx-hint">每一步的 prompt 都算一次（累计消耗）——与上面的"此刻窗口里有多少"不是一回事</div>
    </div>
  );
}
