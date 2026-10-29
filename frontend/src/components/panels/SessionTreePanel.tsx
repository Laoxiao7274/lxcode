// 右侧「会话树」面板（docs/session-tree.md §D）：把轮次面板升级成树形视图。
//
// 数据源 = `chat.tree`（后端按 seq 升序给的骨架，前端自己按 parent_seq 建树，见
// ./tree-model 的纯函数层）。**老后端没有 chat.tree 时由 App 回落 TurnPanel**
// （tree === null 的分支在 App 里，不在本组件——组件只管画树，不管降级）。
//
// 交互（§D.5，用户明确要求）：
//   - **单击 = 跳转**（滚动到该消息；在别的分支上则先切过去再跳）；
//   - **行尾按钮 = 切到此**（显式、可 Tab、可触摸；**否决双击**——不可发现、触摸端
//     冲突、键盘不可达）；
//   - 废弃分支（on_path=false）灰化但**不隐藏**，首节点常显；
//   - 过滤三档：全部 / 只看用户消息 / 只看有改动。
//
// 组件不碰 DOM（滚动/扩窗/高亮都在 Thread 里，与 TurnPanel 同一分工），也不发请求
// （数据与动作都由 App 经 props 传进来——面板是纯展示）。
import { useEffect, useRef, type CSSProperties } from "react";
import { Button } from "../form";
import {
  branchBadge,
  depthClamped,
  externalBadge,
  filesBadge,
  truncationNote,
  visualDepth,
  type TreeFilter,
  type TreeRow,
} from "./tree-model";

const FILTERS: { value: TreeFilter; label: string; desc: string }[] = [
  { value: "all", label: "全部", desc: "所有节点（含废弃分支，灰化显示）" },
  { value: "user", label: "只看用户消息", desc: "只留你说过的话 + 分支骨架" },
  { value: "changed", label: "只看有改动", desc: "只留有文件改动或无法还原的操作的轮" },
];

export function SessionTreePanel({
  rows,
  filter,
  onFilter,
  activeSeq,
  onJump,
  onToggle,
  onSetLeaf,
  onBranchFrom,
  onLoadAll,
  loadedAll,
  onLatest,
  canLatest,
  truncation,
  loading,
  error,
  busy,
}: {
  rows: TreeRow[];
  filter: TreeFilter;
  onFilter: (filter: TreeFilter) => void;
  /** 当前高亮行的 seq（树行按 seq 判高亮；块 uid 与 seq 是两个空间，不许混）。
   *  null = 还没有可高亮的行。 */
  activeSeq: number | null;
  /** 单击一行：跳转（在别的分支上由 App 先切过去再跳）。 */
  onJump: (row: TreeRow) => void;
  /** 折叠/展开（分支行）。 */
  onToggle: (row: TreeRow) => void;
  /** 行尾「切到此」：把 leaf 移到该行（`files` = 同时还原文件）。 */
  onSetLeaf: (row: TreeRow, files: boolean) => void;
  /** 行尾「从此分支」：把该 user 节点自己移出路径 + 原文回填输入框（= 旧的
   *  「编辑后重发」链路）。**只对当前路径上的 user 行给**：后端 rewind 对不在
   *  当前路径上的 seq 是幂等 no-op（返回 removed=0），做成按钮就是"点了没反应"。 */
  onBranchFrom: (row: TreeRow) => void;
  /** 「加载全部」（truncated 时的出口）。 */
  onLoadAll: () => void;
  /** 已经点过「加载全部」：此时后端已按它的上限（2000）返回，还有 truncated 就说明
   *  超出上限——**不再给按钮**（点了也不会有变化，那是"点了没反应"）。 */
  loadedAll: boolean;
  /** 「回到最新」：落点 = 树里最大的 seq（**绝不传 0**——0 是空路径）。 */
  onLatest: () => void;
  /** 当前已经在最新处 → 按钮禁用（点了没有变化，不做成点了没反应）。 */
  canLatest: boolean;
  /** 截断提示（truncationNote 的产物；null = 没截断）。 */
  truncation: { count: number; text: string } | null;
  loading: boolean;
  /** 拉树失败的原因（老后端没有 chat.tree 时 App 已回落线性视图，这里只剩别的失败）。 */
  error: string | null;
  /** 会话正在生成：切叶会被后端 ErrBusy 拒绝 → 按钮禁用并说明原因。 */
  busy: boolean;

}) {
  const listRef = useRef<HTMLDivElement>(null);

  // 滚动跟随：**只在当前行变化时**把它滚进可见区（与 TurnPanel 同款：block:"nearest"
  // 而不是 center——用户自己滚动时间线时当前行会一路变化，center 会把面板每变一次
  // 就重新居中一次）。
  useEffect(() => {
    if (activeSeq === null) return;
    const el = listRef.current?.querySelector(`[data-seq="${activeSeq}"]`);
    el?.scrollIntoView({ block: "nearest" });
  }, [activeSeq]);

  if (rows.length === 0) {
    // 空态分三种，**不许混成一句**：过滤无命中 ≠ 会话没有消息 ≠ 正在加载。
    // 「还没有发过消息」挂在过滤态上是假事实（用户明明有历史，只是这一档没有命中）。
    const empty = loading
      ? "正在读取会话树…"
      : error ?? (filter !== "all" ? "这一档没有匹配的节点" : "还没有发过消息");
    return (
      <div className="stree-panel">
        <div className="turn-empty">{empty}</div>
      </div>
    );
  }

  return (
    <div className="stree-panel">
      {/* 过滤三档 + 分支汇总。chips 常显（不是 hover 才出现）——过滤是主操作，
          藏起来用户不知道有它。 */}
      <div className="stree-toolbar">
        <div className="stree-chips" role="group" aria-label="过滤会话树">
          {FILTERS.map((f) => (
            <button
              key={f.value}
              type="button"
              className={"stree-chip" + (filter === f.value ? " on" : "")}
              aria-pressed={filter === f.value}
              title={f.desc}
              onClick={() => onFilter(f.value)}
            >
              {f.label}
            </button>
          ))}
        </div>
        {/* 「回到最新」：落点 = 树里最大的 seq（不是 0——0 是空路径，会把用户送到
            第一条消息之前）。已经在最新处时禁用并说明。 */}
        <Button
          className="stree-latest"
          disabled={!canLatest || busy || loading}
          title={busy ? "生成中不能切换分支（先停止）"
            : canLatest ? "切到这条会话最新的位置" : "已经在最新位置"}
          onClick={onLatest}
        >
          ⤓ 回到最新
        </Button>
      </div>
      {error && <div className="stree-note stree-note-warn">{error}</div>}
      <div className="stree-list" ref={listRef}>
        {/* 截断提示（**不静默截断**）：显示"更早的 N 个节点未加载" + 加载全部。
            放在列表顶部（时间顺序里"更早"就在上面），与节点顺序一致。 */}
        {truncation && (
          <div className="stree-note">
            <span>
              {truncation.text}
              {/* 点过「加载全部」还有截断 = 超出后端的单次上限：如实说明，不再给按钮 */}
              {loadedAll && "（超出单次上限）"}
            </span>
            {!loadedAll && (
              <Button className="stree-loadall" onClick={onLoadAll} disabled={loading}>
                {loading ? "加载中…" : "加载全部"}
              </Button>
            )}
          </div>
        )}
        {rows.map((row) => (
          <TreeNodeRow
            key={row.seq}
            row={row}
            active={activeSeq === row.seq}
            busy={busy}
            onJump={onJump}
            onToggle={onToggle}
            onSetLeaf={onSetLeaf}
            onBranchFrom={onBranchFrom}
          />
        ))}
      </div>
    </div>
  );
}

