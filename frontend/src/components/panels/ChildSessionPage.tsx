// 子会话标签页：把一张 dispatch 卡上的子会话作为**独立工作区标签**打开的完整视图。
//
// 子 Agent = 独立会话（AGENTS.md §2.3）：它有自己的 messages、自己的压缩检查点。
// 这一页读的就是**这个子会话在 store 里的 state**（`sessionStates[childId]`）——
// 与主时间线共用同一份归约：历史由 store 的 historyLoaded 重建，之后的实时事件
//（store 的**双投**：带 dispatch_id 的事件同时归约进子会话自己的 state）直接续上。
//
// **为什么不自己读一次历史**（2026-09-30 改）：那样只能看到打开那一刻的快照，
// 子 Agent 之后继续思考、继续跑工具都不会出现在这一页——用户报的正是这个：
// 「我点开之后他里面就没有接着思考」。历史由 store 装载（source.childHistory 发
// historyLoaded，sessionId 是子会话自己的），这一页只负责画。
//
// 只读，但**版式与主会话完全一致**（2026-10-07 用户要求）：时间线 + 同一个
// Composer（输入框/选择器/读数全保留），只是输入被锁——不再维护第二套页面样式。
// 页头只留身份信息（子会话/模型/返回），读数（上下文环/统计）在输入条里，
// 与主会话同一位置。例外是**确认门**：子 Agent 的确认由父会话代理（AGENTS.md
// §2.3），用户在这一页看到待裁决的确认卡时必须能批（点了没反应比没有按钮更糟）。
import { useCallback, useEffect, useRef, useState } from "react";
import type { AgentSource } from "../../shared/types";
import type { UIState } from "../../shared/store";
import { Button } from "../form";
import { Composer } from "../composer";
import { Block } from "../thread/blocks/Block";
import { ScrollToBottom } from "../thread/ScrollToBottom";
import { WINDOW_INITIAL, WINDOW_BATCH, WindowSentinel } from "../thread/Thread";
import { useStickyFollow } from "../thread/useStickyFollow";

