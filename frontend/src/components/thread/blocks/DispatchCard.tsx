// 主 Agent 调度子 Agent 的卡（agent_dispatch 的渲染形态——M3 语义的
// 前端部分）：Agent 身份头（色点 + 名 + 任务摘要）+ 状态（运行中 spinner/
// 已完成）+ 子执行过程（嵌套块缩进——子上下文隔离）+ 最终结果（验收）。
// 子块的事件流已按 dispatchId 归属到 subBlocks（store 的归属路由）。
//
// **回放路径的子过程要现读**：子 Agent 是**独立会话**（AGENTS.md §2.3）——它自己的
// messages 与压缩检查点在库里另存一份，父会话历史里没有这些明细。所以回放建出来的卡
// subBlocks 是空的（诚实的降级），展开时按 sessionId 懒加载一次子会话历史补进来。
import { useEffect, useRef, useState } from "react";
import type { ThreadBlock } from "../../../shared/store";
import { AGENT_COLORS, useAgents } from "../../../shared/agents";
import { useEnterRef } from "../../../shared/anim";
import { Markdown } from "../../../shared/markdown";
import { Button } from "../../form";
import { Block } from "./Block";
import { dispatchPrimaryAction, dispatchPrimaryTitle } from "./dispatch-primary";

/** agentId 也查不到时的名字（历史里参数坏了——比如 arguments 是截断的 JSON）。
 *  **不许留空白**：空白的卡头让用户不知道是谁在干活，比一个明确的"未知"更糟。 */
const UNKNOWN_AGENT = "未知 Agent";

