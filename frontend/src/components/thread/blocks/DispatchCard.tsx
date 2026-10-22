// 主 Agent 调度子 Agent 的卡（agent_dispatch 的渲染形态）：**一行摘要**。
//
// 形态（2026-09-30 用户拍板，两轮收敛后的定案）：dot + Agent 名 + 任务摘要 + 状态 +
// 用量 + 子会话 id（带 ↗）——**就这一行**。整行可点 = 进子会话（临时视图）。
//
// 为什么卡里不放子过程与结论：
//   ① 子 Agent 是**独立会话**（AGENTS.md §2.3），它自己的过程在它自己的临时视图里看，
//      而且是**实时的**（store 把带 dispatch_id 的子事件同时归约进子会话自己的 state）；
//   ② 卡里再画一份只可能是**滞后的摘要**，用户还得先展开、再滚到底才看得见——用户原话
//      「执行完了还是会展示整个子Agent会话展开」；
//   ③ 结论正文同样没必要（用户原话「打开子会话这个按钮太丑了，没必要」是同一个意思：
//      要看就点这一行进子会话，那里是完整的）。
//
// 所以这里**没有**折叠区、**没有**卡内子时间线、**没有**结果正文、**没有**底部按钮——
// 唯一的入口是这一行本身（可打开时是真 <button>，进不去时是静态行，不留点了没反应的按钮）。
//
// 例外是**待裁决的确认卡**：子 Agent 的确认由父会话代理（AGENTS.md §2.3），双投之后卡里
// 与子会话视图里各有一份——它不是过程展示，是必须点得到的操作入口，去掉会让没开子会话
// 视图的用户无从批准（会话卡在忙态）。
import type { ThreadBlock } from "../../../shared/store";
import { AGENT_COLORS, useAgents } from "../../../shared/agents";
import { useEnterRef } from "../../../shared/anim";
import type { AttachmentMeta } from "../../../shared/attachments";
import { Block } from "./Block";
import { AttachmentCards } from "./AttachmentCard";
import { dispatchErrorTitle, dispatchPrimaryAction, dispatchPrimaryTitle } from "./dispatch-primary";

/** agentId 也查不到时的名字（历史里参数坏了——比如 arguments 是截断的 JSON）。
 *  **不许留空白**：空白的卡头让用户不知道是谁在干活，比一个明确的"未知"更糟。 */
const UNKNOWN_AGENT = "未知 Agent";

