// 子会话临时视图：点主时间线 dispatch 卡打开的**临时视图**（2026-12 用户拍板：标签栏
// 不再出现子会话——这个视图不进 tabs，页头「返回主会话」关闭它，返回后视图卸载，
// store 里的子会话 state 与实时流缓存保留，重新打开由这里重新装载历史）。
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
import { ThinkingState } from "../../aicss/ThinkingState";
import { WindowSentinel } from "../thread/Thread";
import { useWindowedHistory } from "../thread/useWindowedHistory";
import { useLoadOlderHistory, loadOlderLabel } from "../thread/useLoadOlderHistory";
import { useStickyFollow } from "../thread/useStickyFollow";

/** 尾块是流式中的 assistant：主时间线的判定（Thread.tsx）对子会话**同一份**——
 *  delta 在流时就「有输出」，不该再叠一行「正在处理…」（两个进行中指示互相打架）。
 *
 *  2026-12 起流式块单独存在 state.streaming 里（见 shared/blocks.ts），所以判定
 *  直接看它——逻辑时间线的尾块**就是**它（不变量：它恒为最后一块）。 */
function lastIsStreamingAssistant(state?: UIState): boolean {
  return Boolean(state?.streaming);
}

export function ChildSessionPage({
  sessionId,
  source,
  state,
  onConfirm,
  onAnswer,
  onBack,
}: {
  /** 子会话 id（视图键 child:<sessionId> 解析出来的那个）。 */
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
  /** 返回主会话（关闭临时视图：视图不进标签栏，「返回主会话」是显式的关闭/返回入口）。 */
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
    // alive：切走/卸载后迟到的应答不许再写 state（会画到别的子会话视图上）
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

  // **渲染不拼数组**（与主时间线同一条纪律）：已定型块走窗口切片，正在流式的
  // 那一块（state.streaming）单独挂——每帧拼一份逻辑时间线等于把 reduceDelta
  // 省下来的 O(块数) 复制又还回去。
  const sealed = state?.blocks ?? [];
  const streaming = state?.streaming ?? null;
  const total = sealed.length + (streaming ? 1 : 0);
  const context = state?.context ?? null;
  const stats = state?.stats ?? null;
  const model = state?.model ?? "";
  // 子会话的忙闲（CHILD_BUSY_HINTS 兜底置真 / 轮末 busy=false 翻回）：
  // 驱动页头忙点与时间线尾部的「正在处理…」指示。
  const busy = Boolean(state?.busy);
  // todo 契约批②：子会话自己的计划条——数据 = 该子会话 state 的 todos
  //（双投已把带 dispatch_id 的 todo.updated 归约进子会话 state，零新机制）。
  // 派发结束定格后显示定格值。**挂载点 = Composer 的 todos prop**（2026-12 用户
  // 报「清单卡出现在时间线顶部」的修复）：主会话的纪律是清单只在输入白卡顶部
  //（.pi-plan，见 Composer），子会话视图用同一个 Composer，就传同一份 prop——
  // 之前这页自己在页头下挂 TodoList，位置落在了时间线上方（消息流内），且与主
  // 会话两套位置两套视觉。时间线里不出现清单卡：归约层 todo.updated 不产生块
  //（reduce.ts 的既有纪律），子会话双投路径同一条，无特例。
  const todos = state?.todos ?? [];

  // 滚动跟随：与主时间线**同一份状态机**（useStickyFollow）——之前这页没装，
  // 子 Agent 流式跑工具/输出时页面纹丝不动（用户报「子会话尤其明显」的根因）。
  // empty 传「当前有没有内容」：加载态→内容就绪会替换 endRef 所在子树，
  // 状态机必须随这个边界重装（否则装在空态上永不生效）。
  const { endRef, stickyRef, scrollRef } = useStickyFollow({
    empty: total === 0,
    composerScope: ".child-session-page",
    // 子会话标签页之间/从主会话切进来同样要重装状态机并贴底——否则视口停在
    // 上一个会话的滚动位置（与主时间线同款 bug）。
    sessionKey: sessionId,
  });
  // 窗口化渲染 + 上翻扩窗的滚动锚定：与主时间线**同一份实现**
  // （useWindowedHistory——窗口按会话派生，前插后按高度差补偿 scrollTop）。
  // 之前这页自己写一份 useState + useEffect 重置：既在切换首帧挂着上一份
  // 窗口，又没有锚定——子会话上翻照样“跳顶”。
  const { hidden, expandOlder, captureAnchor, clearAnchor } = useWindowedHistory({
    sessionKey: sessionId,
    total,
    scrollRef,
    isSticky: () => stickyRef.current,
  });
  // 窗口只切已定型块；流式块恒在末尾、永远落在窗口内（hidden 按逻辑时间线算，
  // 已包含那一块——见 Thread.tsx 的同款说明）
  const visible = hidden > 0 ? sealed.slice(hidden) : sealed;
  // 上翻触顶：先补后端更早一页（子会话也是会话，chat.history 同一个方法），
  // 补不到再扩本地窗口——与主时间线共用一份实现（useLoadOlderHistory）。
  const loadOlder = useLoadOlderHistory({
    sessionId,
    source,
    hasMore: Boolean(state?.historyMore),
    firstSeq: state?.historyFirstSeq ?? 0,
    expand: expandOlder,
    captureAnchor,
    clearAnchor,
    // 子会话分页必须走 childHistory（带 child 标记——分页补进来的任务说明书
    // 要渲染成「任务」块，不能变成用户气泡）
    load: (beforeSeq: number) => source.childHistory(sessionId, beforeSeq).then(() => undefined),
  });

  return (
    <div className="child-session-page">
      <div className="child-session-head">
        <span className="child-session-title">子会话</span>
        {/* 忙闲点：与会话侧栏同一套件（.s-dot，busy 时加 .live 呼吸蓝点）——
         *  子会话在跑时页头一眼可辨；轮末 busy=false 翻回，恢复普通灰点。 */}
        <span className={"s-dot" + (busy ? " live" : "")} aria-hidden />
        <span className="child-session-id mono" title={sessionId}>{sessionId}</span>
        {/* 子会话自己的模型：缺席（老后端/未配置）就不渲染这一项，不显示假名字 */}
        {model !== "" && <span className="child-session-model mono" title={`子会话用的模型：${model}`}>{model}</span>}
        {/* 只读语义说明一次（页头）——而不是在每条消息上重复禁用原因：
         *  禁用按钮的 title 提示在多数浏览器里不弹（禁用元素不派发鼠标事件）。
         *  2026-12：模型选择器单独放行，说明随之补充去向——切了模型要回主会话
         *  让主 Agent 续跑此子会话才见新模型（输入框仍锁，派发是主会话发起的）。 */}
        <span className="child-session-readonly">只读 · 这是子 Agent 自己的会话，不提供撤回/编辑（待裁决的确认可以在这里批）；模型可切换——对下一次派发生效，回主会话让主 Agent 续跑此子会话</span>
        <Button className="child-session-back" onClick={onBack}>返回主会话</Button>
      </div>
      {/* 子会话自己的计划条不再在这里挂（2026-12 用户报「清单卡出现在时间线顶部」）：
       *  它随 todos prop 进 Composer 的白卡顶部区块（.pi-plan）——与主会话同一个
       *  挂载点、同一套视觉。归约层 todo.updated 不产生时间线块，清单不会在消息流里。 */}
      <div className="child-session-body">
        {status === "loading" && total === 0 && <div className="child-session-note">正在读取子会话历史…</div>}
        {status === "error" && (
          <div className="child-session-note error" role="alert">
            <span className="child-session-msg">读不到子会话历史：{error}</span>
            <Button className="child-session-retry" onClick={() => setNonce((n) => n + 1)}>重试</Button>
          </div>
        )}
        {status === "ready" && total === 0 && (
          <div className="child-session-note">这个子会话没有可显示的历史（它可能还没开始执行，或历史已被清空）。</div>
        )}
        {total > 0 && (
          <>
            {/* 复用 .thread（主时间线的容器类：块间距、720px 居中、左右 24px 内边距）——
             *  同一个视觉语言，不是第二套排版 */}
            <div className="thread">
              {/* 与主时间线同一条：本地窗口扩到 0 之后只要后端还有更早的历史，
               *  哨兵就必须在（否则分页永远触发不了）。 */}
              {(hidden > 0 || state?.historyMore) && (
                <WindowSentinel onExpand={loadOlder} label={loadOlderLabel(Boolean(state?.historyMore), hidden)} />
              )}
              {visible.map((block) => (
                // readOnly：不渲染用户气泡的动作条（撤回/编辑在这里没有意义，见 Block 的说明）
                <Block key={block.uid} block={block} onConfirm={handleConfirm} onAnswer={handleAnswer} readOnly />
              ))}
              {/* 正在流式的那一块：单独挂（不拼数组——与主时间线同一条纪律） */}
              {streaming && (
                <Block key={streaming.uid} block={streaming} onConfirm={handleConfirm} onAnswer={handleAnswer} readOnly />
              )}
              {/* 生成中且还没有任何输出时显示思考 shimmer（与主时间线同一形态、同一文案：
               *  Thread.tsx 对 state.busy && !lastIsStreamingAssistant 的呈现原样复刻——
               *  busy 的信号来自子会话自己的 state（CHILD_BUSY_HINTS 兜底置真 + 轮末
               *  busy=false 翻回），流式打字光标与思考块由共用的 Block/AnswerBody 自带，
               *  这里补的是容器级指示：工具执行中 / 等首字时时间线尾部不空着。 */}
              {busy && !streaming && (
                <div className="msg">
                  <ThinkingState text="正在处理…" />
                </div>
              )}
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
       *  三个选择器、上下文环、统计胶囊全保留——输入框仍锁定（chat.send 对子会话
       *  的拦截是刻意设计），但**模型选择器单独放行**（2026-12：子会话页切模型）：
       *  模型是子会话自己的会话级状态，onPickModel 显式带子会话 id 寻址（视图打开
       *  期间连接焦点仍是父会话，缺省寻址会落错会话）。Agent/权限选择器保持锁定
       *  （它们作用的是父会话的上下文），上下文环/统计胶囊是读数保持可点开，
       *  数据就是这个子会话自己的那份。
       *  onSend 传 no-op：输入已锁，canSend 恒 false，到不了这个回调。
       *  onCancel 接通（2026-10-10：子会话 busy 事件上抛后，「生成中」行与停止钮
       *  会随 busy 出现）——停止 = chat.cancel 带子会话 id，服务端经父会话的
       *  CancelChild 停掉真正在跑的子轮（子会话运行时不在 server.sessions 里）。 */}
      <Composer
        readOnly
        busy={busy}
        context={context}
        stats={stats}
        todos={todos}
        // 子会话**自己的**模型（与页头那一项同一个值）：没选过（state.model 为空串）
        // 就原样传空串——ModelPicker 的回落链是 `model ?? settings.model`，`??` 对
        // 空串不触发，芯片显示「模型」空态占位；传 undefined 反而会回落全局 default
        // 角色冒充「子会话已选」（正是要避免的误导）。
        model={model}
        modelInteractive
        onPickModel={(id) =>
          // 失败不弹全局报错条（子会话视图没有那条通道）也不假装成功：选中态
          // 归根由 chat.modelChanged 广播对齐——后端没改成功就不会广播，芯片
          // 停在旧模型上（与主会话 pickSessionModel 的「不乐观改」同一条纪律）。
          source.setSessionModel(id, sessionId).catch(() => {})}
        onSend={() => {}}
        onCancel={() => source.cancel(sessionId)}
      />
    </div>
  );
}
