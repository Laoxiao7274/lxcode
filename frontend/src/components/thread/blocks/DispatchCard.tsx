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

/** agentId 也查不到时的名字（历史里参数坏了——比如 arguments 是截断的 JSON）。
 *  **不许留空白**：空白的卡头让用户不知道是谁在干活，比一个明确的"未知"更糟。 */
const UNKNOWN_AGENT = "未知 Agent";

export function DispatchCard({ block, onConfirm, onLoadChild, "data-uid": dataUid }: {
  block: Extract<ThreadBlock, { kind: "dispatch" }>;
  onConfirm: (id: string, allow: boolean) => void;
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

  return (
    <div className="dispatch-card" data-uid={dataUid} data-done={done ? "true" : undefined} data-error={block.isError ? "true" : undefined} ref={cardRef}>
      <button
        type="button"
        className="dispatch-head"
        onClick={() => setUserSet(!expanded)}
        aria-expanded={expanded}
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
        {block.sessionId && (
          <span className="dispatch-session mono" title={`子会话 ${block.sessionId}（独立会话：自己的历史与压缩，可续跑）`}>
            {block.sessionId.slice(0, 8)}
          </span>
        )}
        {expandable && (
          <svg
            className={"dispatch-chev" + (expanded ? " open" : "")}
            width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden
          >
            <path d="m6 9 6 6 6-6" />
          </svg>
        )}
      </button>

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
          {/* 底部收起：长展开卡（子 Agent 的过程 + 结果）滚到底后不必再回到卡片顶部
           *  找那个 chevron。走表单套件的 Button——裸写 <button> 就是 OS 默认灰皮。 */}
          <div className="dispatch-foot">
            <Button className="dispatch-collapse" onClick={() => setUserSet(!expanded)}>
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