export function DispatchCard({ block, onConfirm, onAnswer, onOpenChild, replayed, "data-uid": dataUid }: {
  block: Extract<ThreadBlock, { kind: "dispatch" }>;
  /** 回放帧（换会话）：不播入场动效（见 useEnterRef 的 skip）。 */
  replayed?: boolean;
  onConfirm: (id: string, allow: boolean) => void;
  /** ask_user 提问的回答（卡内子时间线的 ask 确认卡）——透传给 Block。 */
  onAnswer?: (id: string, text: string) => void;
  /** 进子会话——把这张卡的子会话作为**临时视图**打开（App 接线到 focusChildView；
   *  2026-12 拍板：不进标签栏，页头「返回主会话」关闭视图）。
   *  子 Agent 是独立会话（AGENTS.md §2.3），它的实时过程与完整时间线（含它自己的压缩
   *  检查点）都在它自己的视图里。缺省（老调用方/测试）时整行渲染成静态行——不假装能打开。 */
  onOpenChild?: (sessionId: string) => void;
  /** 块锚点（右侧大纲按 data-uid 精确寻址——不许按文本找元素）。 */
  "data-uid"?: number;
}) {
  // Agent 身份（名字 + 颜色）的回落：实时路径由 chat.dispatchStart 直接带
  //（事件里有 agent_name/agent_color），**回放路径没有**——历史里只有
  // agent_dispatch 的 arguments（{agent, task}），名字与颜色要 Agent 注册表，
  // 而纯函数（history.ts）里没有注册表。所以在这里按 agentId 查：
  //   注册表里有 → 用注册表的名字与颜色（改过名/换过色也能对上）；
  //   查不到 → 显示 agentId 本身（它至少能对上 Agent 名单）；
  //   agentId 也是空（参数坏了）→ 明确的"未知 Agent"（不许空白）。
  // 颜色同理回落：空 background 会让色点整个消失（老历史里 agentColor 是 ""）。
  // 注册表是**上下文**（不是 props）：AgentsProvider 的值变了，memo 拦不住
  // 上下文更新——卡片会跟着重新解析，不会停在旧名字上。
  const { agents } = useAgents();
  const known = agents.find((a) => a.id === block.agentId);
  const agentName = block.agentName || known?.name || block.agentId || UNKNOWN_AGENT;
  const agentColor = block.agentColor || known?.color || AGENT_COLORS[0];
  const done = block.status === "done";
  const cardRef = useEnterRef<HTMLDivElement>(undefined, undefined, replayed);
  const sessionId = block.sessionId ?? "";

  // todo 契约批②：派发卡上的进度徽标（不破「卡 = 一行摘要」契约——徽标是计数，
  // 不是内容，不展开卡内子时间线）。running 时随带 dispatch_id 的 todo.updated
  // 实时更新；done 时定格为 FinalTodos（派发结束即归档）。
  const todos = block.todos ?? [];
  const todoDone = todos.filter((it) => it.status === "done").length;
  const todoTotal = todos.length;
  const todoFocus = todos.find((it) => it.status === "active")?.content;
  const showTodoBadge = todoTotal > 0;
  // 未收敛（done 卡上仍有 active/pending 项）：如实呈现，不假装完成——hover
  // 点名残留项（用户能感知「清单没收敛」而不是以为界面没刷新）。清单是否收敛
  // 归子会话如实报告，卡片只放大这个事实，绝不代它把最后一项标成完成。
  const todoUnfinished = todos.filter((it) => it.status !== "done");
  const todoBadgeTitle = todoUnfinished.length > 0
    ? `子会话任务清单：完成 ${todoDone}/${todoTotal}${todoFocus ? `，进行中：${todoFocus}` : ""}——清单未收敛：${todoUnfinished.map((it) => it.content).join("、")}`
    : `子会话任务清单：完成 ${todoDone}/${todoTotal}${todoFocus ? `，进行中：${todoFocus}` : ""}`;

  // ---- 整行点下去该干什么（纯判定在 dispatch-primary.ts，有单测钉住）----
  // canOpenChild / primaryTitle 与 primaryAction 共用同一份判定——两处各判一遍的后果是
  // "提示说能进、点下去什么都没发生"。
  const canOpenChild = dispatchPrimaryAction(block, Boolean(onOpenChild)) === "open";
  const primaryTitle = dispatchPrimaryTitle(block, Boolean(onOpenChild));
  // 失败卡（2026-10-09：用户原话「失败的子代理，鼠标移入要能展示错误原因或者点击打开」）：
  // 错误原因一直在 block.result 里（dispatchEnd 的 result 装 isError 时的错误文本），
  // 卡片从不渲染——hover 与 aria 都改成它。成功/运行中的卡维持「打开子会话」提示不变。
  // 拿不到原因（老数据 result 空白）→ 回落默认提示，不许出现「失败：」后面空空如也。
  const errorReason = done && block.isError ? dispatchErrorTitle(block.result) : null;
  const headTitle = errorReason ? `失败：${errorReason}` : primaryTitle;
  const headAria = errorReason ? `子代理失败：${errorReason}` : primaryTitle;
  // 待裁决的确认卡（子 Agent 的确认由父会话代理，AGENTS.md §2.3）：双投之后它同时
  // 落在卡里与子会话自己的时间线里（store 的两处同时定格），所以这里渲染的是
  // **操作入口**而不是过程展示——用户没打开子会话视图也必须能裁决，否则会话卡在忙态。
  const confirms = block.subBlocks.filter((b) => b.kind === "confirm" && !b.resolved);

  // 整行内容（两种形态共用同一份）：能进子会话时它是 <button>，进不去时是静态行。
  // 为什么不留一个永远可点的按钮：点了没反应的按钮比"没有这个入口"更糟——用户会
  // 反复点、以为界面坏了。进不去时 title 如实说明原因（老数据没记下子会话 id）。
  const row = (
    <>
      <span className="dispatch-dot" style={{ background: agentColor }} aria-hidden />
      <span className="dispatch-agent">{agentName}</span>
      <span className="dispatch-task" title={block.task}>{clipTask(block.task)}</span>
      {showTodoBadge && (
        <span
          className="dispatch-todos mono"
          title={todoBadgeTitle}
        >
          ✓ {todoDone}/{todoTotal}
          {todoFocus && <span className="dispatch-todos-focus">· {clipTask(todoFocus)}</span>}
        </span>
      )}
      <span className="dispatch-state">
        {done ? (
          <>
            {block.isError ? "✗ 失败" : "✓ 完成"}
            {/* 「错误」小标识：失败原因全文进了 hover title（headTitle），这里给一个
             *  **看得见**的入口——点它也打开子会话（错误上下文在子会话里），与整行
             *  点击同一个动作。不可打开（无 id / 未接线）时不渲染：不许长出点了没
             *  反应的标识。span 而不是 <button>：它在卡头 <button> 内部，HTML 不允许
             *  按钮嵌按钮——用 role="button" + 键盘处理补齐语义，stopPropagation
             *  防止触发外层卡头的同一次打开。 */}
            {block.isError && canOpenChild && sessionId !== "" && (
              <span
                className="dispatch-error-chip"
                role="button"
                tabIndex={0}
                title="错误原因（点击打开子会话查看完整上下文）"
                aria-label="错误：打开子会话查看原因"
                onClick={(e) => {
                  e.stopPropagation();
                  onOpenChild?.(sessionId);
                }}
                onKeyDown={(e) => {
                  if (e.key !== "Enter" && e.key !== " ") return;
                  e.preventDefault();
                  e.stopPropagation();
                  onOpenChild?.(sessionId);
                }}
              >
                错误
              </span>
            )}
            {block.usageTokens ? <span className="dispatch-tokens mono">{block.usageTokens} tk</span> : null}
          </>
        ) : (
          <>
            <span className="mset-spinner" aria-hidden />
            执行中
          </>
        )}
      </span>
      {/* 子会话 id 徽标：**可打开时**加一个向外的箭头（↗）——一眼看出"点这里能进去"。
       *  不可打开（无 id / 未接线）时保持中性徽标：不许长出一个点了没反应的箭头。 */}
      {sessionId !== "" && (
        <span
          className={"dispatch-session mono" + (canOpenChild ? " openable" : "")}
          title={`子会话 ${sessionId}（独立会话：自己的历史与压缩，实时过程在它自己的标签页里）`}
        >
          {sessionId.slice(0, 8)}
          {canOpenChild && (
            <svg
              className="dispatch-session-go" width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden
            >
              <path d="M7 17 17 7" />
              <path d="M8 7h9v9" />
            </svg>
          )}
        </span>
      )}
    </>
  );

  return (
    <div className="dispatch-card" data-uid={dataUid} data-done={done ? "true" : undefined} data-error={block.isError ? "true" : undefined} ref={cardRef}>
      {canOpenChild ? (
        <button
          type="button"
          className="dispatch-head"
          data-openable="true"
          onClick={() => onOpenChild?.(sessionId)}
          // 进子会话是**导航**不是展开：这里不报 aria-expanded（那会告诉读屏
          // 用户"这里能展开/收起"）。
          aria-label={headAria}
          title={headTitle}
        >
          {row}
        </button>
      ) : (
        <div className="dispatch-head" title={headTitle} aria-label={headAria}>{row}</div>
      )}

      {/* 待裁决的确认：子 Agent 的确认由父会话代理（AGENTS.md §2.3），双投之后
       *  卡里与子会话视图里各有一份——这里渲染的是**操作入口**（不是过程展示），
       *  去掉它会让没打开子会话视图的用户无从批准（会话卡在忙态）。 */}
      {confirms.map((c) => (
        <Block key={c.uid} block={c} onConfirm={onConfirm} onAnswer={onAnswer} />
      ))}

      {/* 这次派发共享到对话的附件（子会话 share_file 等的产物，随调度结果回流）：
       *  卡 = 一行摘要的**例外**——附件不是子过程，是成果。渲染成**独立的成果区**
       *  （发丝分隔线 + 「成果文件」小标题 + 附件卡网格），不与卡头拼在一坨；
       *  用户点开卡就能拿到文件，不必进子会话视图再翻。实时（toolResult 回流）
       *  与回放（调度结果消息上的同一份元数据）两条路径共用 AttachmentCards。 */}
      <DispatchDeliverables atts={block.attachments} />
    </div>
  );
}

/** 派发卡的成果区：附件非空才渲染。发丝线与卡头分开 + 小标题（agent-console-v3
 *  的区块标签语言：小号、faint、字距微开）+ 附件卡网格。 */
function DispatchDeliverables({ atts }: { atts?: AttachmentMeta[] }) {
  if (!atts?.length) return null;
  return (
    <div className="dispatch-deliverables" role="region" aria-label="成果文件">
      <span className="dispatch-deliverables-label">成果文件</span>
      <AttachmentCards atts={atts} />
    </div>
  );
}

function clipTask(task: string): string {
  const one = task.replace(/\n+/g, " ").trim();
  const r = [...one];
  if (r.length <= 60) return one;
  return r.slice(0, 60).join("") + "…";
}
