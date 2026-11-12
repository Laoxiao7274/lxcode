// 助手页签（助手模式 P1 前端接线）：助手域（assistant.* 方法域）的独立会话时间线。
//
// 布局与主会话聊天页同款（thread + Composer），数据源是**助手域 state**
//（store 的 assistantStates——assistant.* 事件归约的独立 map，与主域空间隔离）；
// 输入走 assistant.send，确认卡/派发卡/任务块复用现有 Block 组件（事件 payload
// 与 chat 同构——一份渲染代码两域共用，AGENTS.md §2 前端一栏的既有纪律）。
//
// 会话管理（P1 最小）：后端 P0 没有暴露助手会话列表方法（store.AssistantSessions
// 是库函数、协议面未挂），页签持有**一个**当前助手会话 id——首次进入建一个
//（assistant.new），「新对话」按钮再建一个并切换。历史由这里装载
//（source.assistant.history 发 historyLoaded，space 标记进助手域 state），
// 之后 assistant.* 事件由 store 的助手域归约实时续上——与 ChildSessionPage 的
// 「装载 + 实时流」同一条契约。
//
// boot 与 StrictMode：boot 流程抽成 createBootRunner（in-flight promise 共享）——
// 语义是**纯装载**（2026-12 拍板：进入助手模式不再自动 assistant.new）：sessionId
// 非空时装载历史（historyLoaded → store 建助手域 state），空 = 中性态「未选择」
// （AssistantWorkspace 渲染提示，本组件不渲染——仍保留空 id 的静默处理以稳住
// boot runner 的契约）。重挂/重复 effect await 同一个 promise，结果照常写 state；
// 失败的 boot 清掉缓存，显式重试（reset + nonce）能重新走。
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { AgentSource, ChatSendImage, SendAttachments, SessionMeta } from "../../shared/types";
import type { UIState } from "../../shared/store";
import { Button } from "../form";
import { AssistantInput } from "./AssistantInput";
import { Block } from "../thread/blocks/Block";
import { ScrollToBottom } from "../thread/ScrollToBottom";
import { ThinkingState } from "../../aicss/ThinkingState";
import { WindowSentinel } from "../thread/Thread";
import { useWindowedHistory } from "../thread/useWindowedHistory";
import { useLoadOlderHistory, loadOlderLabel } from "../thread/useLoadOlderHistory";
import { useStickyFollow } from "../thread/useStickyFollow";
import { useCompaction } from "../../app-hooks/use-compaction";
import { useSettings } from "../../shared/settings";
import { visionBlockNotice } from "../../shared/attachments";

export type AssistantUIState = UIState;

/** 尾块是流式中的 assistant：与主时间线同一判定（state.streaming 恒为逻辑尾块）。 */
function lastIsStreamingAssistant(state?: AssistantUIState): boolean {
  return Boolean(state?.streaming);
}

/** 连接类错误判定（boot 自动重试的门槛）：与 connection.ts 的三类失败串同源
 *  （「后端未连接」= call 的 WS 未 open 同步 reject；「后端连接已断开」= 断连
 *  rejectAllPending；「切换后端连接」= setBackend rejectAllPending）。
 *  「请求超时」不在此列——后端活着、只是慢/挂了，自动重试时机不可预判，
 *  留给手动重试。 */
export function isConnectionError(message: string): boolean {
  return message === "后端未连接"
    || message === "后端连接已断开"
    || message === "切换后端连接";
}