export function DispatchCard({ block, onConfirm, onLoadChild, onOpenChild, "data-uid": dataUid }: {
  block: Extract<ThreadBlock, { kind: "dispatch" }>;
  onConfirm: (id: string, allow: boolean) => void;
  /** 「打开子会话」——把这张卡的子会话作为**独立工作区标签**打开（App 接线到
   *  focusChildTab）。子 Agent 是独立会话（AGENTS.md §2.3），卡内只显示摘要，
   *  完整时间线（含它自己的压缩检查点）在它自己的标签页里看。
   *  缺省（老调用方/测试）时不渲染这个按钮——不假装能打开。 */
  onOpenChild?: (sessionId: string) => void;
  /** 懒加载子会话历史的入口（App 接线：source.childHistory + historyBlocks）。
   *  返回子时间线的块；**失败必须 reject**（卡里显示原因并允许重试）。
   *  缺省（老调用方/测试）时不做懒加载——卡片保持原来的诚实降级。 */
  onLoadChild?: (sessionId: string) => Promise<ThreadBlock[]>;
  /** 块锚点（右侧大纲按 data-uid 精确寻址——不许按文本找元素）。 */
  "data-uid"?: number;
}) {
  // 子过程可折叠：运行中默认展开（盯着进度），完成后默认折叠（只留结果）。
  // 用派生 + 手动覆盖的写法，不能用 useState(初值)——useState 初值只在
  // 挂载时求值一次，卡从 running 变 done 不会跟随；又因线程是窗口化渲染
  // （Thread 的 visible 切片），滑出窗口再回来的卡会重挂并重新按 done
  // 初始化，于是卡与卡之间收缩状态不一致（"没有全部收缩"）。
  // userSet === null = 用户未干预，跟随状态自动；干预后以用户为准。
  const [userSet, setUserSet] = useState<boolean | null>(null);
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
  const expanded = userSet ?? !done;
  const cardRef = useEnterRef<HTMLDivElement>();
  const bodyRef = useRef<HTMLDivElement | null>(null);
  const sessionId = block.sessionId ?? "";

  // ---- 子会话历史的懒加载 ----
  // 只在**已完成**的卡上读：运行中的卡的子过程由实时事件流填（delta/toolCall 按
  // dispatchId 归属），此时去读历史会与实时块**重复叠加**，而且子会话还在写、读到的是
  // 半截历史。done 之后子会话的历史才是完整的。
  const needChildHistory = done && block.subBlocks.length === 0 && sessionId !== "";
  const [childBlocks, setChildBlocks] = useState<ThreadBlock[] | null>(null);
  const [childState, setChildState] = useState<"idle" | "loading" | "ready" | "error">("idle");
  const [childError, setChildError] = useState("");
  // 「已加载过 / 正在加载」的标记放 **ref**：state 变化会重渲染，靠 state 判重的话
  // 每次渲染都可能再发一次请求（展开动画期间的重渲染尤其密）。失败时复位它 ——
  // 失败不许被永久缓存，下次展开（或点「重试」）要能再试一次。
  const loadTriedRef = useRef(false);
  // 重试计数：展开态没变（用户没折叠）也能重新触发 effect。
  const [retryNonce, setRetryNonce] = useState(0);
  // 加载入口放 ref：App 重建回调身份（依赖变化）不该重新发请求，更不该与
  // 「失败复位标记」组合成死循环（effect 依赖它 + catch 里复位 → 无限重试）。
  const loadRef = useRef(onLoadChild);
  loadRef.current = onLoadChild;
  useEffect(() => {
    if (!expanded || !needChildHistory) return;
    if (loadTriedRef.current) return;
    const load = loadRef.current;
    if (!load) return; // 没接线的调用方（老测试）保持原样：不假装能读
    loadTriedRef.current = true;
    setChildState("loading");
    setChildError("");
    // alive：卸载/依赖变化后到达的结果不再写 state（切会话后迟到的应答不许改新卡的显示）
    let alive = true;
    load(sessionId).then(
      (blocks) => {
        if (!alive) return;
        setChildBlocks(blocks);
        setChildState("ready");
      },
      (e: unknown) => {
        if (!alive) return;
        // **失败必须说话**：静默降级会让用户以为"子会话本来就是空的"（那是编的结论）。
        // 复位标记 = 下次展开再试一次（失败不缓存）。
        loadTriedRef.current = false;
        setChildError(e instanceof Error ? e.message : String(e));
        setChildState("error");
      },
    );
    return () => { alive = false; };
  }, [expanded, needChildHistory, sessionId, retryNonce]);

  const retryChildHistory = () => {
    loadTriedRef.current = false;
    setRetryNonce((n) => n + 1);
  };

  // 渲染用的子块：实时归属的 subBlocks 优先（卡在跑时用户盯着它），懒加载到的历史
  // 只在 subBlocks 为空时补位（回放路径：父会话历史里没有子执行明细）。
  const subBlocks = block.subBlocks.length > 0 ? block.subBlocks : childBlocks ?? [];
  const subCount = subBlocks.length;
  // 可展开 = 有子过程，或有最终结果（结果也在折叠区内——用户报告
  // 「自动收缩和手动都收不掉最终结果」，根因是结果原本渲染在折叠区之外），
  // 或有子会话 id（展开才能触发懒加载——否则子会话永远读不出来）
  const expandable = subCount > 0 || Boolean(done && (block.result || block.sessionId));

  // ---- 卡头主区点下去该干什么（纯判定在 dispatch-primary.ts，有单测钉住）----
  // 用户实测报的「点击现在还是展开和收缩，并不是新标签页」：上一版把「打开子会话」放在
  // 折叠区底部——不展开看不见、展开了还得滚到底，**主操作不可发现**。这一版把主操作搬到
  // 卡头主区，展开降级成 chevron 小按钮（两个并列按钮，不嵌套——button 里套 button 是
  // 非法 HTML，浏览器会把内层提出来）。
  // canOpenChild 与 primaryAction 共用同一份判定（dispatchPrimaryAction），提示语也用
  // 同一份（dispatchPrimaryTitle）——两处各判一遍的后果是"提示说打开、点下去是展开"。
  const canOpenChild = sessionId !== "" && Boolean(onOpenChild);
  const primaryAction = dispatchPrimaryAction(block, Boolean(onOpenChild));
  const primaryTitle = dispatchPrimaryTitle(block, Boolean(onOpenChild));
  const toggleExpanded = () => setUserSet(!expanded);
  const onPrimaryClick = () => {
    // 有子会话 id + 接了 onOpenChild → 打开独立会话；否则**回落成切换展开**
    // （老数据/演示态点了没反应比"点开的是展开"更糟）。
    if (primaryAction === "open") onOpenChild?.(sessionId);
    else toggleExpanded();
  };

  return (
    <div className="dispatch-card" data-uid={dataUid} data-done={done ? "true" : undefined} data-error={block.isError ? "true" : undefined} ref={cardRef}>
      {/* 卡头 = 一行里两个**并列**按钮（不是嵌套：button 里套 button 是非法 HTML，
       *  浏览器会把内层提出来，React 也会警告）：
       *    .dispatch-head      主区：dot + 名 + 任务摘要 + 状态 + 子会话 id 徽标
       *                        —— 点它 = **打开子会话**（有 id 且接了 onOpenChild 时），
       *                        否则回落成切换展开（老数据/演示态不许点了没反应）
       *    .dispatch-chev-btn  chevron 小按钮：永远只做展开/收起
       *  两个都是真 <button>，Tab 可到、Enter/Space 可触发（键盘可达）。
       *  类名把 .dispatch-head 留给**主区按钮**而不是这一行的容器：脚本
       *  scripts/check-preview-layout.cjs 按 .dispatch-head 点卡头验证展开/收起
       *  （演示态的卡没有子会话 id，主区就是切换展开），沿用旧类名那个脚本一行都不用动。 */}
      <div className="dispatch-head-row">
        <button
          type="button"
          className="dispatch-head"
          data-openable={canOpenChild ? "true" : undefined}
          onClick={onPrimaryClick}
          // 打开子会话是**导航**不是展开，此时不报 aria-expanded（那会告诉读屏用户
          // "这里能展开/收起"）；只有回落成切换展开、且真的能展开时才报。
          aria-expanded={primaryAction === "toggle" && expandable ? expanded : undefined}
          aria-label={primaryTitle}
          title={primaryTitle}
        >
          <span className="dispatch-dot" style={{ background: agentColor }} aria-hidden />
          <span className="dispatch-agent">{agentName}</span>
          <span className="dispatch-task" title={block.task}>{clipTask(block.task)}</span>
          <span className="dispatch-state">
            {done ? (
              <>
                {block.isError ? "✗ 失败" : "✓ 完成"}
                {block.usageTokens ? <span className="dispatch-tokens mono">{block.usageTokens} tk</span> : null}
              </>
            ) : (
              <>
                <span className="mset-spinner" aria-hidden />
                执行中
              </>
            )}
          </span>
          {/* 子会话 id 徽标：**可打开时**加一个向外的箭头（↗）与成功色——收起状态下
           *  也让人一眼看出"点这里能进去"。不可打开（无 id / 未接线）时保持原来的
           *  中性徽标：不许长出一个点了没反应的箭头。 */}
          {sessionId !== "" && (
            <span
              className={"dispatch-session mono" + (canOpenChild ? " openable" : "")}
              title={`子会话 ${sessionId}（独立会话：自己的历史与压缩，可续跑）`}
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
        </button>
        {expandable && (
          <button
            type="button"
            className="dispatch-chev-btn"
            onClick={toggleExpanded}
            aria-expanded={expanded}
            aria-label={expanded ? "收起子过程" : "展开子过程"}
            title={expanded ? "收起子过程" : "展开子过程"}
          >
            <svg
              className={"dispatch-chev" + (expanded ? " open" : "")}
              width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden
            >
              <path d="m6 9 6 6 6-6" />
            </svg>
          </button>
        )}
      </div>

      {/* 折叠区：子执行过程 + 最终结果。结果必须在这里面——否则卡片永远收不短 */}
      {expanded && expandable && (
        <div className="dispatch-body" ref={bodyRef}>
          {/* 子会话历史的读取状态：三种都要说话（加载中/失败+重试/读到但是空的）。
           * 静默失败会让用户以为子会话本来就是空的——那是编的结论。 */}
          {childState === "loading" && <div className="dispatch-child-note">正在读取子会话…</div>}
          {childState === "error" && (
            <div className="dispatch-child-note error" role="alert">
              <span className="dispatch-child-msg">读不到子会话历史：{childError}</span>
              <Button className="dispatch-child-retry" onClick={retryChildHistory}>重试</Button>
            </div>
          )}
          {childState === "ready" && subCount === 0 && (
            <div className="dispatch-child-note">子会话没有可显示的历史（它可能还没开始执行，或历史已被清空）。</div>
          )}
          {subBlocks.map((sub) => (
            // **不把 onLoadChild 传进子时间线**：委派深度恒 1（AGENTS.md §8 两类制），
            // 子会话里再出现 dispatch 卡只可能是坏数据/老库——再传下去会递归读历史。
            <Block key={sub.uid} block={sub} onConfirm={onConfirm} />
          ))}
          {done && block.result && (
            <div className="dispatch-result">
              <Markdown text={block.result} />
            </div>
          )}
          {/* 底部两个按钮（**刻意保留**，与卡头主区形成两个入口——不是重复）：
           *   「打开子会话」——读完了想进去：长展开卡（子 Agent 的过程 + 结果）滚到底后
           *     不必再回到卡片顶部；用户明确要求过长展开在最下面也能收起来。
           *   「收起」——同上，滚到底就能收，不用回卡头找 chevron。
           *  两个入口对应两个真实场景：卡头主区是"我一看就想进去"（还没读，先跳进
           *  独立会话），底部是"我读完了想进去"（在卡内扫完摘要，顺手进完整时间线）。
           *  少任何一个都会让某一类用户多走一趟滚动或回顶部——所以刻意都留着。
           *  走表单套件的 Button——裸写 <button> 就是 OS 默认灰皮。 */}
          <div className="dispatch-foot">
            {/* 判据用 canOpenChild（与卡头主区**同一份**，见上）——这里再手写一遍
             *  sessionId !== "" && onOpenChild 的话，两处判据将来会漂移成"卡头能开、
             *  底部不显示"这种自相矛盾的卡。 */}
            {canOpenChild && (
              <Button className="dispatch-open-child" onClick={() => onOpenChild?.(sessionId)}>
                打开子会话
              </Button>
            )}
            <Button className="dispatch-collapse" onClick={toggleExpanded}>
              收起
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

function clipTask(task: string): string {
  const one = task.replace(/\n+/g, " ").trim();
  const r = [...one];
  if (r.length <= 60) return one;
  return r.slice(0, 60).join("") + "…";
}