export function ChildSessionPage({
  sessionId,
  source,
  state,
  onConfirm,
  onAnswer,
  onBack,
}: {
  /** 子会话 id（标签键 child:<sessionId> 解析出来的那个）。 */
  sessionId: string;
  /** 数据源：childHistory 按子会话 id 寻址（后端 chat.history 已支持）。 */
  source: AgentSource;
  /** 这个子会话在 store 里的 state（blocks 实时流 + 历史 + 它自己的模型/占用）。
   *  还没装载（App 正在读历史）时是 undefined——显示加载态，不编一份空时间线。 */
  state?: UIState;
  /** 裁决这个子会话里挂起的确认（父会话代理确认门，AGENTS.md §2.3）。 */
  onConfirm: (id: string, allow: boolean) => void;
  /** 回答这个子会话里的 ask_user 提问（确认门代理同一条路由——答案发给父会话）。 */
  onAnswer?: (id: string, text: string) => void;
  /** 返回主会话（标签栏的「聊天」标签）。 */
  onBack: () => void;
}) {
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  // 重试计数：失败后点「重试」要能重新触发 effect（状态没变也能再试一次）
  const [nonce, setNonce] = useState(0);
  // source 放 ref：App 重建回调身份不该重新发请求（与 DispatchCard 同款纪律）
  const sourceRef = useRef(source);
  sourceRef.current = source;

  // 读这个子会话的历史——**通过数据源**读，让 store 按 sessionId 把它的 state 建起来
  //（WSAgent.childHistory 发 historyLoaded，sessionId 是子会话自己的），随后的事件
  // 由 store 的双投续上：所以这一页看到的是**实时流**，不是一张快照。
  useEffect(() => {
    // alive：切标签/卸载后迟到的应答不许再写 state（会画到别的子会话页上）
    let alive = true;
    setStatus("loading");
    setError("");
    sourceRef.current.childHistory(sessionId).then(
      () => { if (alive) setStatus("ready"); },
      (e: unknown) => {
        if (!alive) return;
        // 失败必须说话：静默降级成"空历史"是编结论（用户会以为子会话本来就是空的）
        setError(e instanceof Error ? e.message : String(e));
        setStatus("error");
      },
    );
    return () => { alive = false; };
  }, [sessionId, nonce]);

  const handleConfirm = useCallback((id: string, allow: boolean) => {
    onConfirm(id, allow);
  }, [onConfirm]);

  const handleAnswer = useCallback((id: string, text: string) => {
    onAnswer?.(id, text);
  }, [onAnswer]);

  const blocks = state?.blocks ?? [];
  const context = state?.context ?? null;
  const stats = state?.stats ?? null;
  const model = state?.model ?? "";

  // 滚动跟随：与主时间线**同一份状态机**（useStickyFollow）——之前这页没装，
  // 子 Agent 流式跑工具/输出时页面纹丝不动（用户报「子会话尤其明显」的根因）。
  // empty 传「当前有没有内容」：加载态→内容就绪会替换 endRef 所在子树，
  // 状态机必须随这个边界重装（否则装在空态上永不生效）。
  const { endRef } = useStickyFollow({ empty: blocks.length === 0, composerScope: ".child-session-page" });
  // 窗口化渲染：与主时间线同一份参数（之前全量挂载——长子会话切标签页必卡）。
  const [windowSize, setWindowSize] = useState<number>(WINDOW_INITIAL);
  const hidden = Math.max(0, blocks.length - windowSize);
  const visible = hidden > 0 ? blocks.slice(hidden) : blocks;
  // 换子会话（标签切走再切回同一组件实例）重置窗口
  useEffect(() => { setWindowSize(WINDOW_INITIAL); }, [sessionId]);

  return (
    <div className="child-session-page">
      <div className="child-session-head">
        <span className="child-session-title">子会话</span>
        <span className="child-session-id mono" title={sessionId}>{sessionId}</span>
        {/* 子会话自己的模型：缺席（老后端/未配置）就不渲染这一项，不显示假名字 */}
        {model !== "" && <span className="child-session-model mono" title={`子会话用的模型：${model}`}>{model}</span>}
        {/* 只读语义说明一次（页头）——而不是在每条消息上重复禁用原因：
         *  禁用按钮的 title 提示在多数浏览器里不弹（禁用元素不派发鼠标事件）。 */}
        <span className="child-session-readonly">只读 · 这是子 Agent 自己的会话，不提供撤回/编辑（待裁决的确认可以在这里批）</span>
        <Button className="child-session-back" onClick={onBack}>返回主会话</Button>
      </div>
      <div className="child-session-body">
        {status === "loading" && blocks.length === 0 && <div className="child-session-note">正在读取子会话历史…</div>}
        {status === "error" && (
          <div className="child-session-note error" role="alert">
            <span className="child-session-msg">读不到子会话历史：{error}</span>
            <Button className="child-session-retry" onClick={() => setNonce((n) => n + 1)}>重试</Button>
          </div>
        )}
        {status === "ready" && blocks.length === 0 && (
          <div className="child-session-note">这个子会话没有可显示的历史（它可能还没开始执行，或历史已被清空）。</div>
        )}
        {blocks.length > 0 && (
          <>
            {/* 复用 .thread（主时间线的容器类：块间距、720px 居中、左右 24px 内边距）——
             *  同一个视觉语言，不是第二套排版 */}
            <div className="thread">
              {hidden > 0 && <WindowSentinel onExpand={() => setWindowSize((n) => n + WINDOW_BATCH)} label={`前面还有 ${hidden} 条…`} />}
              {visible.map((block) => (
                // readOnly：不渲染用户气泡的动作条（撤回/编辑在这里没有意义，见 Block 的说明）
                <Block key={block.uid} block={block} onConfirm={handleConfirm} onAnswer={handleAnswer} readOnly />
              ))}
              {/* 跟随锚点：useStickyFollow 靠它定位滚动容器（.thread → .child-session-body） */}
              <div ref={endRef} />
            </div>
            {/* 回到底部：与主时间线**共用同一份组件**（自己找滚动祖先 = 这里的
             *  .child-session-body）。各写一份的下场是修了一处漏一处。 */}
            <ScrollToBottom />
          </>
        )}
      </div>
      {/* 与主会话**同一个 Composer**（2026-10-07 用户要求版式完全一致）：输入框、
       *  三个选择器、上下文环、统计胶囊全保留，readOnly 只锁交互——三个选择器
       *  整组不可点（它们作用的是主会话的上下文，在子会话页操作会改错会话），
       *  上下文环/统计胶囊是读数保持可点开，数据就是这个子会话自己的那份。
       *  onSend/onCancel 传 no-op：输入已锁，canSend 恒 false，到不了这两个回调。 */}
      <Composer
        readOnly
        busy={Boolean(state?.busy)}
        context={context}
        stats={stats}
        onSend={() => {}}
        onCancel={() => {}}
      />
    </div>
  );
}
