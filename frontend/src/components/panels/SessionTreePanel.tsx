// 右侧「会话树」面板（docs/session-tree.md §D）：**只展示用户消息 + 纯导航**
// （2026-12 用户拍板）。
//
// 数据源 = `chat.tree`（后端按 seq 升序给的 user 骨架，前端按 parent_seq 建树，见
// ./tree-model 的纯函数层）。**老后端没有 chat.tree 时由 App 回落 TurnPanel**
// （tree === null 的分支在 App 里，不在本组件——组件只管画树，不管降级）。
//
// 交互（user-only 改版）：
//   - **单击 = 跳转**（滚动到该消息；在别的分支上则先切过去再跳——判定与切叶都在
//     App 的 handleTreeJump 里，本组件只上报点击）；
//   - off_path 行灰化显示（**不隐藏**——用户要能看出有别的分支，行 title 说明
//     「在分支上」，单击先切过去再跳）；
//   - 行上没有操作按钮：切分支的唯一入口是单击行（纯导航，行尾按钮与过滤档已删）。
//
// 组件不碰 DOM 里的业务（滚动/扩窗/高亮都在 Thread 里），也不发请求（数据与动作都
// 由 App 经 props 传进来——面板是纯展示）。
import { useEffect, useRef } from "react";
import {
  filesBadge,
  type TreeRow,
} from "./tree-model";

export function SessionTreePanel({
  rows,
  activeSeq,
  onJump,
  onLoadAll,
  loadedAll,
  truncation,
  loading,
  error,
}: {
  rows: TreeRow[];
  /** 当前高亮行的 seq（树行按 seq 判高亮；块 uid 与 seq 是两个空间，不许混）。
   *  null = 还没有可高亮的行。 */
  activeSeq: number | null;
  /** 单击一行：跳转（在别的分支上由 App 先切过去再跳）。 */
  onJump: (row: TreeRow) => void;
  /** 「加载全部」（truncated 时的出口）。 */
  onLoadAll: () => void;
  /** 已经点过「加载全部」：此时后端已按它的上限（2000）返回，还有 truncated 就说明
   *  超出上限——**不再给按钮**（点了也不会有变化，那是"点了没反应"）。 */
  loadedAll: boolean;
  /** 截断提示（truncationNote 的产物；null = 没截断）。 */
  truncation: { count: number; text: string } | null;
  loading: boolean;
  /** 拉树失败的原因（老后端没有 chat.tree 时 App 已回落线性视图，这里只剩别的失败）。 */
  error: string | null;
}) {
  const listRef = useRef<HTMLDivElement>(null);

  // 滚动跟随：**只在当前行变化时**把它滚进可见区（block:"nearest"——用户自己滚动
  // 时间线时当前行会一路变化，center 会把面板每变一次就重新居中一次）。
  useEffect(() => {
    if (activeSeq === null) return;
    const el = listRef.current?.querySelector(`[data-seq="${activeSeq}"]`);
    el?.scrollIntoView({ block: "nearest" });
  }, [activeSeq]);

  if (rows.length === 0) {
    // 空态分两种，**不许混成一句**：会话没有消息 ≠ 正在加载。
    const empty = loading ? "正在读取会话树…" : error ?? "还没有发过消息";
    return (
      <div className="stree-panel">
        <div className="turn-empty">{empty}</div>
      </div>
    );
  }

  return (
    <div className="stree-panel">
      {error && <div className="stree-note stree-note-warn">{error}</div>}
      <div className="stree-list" ref={listRef}>
        {/* 截断提示（**不静默截断**）：显示"更早的 N 条消息未加载" + 加载全部。
            放在列表顶部（时间顺序里"更早"就在上面），与节点顺序一致。 */}
        {truncation && (
          <div className="stree-note">
            <span>
              {truncation.text}
              {/* 点过「加载全部」还有截断 = 超出后端的单次上限：如实说明，不再给按钮 */}
              {loadedAll && "（超出单次上限）"}
            </span>
            {!loadedAll && (
              <button type="button" className="stree-loadall" onClick={onLoadAll} disabled={loading}>
                {loading ? "加载中…" : "加载全部"}
              </button>
            )}
          </div>
        )}
        {rows.map((row) => (
          <TreeNodeRow key={row.seq} row={row} active={activeSeq === row.seq} onJump={onJump} />
        ))}
      </div>
    </div>
  );
}

/** 一行。单击 = 跳转（可键盘可达）；没有行尾按钮、没有折叠、没有缩进。
 *
 *  **平级列表（2026-12 用户拍板）**：不做缩进层级（无深度变量/连接线/分支圆点/钳制
 *  错位）——所有行按 seq 平铺、全宽，一百条消息线性可滚。分叉信息只靠 off_path 的
 *  灰化 + 行 title（「在分支上」）表达，纯导航无需表达父子。
 *  减负后的行内容：轮次号 = 轻量等宽序号；徽标只留「你在这里/文件 N/orphan」。 */
function TreeNodeRow({ row, active, onJump }: {
  row: TreeRow;
  active: boolean;
  onJump: (row: TreeRow) => void;
}) {
  const files = filesBadge(row);

  return (
    <div
      className="stree-row-wrap"
      data-off-path={row.onPath ? "false" : "true"}
      data-leaf={row.isLeaf ? "true" : "false"}
      data-active={active ? "true" : "false"}
      data-kind="user"
      data-seq={row.seq}
    >
      <button
        type="button"
        className="stree-row"
        // 悬停显示完整摘要（列表里是截断后的单行）；off_path 行 title 说明在分支上
        title={row.onPath
          ? `第 ${row.turn ?? "?"} 轮：${row.label}`
          : `在分支上 · 第 ${row.turn ?? "?"} 轮：${row.label}`}
        onClick={() => onJump(row)}
      >
        <span className="stree-num">{row.turn ?? "·"}</span>
        <span className="stree-label">{row.label || "（空消息）"}</span>
        {/* 徽标全部 omitempty 语义：0 不渲染（**不显示「0 文件」**）。 */}
        {files && <span className="stree-files" title="这一轮可还原的文件改动（含子 Agent 的）">{files}</span>}
        {row.isLeaf && <span className="stree-here">你在这里</span>}
        {row.orphan && <span className="stree-orphan" title="这条记录的父节点缺失（历史数据不完整），仍按原样显示">⚠</span>}
      </button>
    </div>
  );
}