/**
 * boot 流程（装载历史）：从组件里抽出的纯异步函数，让 StrictMode
 * 双挂载场景可以被测试直接驱动（见 tests/assistant-page.test.mjs）。
 *
 * **只装载，不新建**（2026-12 拍板）：sessionId 非空走 assistant.history；
 * 空时直接静默返回（中性态由 AssistantWorkspace 渲染，不发 assistant.new）。
 * 返回 { run, isBooted, reset }：run 发起 boot 并**共享 in-flight promise**
 *（多次调用只装载一次——后到的调用 await 同一个 promise）；isBooted 判断是否
 * 已有 boot（含进行中）；reset 只在显式重试（错误态的「重试」按钮）时调用，
 * 丢掉旧 promise 让下一次 run 重新走一遍。
 *
 * **已装载的会话不重发 assistant.history**（2026-12 会话切换优化批 2）：
 * opts.isLoaded() 为真（该会话的助手域 state.historyReady——历史完成过一次
 * 回放）时 run 直接返回。切走期间 assistant.* 事件仍由 store 双投归约进
 * assistantStates[id]（双投不依赖视图挂载），切回来时增量已经在 state 里——
 * 再拉一次 assistant.history 是一个纯往返 + 整时间线重建，拿到的是同一份事实。
 * 判定放在 runner 里（而不是组件的 effect 外）：StrictMode 双挂载/重复 effect
 * 与切会话重挂（AssistantWorkspace 的 key）走同一条闸门，不会出现「双挂载绕过
 * 判定多拉一次」的口子。失败重试照旧：失败的装载不会让 historyReady 变真，
 * reset + run 能重新走。
 */
export function createBootRunner(opts: {
  source: () => AgentSource;
  sessionId: () => string;
  onSessionId: (id: string) => void;
  /** 该会话的历史是否已装载过（助手域 state.historyReady）——为真时 boot
   *  不再发 assistant.history（秒回路）。缺省恒 false（测试/旧调用方语义不变）。 */
  isLoaded?: () => boolean;
}) {
  let promise: Promise<void> | null = null;
  const run = (): Promise<void> => {
    if (promise) return promise;
    const boot = async (): Promise<void> => {
      const id = opts.sessionId();
      // 空 id = 未选择会话（中性态）：不自动 assistant.new——进入助手模式
      // 建空会话是旧语义（加重「点回会话无内容」的 bug），已废除。
      if (!id) return;
      // 已装载：跳过（实时流没断过——store 双投一直在归约）
      if (opts.isLoaded?.()) return;
      const a = opts.source().assistant;
      if (!a) {
        throw new Error("当前数据源不支持助手（演示模式请升级数据源）");
      }
      await a.history(id);
    };
    promise = boot().catch((e) => {
      // 失败必须说话：静默降级成"空历史"是编结论。吞掉本次错误并清掉
      // 缓存的 promise——失败的 boot 不算数，重试（reset + run）能重新走。
      promise = null;
      throw e;
    });
    return promise;
  };
  return { run, isBooted: () => promise !== null, reset: () => { promise = null; } };
}

