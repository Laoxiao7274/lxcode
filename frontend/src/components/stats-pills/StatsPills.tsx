// 会话统计胶囊（对齐 DSH 的 StatsPills）：**输入框那一行**一个胶囊，紧挨上下文环——
//   仪表盘 + 「N 轮 · M 步 · X tok/s」，点开是模型时间 / 工具时间 / 首字延迟 / 生成速度。
//
// 位置是用户拍板（2026-09-30）：上下文环与统计都是"这条会话花了多少"的读数，放一起看；
// 早先按 DSH 挂在输入框上方一行，用户要求移进 .piBar。
//
// **用量那一半不在这个胶囊里**（2026-09-30 用户拍板）：累计消耗（四桶 + 缓存命中率）并进了
// 上下文环那个「会话用量」弹层——那条弹层本来就是"这条会话用了多少"，两处分开看反而让人以为
// 是两个不同的东西；输入条的宽度压力（`.composer-inner` 只有 720px）也一并消失。
//
// 数据来自后端折叠**整段会话日志**的统计（chat.done / chat.history 携带）：压缩与翻页都
// 改不了它——那是"这条会话一共花了多少"，不是"此刻窗口里有多少"（后者是 ContextIndicator）。
//
// 三条展示纪律：
//   - 一步都没有（steps=0）→ **整行不渲染**，不显示一排 0；
//   - 每一项缺席就**不显示那一项**（工具时间 0 与"没有工具时间"必须分得开）；
//   - 数字格式只有一份实现（shared/session-stats.ts 的纯函数，组件与测试共用）。
import { usePopover } from "../../shared/popover";
import { IconGauge } from "../icons";
import { timeDialogRows, timePillLabel } from "../../shared/session-stats";
import type { SessionStats } from "../../shared/types";

export function StatsPills({ stats, placement = "up" }: { stats: SessionStats | null; placement?: "up" | "down" }) {
  // 一步都没有：整行不渲染（新会话/纯内存模式）——显示"0 轮 0 步"是个假事实。
  // 用量那一半已经并进上下文环的弹层，所以这里只认步数（只有 token 没有步数时不该留一个空胶囊）。
  if (!stats || stats.steps <= 0) return null;
  return (
    <div className="stats-pills" data-composer-stats>
      <TimePill stats={stats} placement={placement} />
    </div>
  );
}

/** 时间胶囊：轮/步/速度 + 弹层明细。 */
function TimePill({ stats, placement }: { stats: SessionStats; placement: "up" | "down" }) {
  const { open, toggle, rootRef } = usePopover();
  const label = timePillLabel(stats);
  const rows = timeDialogRows(stats);
  return (
    <span className="stats-anchor" ref={rootRef}>
      <button
        type="button"
        className={"stats-pill" + (open ? " on" : "")}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={`会话统计：${label}`}
        title={`会话统计：${label}`}
        onClick={toggle}
      >
        <IconGauge />
        <span className="stats-label">{label}</span>
      </button>
      {open && (
        <div className={"stats-pop" + (placement === "down" ? " down" : "")} role="dialog" aria-label="会话统计" data-pop>
          <div className="stats-pop-head">
            <span className="stats-pop-title">会话统计</span>
            <span className="stats-pop-sub">整段会话（压缩与翻页都改不了它）</span>
          </div>
          <dl className="stats-rows">
            {rows.map((r) => (
              <div className="stats-row" key={r.label}>
                <dt>{r.label}</dt>
                <dd>{r.value}</dd>
              </div>
            ))}
          </dl>
          {/* 一步都没有可测的计时（本功能上线前落库的消息没有这些字段）：如实说明，
              不留一个空面板——空面板看起来像"加载失败" */}
          {rows.length === 0 && (
            <div className="stats-empty">这条会话没有可用的计时数据（早期版本记录的消息不带这些字段）</div>
          )}
        </div>
      )}
    </span>
  );
}