/** 一行。可点的行做成 <Button>（键盘可达），不可点的做成 <div>（点它没有目标时
 *  做成按钮就是骗键盘用户按一次回车什么都没发生——与 TurnPanel 的「会话开始」行同款）。 */
function TreeNodeRow({ row, active, busy, onJump, onToggle, onSetLeaf, onBranchFrom }: {
  row: TreeRow;
  active: boolean;
  busy: boolean;
  onJump: (row: TreeRow) => void;
  onToggle: (row: TreeRow) => void;
  onSetLeaf: (row: TreeRow, files: boolean) => void;
  onBranchFrom: (row: TreeRow) => void;
}) {
  const files = filesBadge(row);
  const external = externalBadge(row);
  const branch = branchBadge(row);
  // 可点判据：有 user 祖先（跳转锚点只存在于 user 块上）且不是过滤骨架。
  const clickable = row.userSeq > 0 && !row.skeleton;
  // 已在路径上且就是当前叶 → 「切到此」无视觉变化，禁用并说明（不做成点了没反应）。
  const atLeaf = row.isLeaf;
  // 缩进/连接线只认**视觉深度**（钳制后的值）：depth 照旧承载成行祖先数（数据语义），
  // 但超过 STREE_MAX_VISUAL_DEPTH 之后不再线性外推——否则单链会话几十轮之后标签会被
  // 挤出面板（详见 tree-model.ts 的 STREE_MAX_VISUAL_DEPTH）。
  const vDepth = visualDepth(row.depth);
  const clamped = depthClamped(row.depth);
  const indent = { "--stree-depth": String(vDepth) } as CSSProperties;

  const body = (
    <>
      {/* 折叠箭头（分支行）：▸ / ▾。on_path 与 off_path 的默认态相反，但视觉一致——
          用户不需要知道"这两个集合的默认值不同"（那是实现的细节）。 */}
      {row.collapsible ? (
        <span
          className="stree-caret"
          role="button"
          tabIndex={0}
          aria-label={row.collapsed ? "展开分支" : "折叠分支"}
          aria-expanded={!row.collapsed}
          onClick={(e) => {
            // 折叠与跳转是两个动作：点箭头不许同时触发跳转（否则想折一下却跳走了）
            e.stopPropagation();
            onToggle(row);
          }}
          onKeyDown={(e) => {
            if (e.key !== "Enter" && e.key !== " ") return;
            e.preventDefault();
            e.stopPropagation();
            onToggle(row);
          }}
        >
          {row.collapsed ? "▸" : "▾"}
        </span>
      ) : (
        <span className="stree-caret stree-caret-empty" aria-hidden />
      )}
      {row.turn !== undefined ? (
        <span className="stree-badge">{row.turn}</span>
      ) : (
        <span className="stree-badge stree-badge-mark" aria-hidden>
          {row.kind === "checkpoint" ? "▣" : "·"}
        </span>
      )}
      <span className="stree-label" title={row.label}>
        {/* 缩进被钳制（depth > STREE_MAX_VISUAL_DEPTH）：给一个轻量记号，说明这条链
            还在往深处走——**不静默压平**（压平之后深层行看起来像兄弟，看不出层级）。
            记号放在标签内联开头：不占一个 flex 槽位，不额外挤压标签宽度。 */}
        {clamped && (
          <span className="stree-deep" title="缩进已到上限：这条链还有更深的层级">⋯</span>
        )}
        {row.label || "（空消息）"}
      </span>
      {/* 徽标全部 omitempty 语义：0 不渲染（**不显示「0 文件」**）。 */}
      {branch && <span className="stree-branch-badge" title="这个节点有多个后继分支">{branch}</span>}
      {files && <span className="stree-files" title="这一轮可还原的文件改动（含子 Agent 的）">{files}</span>}
      {external && (
        <span className="stree-external" title="这些改动（bash / MCP / 自定义工具）**无法还原**——回滚后文件会保持现状">
          {external}
        </span>
      )}
      {row.collapsed && row.hidden > 0 && <span className="stree-hidden">▸ 还有 {row.hidden} 个</span>}
      {atLeaf && <span className="stree-here">你在这里</span>}
      {row.orphan && <span className="stree-orphan" title="这条记录的父节点缺失（历史数据不完整），仍按原样显示">⚠</span>}
    </>
  );

  return (
    <div
      className="stree-row-wrap"
      data-depth={row.depth}
      data-vdepth={vDepth}
      data-clamped={clamped ? "true" : "false"}
      data-last={row.last ? "true" : "false"}
      data-off-path={row.onPath ? "false" : "true"}
      data-leaf={atLeaf ? "true" : "false"}
      data-active={active ? "true" : "false"}
      data-skeleton={row.skeleton ? "true" : "false"}
      data-kind={row.kind}
      data-seq={row.seq}
      style={indent}
    >
      {clickable ? (
        <Button
          className="stree-row"
          // 悬停显示完整摘要（列表里是截断后的单行）
          title={row.onPath
            ? `跳到这一轮（第 ${row.turn ?? "?"} 轮）`
            : "这条在另一条分支上——点击会先切过去再跳"}
          onClick={() => onJump(row)}
        >
          {body}
        </Button>
      ) : (
        <div className="stree-row stree-row-static" aria-disabled>
          {body}
        </div>
      )}
      {/* 行尾操作：hover / focus 时出现（触摸端常驻，见 CSS）。显式按钮而不是双击：
          可发现、可 Tab、可触摸。 */}
      {clickable && (
        <div className="stree-actions">
          <Button
            className="stree-action"
            disabled={atLeaf || busy}
            title={atLeaf
              ? "已在当前路径上"
              : busy ? "生成中不能切换分支（先停止）" : "从这里接着往下长（这条留在对话里）"}
            onClick={() => onSetLeaf(row, false)}
          >
            ⟲ 切到此
          </Button>
          {/* 「⟲+文件」只在**这一行之后真有的可还原**时出现：
              ① 没有可还原的改动时点它只会多发一次请求（后端 plan 收敛成空操作），却让
                 用户以为文件被动过；
              ② 后端没有文件快照能力（批次 3 未上线）时 rollbackFiles 恒 0 → 按钮**藏起来**
                 而不是灰掉（藏起来 = "这个版本没有这个能力"，灰掉 = "你有但用不了"，
                 后者会让人以为坏了——docs §F 的依赖关系）。 */}
          {row.rollbackFiles > 0 && (
            <Button
              className="stree-action stree-action-files"
              disabled={busy}
              title={`切到这里，并还原之后的 ${row.rollbackFiles} 个文件改动` +
                (row.rollbackChildFiles > 0 ? `（含 ${row.rollbackChildFiles} 个子 Agent 的）` : "") +
                "——有冲突则整批不执行"}
              onClick={() => onSetLeaf(row, true)}
            >
              ⟲+文件
            </Button>
          )}
          {/* 「从此分支」= 这条消息自己离开路径 + 原文回填输入框（重写重发）。
              只对**当前路径上的 user 行**给：废弃分支上的 seq 对 rewind 是幂等 no-op
              （后端按当前路径判定），做成按钮就是"点了没反应"——先「切到此」再来。 */}
          {row.kind === "user" && row.onPath && (
            <Button
              className="stree-action"
              disabled={busy}
              title={busy ? "生成中不能切换分支（先停止）"
                : "从这条重新开始：这条及其之后移出当前对话（不删除），原文回到输入框"}
              onClick={() => onBranchFrom(row)}
            >
              ↳ 从此分支
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
