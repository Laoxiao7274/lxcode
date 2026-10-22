// 用户气泡的编辑/撤回 + 输入框草稿域（审查 M5 前端重构：从 App.tsx 纯移动，
// 语义零改动）。
//
// 职责：草稿注入/记账（draft、drafts、attsDraft）、编辑态（editDraft）、
// 撤回（先本地乐观截断 + 原文乐观回填，失败用后端真相对齐并回滚注入）、
// 发送（编辑态下先撤回再发）、切会话清注入态、发送失败把附件装回输入框。
import { useCallback, useEffect, useRef, useState } from "react";
import { beginEdit, canRewind, planRewind, rewindThenRestore, type EditDraft } from "../shared/blocks";
import { draftOf, writeDraft, writeDraftAtts, writeDraftText, type SessionDraft, type SessionDrafts } from "../shared/session-drafts";
import type { ThreadBlock } from "../shared/store";
import type { SendAttachments } from "../shared/types";
import type { ComposerAttsDraft } from "../components/composer";
import type { ComposerDraft } from "../components/composer";

/** 编辑/撤回/草稿域（AppBody 装配——参数即它持有的闭包依赖，显式化）。 */
export function useRewind({ source, currentId, currentIdRef, reportError, sendWithOptions }: {
  source: import("../shared/types").AgentSource;
  currentId: string;
  /** 草稿记账/失败回填的写入点要落在**正确的会话**上——失败回填可能在用户已切
   *  到别的会话之后到达（ws 异步），按 currentId 闭包写会串会话。 */
  currentIdRef: { current: string };
  reportError: (message: string) => void;
  sendWithOptions: (text: string, atts?: SendAttachments) => void;
}) {
  /** 发送失败的附件回滚（store 钩子 → Composer 的 attsDraft）：文本丢了附件不能丢。
   *  per-session 记账（drafts）之下它只服务「正看着的那个会话」——失败发生在别的
   *  会话时只落记账键（切回去由初始草稿还原），不装给挂载中的 Composer。 */
  const [attsDraft, setAttsDraft] = useState<ComposerAttsDraft | null>(null);
  const attsDraftIdRef = useRef(0);

  /** 输入框草稿注入（撤回/编辑把原文放回输入框；atts = 附件一并装回附件区——
   *  队列条目「编辑」的装回路径）。id 单调递增——同一条消息连续注入两次也要
   *  重新写入（按文本比较的话第二次是 no-op，用户看到"点了没反应"）。
   *
   *  **sid = 注入归属的会话**（调用方在动作发生那一刻捕获，不是回调时刻的
   *  「当前会话」）：记账键恒落 sid；挂载中的 Composer 只在 sid 正是当前会话
   *  时才接收注入——后端应答是异步的，用户可能已经切走，把 A 的撤回文本注进
   *  B 的挂载 Composer（或 B 的记账键）就是跨会话泄漏（用户实测报的问题 1：
   *  撤回 A 里的消息，文本出现在 B 的输入框/时间线里）。
   *
   *  注入同时落 sid 的草稿记账键（drafts，shared/session-drafts.ts）：挂载中
   *  的那份由 draft prop 即时生效，记账键服务「切走再切回」的还原——两处写的
   *  是同一份语义。 */
  const [draft, setDraft] = useState<ComposerDraft | null>(null);
  const draftIdRef = useRef(0);
  /** per-session 草稿记账（照 sendQueues 的模式，独立于 reduce 流——纯 UI 状态）：
   *  键 = session id，值 = 文本 + 暂存附件。切走保留、切回还原、发送清空、失败
   *  回填落原会话键。 */
  const [drafts, setDrafts] = useState<SessionDrafts>({});
  const injectDraft = useCallback((sid: string, text: string, atts?: SendAttachments) => {
    setDrafts((cur) => writeDraftAtts(writeDraftText(cur, sid, text), sid, atts));
    if (sid === currentIdRef.current) {
      draftIdRef.current += 1;
      setDraft({ id: draftIdRef.current, text, ...(atts ? { atts } : {}) });
    }
  }, [currentIdRef]);
  /** 注入草稿的失败回滚（撤回被后端拒绝时把注入的原文撤出输入框）：sid 的记账
   *  键文本清零（附件暂存保留——注入本来就不该动它）；sid 正是当前会话时再清
   *  挂载 Composer 的注入态（清零 = 一条新 id 的空文本注入——Composer 只在 id
   *  变化时应用草稿，同 id no-op）；用户已切走时挂载 Composer 是别的会话的，
   *  只清掉本 hook 持有的注入态，不碰那个会话。 */
  const rollbackDraft = useCallback((sid: string) => {
    setDrafts((cur) => writeDraftText(cur, sid, ""));
    if (sid === currentIdRef.current) {
      draftIdRef.current += 1;
      setDraft({ id: draftIdRef.current, text: "" });
    } else {
      setDraft(null);
    }
  }, [currentIdRef]);
  /** Composer 的草稿实时回写 → 当前会话的记账键（采集端见 Composer.onDraftChange：
   *  打字/注入/清空/回填全汇聚到那一个 effect）。身份恒定——写哪个会话由
   *  currentIdRef 在调用瞬间决定，不进依赖。 */
  const handleDraftChange = useCallback((d: SessionDraft) => {
    setDrafts((cur) => writeDraft(cur, currentIdRef.current, d));
  }, [currentIdRef]);

  /** 发送失败的附件回滚钩子（store 的 onSendFailed → 装回输入框附件区）。 */
  const onSendFailed = useCallback((sessionId: string, atts: SendAttachments) => {
    // 失败回填落在**原会话**的记账键（shared/session-drafts.ts）：不管用户此刻
    // 看的是哪个会话，切回去文本不丢、附件也在。
    setDrafts((cur) => writeDraftAtts(cur, sessionId, atts));
    // 正看着这个会话才经 attsDraft 把附件装回**挂载中**的 Composer——装给别的
    // 会话的 Composer 就是串会话（这正是本次要修的 bug）。
    if (sessionId === currentIdRef.current) {
      attsDraftIdRef.current += 1;
      setAttsDraft({ id: attsDraftIdRef.current, atts });
    }
  }, [currentIdRef]);

  /** 编辑态：只记锚点，**历史一个字都不动**——真正的撤回推迟到下次发送前。 */
  const [editDraft, setEditDraft] = useState<EditDraft | null>(null);
  /** 最新时间线的引用（撤回要按锚点取原文）。**刻意用 ref 而不是把 state.blocks
   *  写进 handleRewind 的依赖**：依赖它 = 每个流式 delta 都换一次回调身份，而
   *  Block 是 memo 的（onRewind 变了 → 整屏用户气泡每帧重渲染一遍）。 */
  const blocksRef = useRef<ThreadBlock[]>([]);
  /** AppBody 每次渲染同步（保持与原实现相同的更新时机）。 */
  const syncBlocks = useCallback((blocks: ThreadBlock[]) => {
    blocksRef.current = blocks;
  }, []);

  // 撤回：历史立刻清空（由 source 的乐观 rewound 事件驱动归约器截断——两个实现
  // 走同一条路径），**原文乐观回填**（2026-12：点击即注入，不再等后端应答——
  // 应答链上有整段日志折叠统计，大会话 1~2s，用户看到"时间线清了文本迟迟不来"）。
  // 后端拒绝（配对校验）时 rollback 把注入的文本撤出输入框，时间线由 source
  // 已用后端真相对齐（重放历史把被乐观删掉的块拿回来），失败原因说给用户听。
  // 归属 sid 在点击那一刻捕获：应答是异步的，回调时「当前会话」可能已经是
  // 别的会话——不捕获就会把 A 的撤回文本注进 B（跨会话泄漏，问题 1 的根因）。
  const handleRewind = useCallback((block: ThreadBlock) => {
    if (!canRewind(block)) {
      reportError("这条消息来自旧版后端（没有 seq），无法撤回");
      return;
    }
    // 文本取自纯函数给出的撤回计划（与归约器同一份判定）——文本与截断不可能对不上
    const plan = planRewind(blocksRef.current, block.seq);
    if (!plan) {
      reportError("这条消息已经不在当前对话里了（可能已被撤回）");
      return;
    }
    const sid = currentId;
    setEditDraft(null); // 撤回之后没有"编辑中"这回事：历史已经清了，没什么可取消
    rewindThenRestore({
      rewind: () => source.rewind(sid, block.seq),
      text: plan.text,
      sid,
      inject: injectDraft,
      rollback: rollbackDraft,
      report: reportError,
    });
  }, [source, currentId, injectDraft, rollbackDraft, reportError]);

  // 编辑：只把原文放回输入框并进入编辑态——**不动历史**。编辑是"我可能改主意"，
  // 一点就把后面的对话毁掉是不可接受的；真正的撤回推迟到下次发送前。
  const handleEdit = useCallback((block: ThreadBlock) => {
    // busy 不再拦截（2026-12 拍板）：后端 Rewind 现在会自动取消当前轮并等它收尾，
    // 「停止本轮并编辑」是一步完成的合法操作（按钮 title 已说明）。
    const d = beginEdit(block);
    if (!d) {
      reportError("这条消息来自旧版后端（没有 seq），无法编辑");
      return;
    }
    setEditDraft(d);
    injectDraft(currentId, d.text);
  }, [currentId, injectDraft, reportError]);

  // 取消编辑：退出编辑态。输入框里的文本**留着**（用户可能还想发）——取消只是
  // 撤回"这条是编辑"的语义，下次发送就是一条普通的新消息。
  const handleCancelEdit = useCallback(() => setEditDraft(null), []);

  // 切会话必须退出编辑态：编辑锚点（seq）只在它所属的那个会话里有意义，带着它
  // 切走再发送会去撤回**另一个会话**的那条消息（静默删错历史——比不生效坏得多）。
  // 注入态（draft / attsDraft）同理一并清：它们是「当前会话」的一次性动作，注入
  // 的内容已实时同步进那个会话的草稿记账键，切回来由初始草稿还原；不清的话重挂
  // 载的新 Composer 会把上一个会话的注入草稿再吃一遍（串会话）。
  useEffect(() => {
    setEditDraft(null);
    setDraft(null);
    setAttsDraft(null);
  }, [currentId]);

  // 发送：编辑态下**先撤回再发**（这是"编辑"真正生效的时刻）。撤回失败就不发——
  // 历史没清干净就发，新消息会接在被编辑那条的后面，等于改了个寂寞；文本还给
  // 用户重试（Composer 的 submit 已经先清了输入框）。atts 随消息携带（图片批次 B），
  // 撤回失败同样把附件装回输入框。归属 sid 在点击那一刻捕获（撤回/回填都落原
  // 会话——应答异步，「当前会话」在回调里不可信）。
  const handleSend = useCallback((text: string, atts?: SendAttachments) => {
    const editing = editDraft;
    const sid = currentId;
    setEditDraft(null);
    if (!editing) {
      sendWithOptions(text, atts);
      return;
    }
    // busy 兜底（2026-12 拍板后语义变化：后端会自动取消当前轮并收尾，撤回不再
    // 必拒）——但**撤回 + 发送**合在一步时若后端返回 busy（例如取消后又有新一轮
    // 抢先开了），仍如实报错并把文本装回输入框，不静默丢弃编辑。
    void source.rewind(sid, editing.seq)
      .then(() => sendWithOptions(text, atts))
      .catch((e) => {
        reportError(`撤回失败，未发送: ${e instanceof Error ? e.message : String(e)}`);
        injectDraft(sid, text, atts);
      });
  }, [editDraft, source, currentId, sendWithOptions, reportError, injectDraft]);

  return {
    attsDraft,
    draft,
    drafts,
    editDraft,
    injectDraft,
    rollbackDraft,
    handleDraftChange,
    onSendFailed,
    syncBlocks,
    handleRewind,
    handleEdit,
    handleCancelEdit,
    handleSend,
  };
}

/** 当前会话的初始草稿（Composer 挂载还原——draftOf 的直通转发）。 */
export function initialDraftOf(drafts: SessionDrafts, currentId: string) {
  return draftOf(drafts, currentId);
}