export function AssistantPage({
  source,
  state,
  sessionId,
  sessionMeta,
  onSessionId,
  onConfirm,
  onAnswer,
  onOpenChild,
}: {
  source: AgentSource;
  /** 当前助手会话在助手域的 state（assistantStates[sessionId]——还没装载时 undefined）。 */
  state?: AssistantUIState;
  /** 当前助手会话 id（AssistantWorkspace 持有——派发卡的子会话视图要按它反查域归属）。 */
  sessionId: string;
  /** 当前会话的列表条目（assistant.list——页头显示标题/时间；缺席 = 未选择）。 */
  sessionMeta?: SessionMeta;
  /** 助手会话 id 变更（boot 不再自动新建后暂无调用方——契约位保留）。 */
  onSessionId: (id: string) => void;
  /** 裁决助手会话里挂起的确认（assistant.confirm）。 */
  onConfirm: (id: string, allow: boolean) => void;
  /** 回答助手会话里的 ask_user 提问（assistant.confirm 带 answer）。 */
  onAnswer: (id: string, text: string) => void;
  /** 点派发卡打开子会话视图（AssistantWorkspace 的临时视图——助手模式内导航）。 */
  onOpenChild?: (sessionId: string) => void;
}) {
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [nonce, setNonce] = useState(0);
  // vision 可用性感知（2026-12 助手附件）：与主域 App 同一条判定链
  //（visionBlockNotice 纯函数）——助手会话实际模型（state.model）优先，回落全局
  // default 角色。非空 = 「+」禁用 + title 说明；未知/查不到不禁用（后端兜底）。
  const { settings, providers } = useSettings();
  const visionBlocked = useMemo(
    () => visionBlockNotice(state?.model || settings.model, providers.flatMap((p) => p.models)),
    [state?.model, settings.model, providers],
  );
  // 手动压缩（assistant.compact——assistant.* 方法域；chat.compact 寻址助手
  // 会话会串域报错）。超时尾巴/重入闸/compacting 反馈全在 useCompaction 钩子里
  //（与主域同一份）；事件认账口用 compacted 等价事件（assistant.compacted 归一），
  // 按 sessionId 匹配照常命中。成功后的历史重拉：助手域 history（historyLoaded
  // 会重建本会话时间线、检查点卡落位）。
  const { handleCompactClick, compacting } = useCompaction({
    source,
    currentId: sessionId,
    reportError: (m) => setError(m),
    compact: (id) => {
      const a = sourceRef.current.assistant;
      if (!a) return Promise.reject(new Error("当前数据源不支持助手"));
      return a.compact(id);
    },
    reloadHistory: async (id) => { await sourceRef.current.assistant?.history(id); },
  });
  // source 放 ref：App 重建回调身份不该重新发请求（与 ChildSessionPage 同款纪律）
  const sourceRef = useRef(source);
  sourceRef.current = source;
  // sessionId 也走 ref：boot promise 读的是最新值，不依赖 effect 重跑
  const sessionIdRef = useRef(sessionId);
  sessionIdRef.current = sessionId;
  // state（该会话的助手域 UIState）进 ref：boot 的 isLoaded 判定读最新值
  //（state prop 每帧都可能是新对象，闭包里那份是创建 runner 那一帧的旧值）
  const stateRef = useRef(state);
  stateRef.current = state;
  // onSessionId 保留契约位（boot runner 形状不动）：纯装载语义下 boot 不再
  // 回报新 id（不自动新建），回调暂无调用方。
  const onSessionIdRef = useRef(onSessionId);
  onSessionIdRef.current = onSessionId;
  // boot promise 缓存（修 StrictMode 双执行死锁）：bootedRef 布尔闸门 + cleanup
  // 的 alive 吞结果，在 StrictMode 下是死锁组合——第一次 effect 的结果被 cleanup
  // 丢弃、第二次被闸门挡住 → sessionId 永空、status 永 loading。改为共享
  // in-flight promise：第二次 effect await 同一个 promise，结果照常写 state
  //（React 18 StrictMode 双挂载是正常流程，写入安全）；重复装载由
  // promise 共享天然防住（副作用请求只发一次）。
  const bootRef = useRef<ReturnType<typeof createBootRunner> | null>(null);
  if (!bootRef.current) {
    bootRef.current = createBootRunner({
      source: () => sourceRef.current,
      sessionId: () => sessionIdRef.current,
      onSessionId: (id) => onSessionIdRef.current(id),
      // 秒回判定：该会话的助手域 state 已完成过历史回放（historyReady）就不再发
      // assistant.history——state 进 ref（prop 每帧换，判定读最新值）。
      isLoaded: () => Boolean(stateRef.current?.historyReady),
    });
  }

  // 进入（挂载）：sessionId 非空时装载历史（historyLoaded → store 建助手域
  // state），空 = 中性态不发请求（不自动 assistant.new——2026-12 拍板）。
  // StrictMode 重挂时 await 同一个 promise，状态照常落定。组件按 sessionId
  // 重挂（AssistantWorkspace 的 key）——bootRef 随实例新建，每次切换都装载。
  useEffect(() => {
    const runner = bootRef.current!;
    if (runner.isBooted()) return;
    runner.run().then(
      () => setStatus("ready"),
      (e: unknown) => {
        setError(e instanceof Error ? e.message : String(e));
        setStatus("error");
      },
    );
  }, [sessionId, nonce]);

  // boot 失败的连接恢复自动重试（2026-12「每次点开都显示后端未连接」修复）：
  // 记忆的助手会话使本页在启动即 boot assistant.history——WS 未 open 时同步
  // reject「后端未连接」，status 落 error。重试条件按错误类型分：**连接类**
  //（isConnectionError：后端未连接/连接断开/切换后端连接——重连层（固定 5s +
  // 回可见重连）保证连接态一定会翻转回来，恢复后 boot 必然重试且必成功，自动
  // 重试是收尾而非赌博）；**非连接类**（请求超时/会话不存在/业务错误）不自动
  // 重试——盲目重试对不存在会话是无限循环，仍走既有的手动「重试」按钮。
  const [connected, setConnected] = useState(false);
  useEffect(() => source.subscribe((ev) => {
    if (ev.type === "connectionChanged") setConnected(ev.connected);
  }), [source]);
  const retryNonceRef = useRef(nonce);
  useEffect(() => {
    if (status !== "error" || !connected) return;
    if (!isConnectionError(error)) return;
    // 同一个错误只自动重试一次：retryNonceRef 对齐到当前 nonce——连接态翻转的
    // 多次事件（断连→连上→又断→又连上）在同一错误下不反复 reset。
    if (retryNonceRef.current !== nonce) return;
    retryNonceRef.current = nonce + 1;
    bootRef.current?.reset();
    setNonce((n) => n + 1);
    setStatus("loading");
  }, [status, connected, error, nonce]);

  const handleSend = useCallback((text: string, atts: SendAttachments) => {
    if (!sessionId || !sourceRef.current.assistant) return;
    // 助手域附件（2026-12 用户拍板）：images 随 assistant.send 上 wire（与
    // chat.send 同款校验与 vision 门）；files 不接（输入区是图片精简版）。
    const opts: { effort?: string; images?: ChatSendImage[] } = {};
    if (atts?.images.length) opts.images = atts.images;
    sourceRef.current.assistant.send(sessionId, text, Object.keys(opts).length ? opts : undefined);
  }, [sessionId]);

  const handleCancel = useCallback(() => {
    if (sessionId) sourceRef.current.assistant?.cancel(sessionId);
  }, [sessionId]);

  // 权限档：改档走 assistant.approval（乐观对齐由 assistant.approvalChanged
  // 广播回写助手域 state——与 ChildSessionPage 的 modelChanged 纪律同款）。
  const handlePickApproval = useCallback((mode: string) => {
    if (sessionId) sourceRef.current.assistant?.setApproval(sessionId, mode);
  }, [sessionId]);

  // 模型：切模型走 assistant.model——不乐观改，由 assistant.modelChanged 对齐
  //（同 ChildSessionPage 纪律：模型选择是会话级状态，后端回执是事实源）。
  const handlePickModel = useCallback((id: string) => {
    if (sessionId) sourceRef.current.assistant?.setModel(sessionId, id);
  }, [sessionId]);

  // 滚动跟随：与主时间线同一份状态机（useStickyFollow）——scrollRef 由它自己定位
  //（endRef 的祖先），这里的 ref 引用未使用（保持与 ChildSessionPage 同款解构）。
  const total = (state?.blocks.length ?? 0) + (state?.streaming ? 1 : 0);
  const { endRef, stickyRef, scrollRef } = useStickyFollow({
    empty: total === 0,
    composerScope: ".assistant-page",
    sessionKey: sessionId,
  });
  // **窗口化渲染 + 上翻扩窗的滚动锚定**（2026-12 会话切换优化批 2）：助手会话
  // 与主时间线/子会话页共用同一份机制（useWindowedHistory——窗口按会话派生，
  // 切会话首帧就是 40 块；上翻前插按高度差补偿 scrollTop）。之前助手页整列表
  // 全量挂载，大会话切进来一次挂上千个块（markdown 解析 + 工具行全在里面）。
  const { hidden, expandOlder, captureAnchor, clearAnchor } = useWindowedHistory({
    sessionKey: sessionId,
    total,
    scrollRef,
    isSticky: () => stickyRef.current,
  });
  // 上翻触顶：先补后端更早一页（assistant.history 的 before_seq 分页，与主域
  // chat.history 同协议），补不到再扩本地窗口——共用 useLoadOlderHistory。
  const loadOlder = useLoadOlderHistory({
    sessionId,
    source,
    hasMore: Boolean(state?.historyMore),
    firstSeq: state?.historyFirstSeq ?? 0,
    expand: expandOlder,
    captureAnchor,
    clearAnchor,
    // 助手域分页必须走 assistant.history（域隔离：chat.* 寻址助手会话报错）
    load: (beforeSeq: number) =>
      sourceRef.current.assistant?.history(sessionId, beforeSeq).then(() => undefined) ?? Promise.resolve(),
  });

  const sealed = state?.blocks ?? [];
  const streaming = state?.streaming ?? null;
  const busy = Boolean(state?.busy);
  const sending = Boolean(state?.sending);
  // 窗口只切已定型块；流式块恒在末尾、永远落在窗口内（hidden 按逻辑时间线算，
  // 已包含那一块——与 Thread/ChildSessionPage 同一条注释）
  const visible = hidden > 0 ? sealed.slice(hidden) : sealed;

  return (
    <div className="assistant-page">
      <div className="assistant-head">
        <span className="assistant-title">{sessionMeta?.title || "助手"}</span>
        <span className={"s-dot" + (busy ? " live" : "")} aria-hidden />
      </div>
      <div className="assistant-body">
        {status === "ready" && total === 0 && (
          <div className="assistant-note-body">把日常事务交给助手——查资料、读文件、跑只读命令；要改代码它会把任务派给项目的 Agent。</div>
        )}
        {status === "error" && (
          <div className="assistant-note-body error" role="alert">
            <span>{error}</span>
            <Button onClick={() => { bootRef.current?.reset(); setNonce((n) => n + 1); setStatus("loading"); }}>重试</Button>
          </div>
        )}
        {total > 0 && (
          <>
            <div className="thread">
              {/* 与主时间线同一条：本地窗口扩到 0 之后只要后端还有更早的历史，
               *  哨兵就必须在（否则分页永远触发不了）。 */}
              {(hidden > 0 || state?.historyMore) && (
                <WindowSentinel onExpand={loadOlder} label={loadOlderLabel(Boolean(state?.historyMore), hidden)} />
              )}
              {visible.map((block) => (
                <Block key={block.uid} block={block} onConfirm={onConfirm} onAnswer={onAnswer} onOpenChild={onOpenChild} />
              ))}
              {streaming && (
                <Block key={streaming.uid} block={streaming} onConfirm={onConfirm} onAnswer={onAnswer} onOpenChild={onOpenChild} />
              )}
              {busy && !streaming && (
                <div className="msg">
                  <ThinkingState text="正在处理…" />
                </div>
              )}
              <div ref={endRef} />
            </div>
            <ScrollToBottom />
          </>
        )}
      </div>
      <AssistantInput
        busy={busy}
        disabled={sending}
        onSend={handleSend}
        onCancel={handleCancel}
        approval={state?.approval}
        onPickApproval={handlePickApproval}
        model={state?.model}
        onPickModel={handlePickModel}
        context={state?.context}
        stats={state?.stats}
        onCompact={handleCompactClick}
        compacting={compacting}
        visionBlocked={visionBlocked}
        todos={state?.todos ?? []}
      />
    </div>
  );
}
