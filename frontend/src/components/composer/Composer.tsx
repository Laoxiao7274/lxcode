import { useEffect, useRef, useState } from "react";
import { Button } from "../form";
import { AgentPicker } from "../agents/AgentPicker";
import { PermPicker } from "../perm-picker";
import { ModelPicker } from "../model-picker";
import { ContextIndicator } from "../context-indicator";
import { StatsPills } from "../stats-pills";
import { TodoList } from "../../aicss/TodoList";
import { SlashPalette, type SlashCommand } from "./SlashPalette";
import { useEnterRef } from "../../shared/anim";
import { usePopover } from "../../shared/popover";
import {
  addPendingFiles,
  addPendingImages,
  attsFromPending,
  formatBytes,
  hasPendingAtts,
  isImageMime,
  MAX_FILE_BYTES,
  MAX_IMAGE_BYTES,
  removePending,
  type PendingFile,
  type PendingImage,
} from "../../shared/attachments";
import { routeSubmit, type SendQueueItem } from "../../shared/send-queue";
import type { SendAttachments, ContextUsage, SessionStats, TodoItem } from "../../shared/types";

/** 输入区（Codex 式）：busy 时输入框保留（可预输入），发送钮变停止。
 *  斜杠命令：输入以 / 开头时上方弹命令面板（关键字过滤 + 键盘导航 +
 *  Enter/Tab 补全——面板开着时 Enter 不发送）。
 *  任务清单卡浮在输入框正上方（与 busy 行同层——都在 composer 浮层内，
 *  不会被绝对定位的输入区遮挡；清单不进对话流）。
 *  附件（图片批次 B）：piBar 左侧「+」按钮弹出菜单（图片/文件，本地选择），
 *  输入区监听 paste（剪贴板图片走附件流程，文本粘贴照常）；暂存附件在输入框
 *  上方预览（缩略图/文件卡，点击 × 移除），随发送组装进 chat.send 的
 *  images/files 参数，busy 时入队**携带附件**。 */
/** 外部注入输入框的草稿（撤回/编辑把原文放回输入框）。
 *
 *  **用单调递增的 id 而不是按文本比较**：同一条消息连续撤回两次（或撤回后再编辑
 *  同一条）文本一模一样，按文本判重的话第二次是 no-op——用户看到"点了没反应"。
 *  atts 非空 = 同时把附件装回附件区（队列条目「编辑」的装回路径）。 */
export interface ComposerDraft {
  id: number;
  text: string;
  atts?: SendAttachments;
}

/** 外部注入的附件草稿（发送失败把附件装回附件区）：只动附件、不碰输入框文本
 *  ——失败时文本本来就没回填，用户可能已经在重新打字，不能覆盖。 */
export interface ComposerAttsDraft {
  id: number;
  atts: SendAttachments;
}

export function Composer({
  busy,
  sending = false,
  disabled,
  readOnly = false,
  todos = [],
  context = null,
  stats = null,
  onSend,
  onCancel,
  onCompact,
  commands = [],
  draft = null,
  editing = false,
  onCancelEdit,
  queue,
  queueNotice = "",
  onQueue,
  onQueueEdit,
  onQueueDelete,
  onQueueSend,
  attsDraft = null,
  visionBlocked = null,
}: {
  busy: boolean;
  /** 发送中态（乐观气泡已插、后端 busy 还没到——新会话首条消息的 git 链窗口）：
   *  发送钮禁用 + 状态行显示「发送中…」，busy 到达后无缝切到「生成中」。 */
  sending?: boolean;
  disabled?: boolean;
  /** 只读视图（子会话页）：版式与可交互模式**逐字节一致**，只是输入被锁——
   *  输入框禁用、发送不可用、三个选择器整组锁定（半透明 + 不响应指针）。
   *  上下文环与统计胶囊是读数（本就只读），保持可点开弹层。 */
  readOnly?: boolean;
  /** 任务清单（空数组不渲染卡片）。 */
  todos?: TodoItem[];
  /** 上下文占用（后端测量；null = 未知——指示器显示中性态）。 */
  context?: ContextUsage | null;
  /** 整段会话统计（后端折叠整段日志；null = 还没有任何一步——整行不渲染）。 */
  stats?: SessionStats | null;
  /** 发送（text 可为空——只附件不打字时后端以「[附件]」行充当正文）。 */
  onSend: (text: string, atts: SendAttachments) => void;
  onCancel: () => void;
  /** 手动压缩历史（空闲才可用；不传 = 指示器不显示入口）。 */
  onCompact?: () => void;
  /** 斜杠命令集（App 注入——页面导航；选择器聚焦命令由 piBar 控件自身
   *  的打开态承载，/model 等 = 聚焦后打开对应选择器的实现放命令集里）。 */
  commands?: SlashCommand[];
  /** 撤回/编辑把原文放回输入框（null = 没有待注入的草稿；atts = 附件一并装回）。 */
  draft?: ComposerDraft | null;
  /** 编辑态（正在编辑某条历史消息）——发送时会先撤回那条及其之后的对话。 */
  editing?: boolean;
  onCancelEdit?: () => void;
  /** 发送缓冲区（当前会话的队列；空/未传 = 不渲染队列区）。
   *  busy 时 chat.send 会被后端拒收（消息不入历史），submit 改为入队，
   *  会话空闲后由 store 的订阅层逐条自动发出。 */
  queue?: SendQueueItem[];
  /** 队列区顶部的一次性提示（如「已置顶，本轮结束后立即发送」；空 = 不显示）。 */
  queueNotice?: string;
  /** busy/sending 时 submit 的去向：入队（App → store.enqueueSend，附件随条目）。 */
  onQueue?: (text: string, atts: SendAttachments) => void;
  /** 队列条目「编辑」：把该条回填输入框并从队列移除（附件由 App 经 draft.atts 装回）。 */
  onQueueEdit?: (id: string) => void;
  /** 队列条目「删除」：从队列移除。 */
  onQueueDelete?: (id: string) => void;
  /** 队列条目「直接发送」：空闲立即发，忙时置顶（提示由 App 经 queueNotice 给）。 */
  onQueueSend?: (id: string) => void;
  /** 外部注入的附件（发送失败装回；null = 无）。 */
  attsDraft?: ComposerAttsDraft | null;
  /** 非空 = 当前模型未声明 vision 能力（值为人话原因）：「图片」入口禁用；
   *  null/undefined = 能力未知（老后端/模型没配）→ 不禁用，发送时依赖后端
   *  的 vision 校验人话错误兜底。文件入口不受影响。 */
  visionBlocked?: string | null;
}) {
  const [value, setValue] = useState("");
  const [images, setImages] = useState<PendingImage[]>([]);
  const [files, setFiles] = useState<PendingFile[]>([]);
  const [notice, setNotice] = useState("");
  const taRef = useRef<HTMLTextAreaElement>(null);
  const zoneRef = useRef<HTMLDivElement>(null);
  const imgInputRef = useRef<HTMLInputElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  /** 已注入的草稿 id（按 id 判重，不按文本——见 ComposerDraft 的注释）。 */
  const draftIdRef = useRef(0);
  const attsDraftIdRef = useRef(0);
  /** 暂存区的 ref 镜像（异步读文件期间 state 可能已变——追加要基于最新值）。 */
  const imagesRef = useRef(images);
  const filesRef = useRef(files);
  imagesRef.current = images;
  filesRef.current = files;
  /** 一次性提示的计时器（4s 自动消失，与队列提示同款节奏）。 */
  const noticeTimerRef = useRef<number | null>(null);
  // 「生成中」状态行挂载即上浮淡入（busy 翻转时才挂载/卸载）
  const busyRowRef = useEnterRef<HTMLDivElement>({ opacity: 0, y: 6 }, { opacity: 1, y: 0, duration: 0.26, ease: "power2.out", clearProps: "transform,opacity" });
  const locked = Boolean(readOnly) || Boolean(disabled);
  // 「+」按钮的弹出菜单（点外/Esc 关闭 + 退场动画——shared/popover 的通用接线）
  const addMenu = usePopover();
  // submit 的去向判定在 send-queue.ts 的 routeSubmit（纯函数，测试直测同一份）：
  // 空闲 → 正常发送路径；busy/sending → 入发送缓冲区（原先 busy 时 Enter 是
  // no-op 且输入文本已清空，被后端拒收的发送连文本都找不回来）。发送中（sending）
  // 不可直发：消息已经交出去，再点发送会插第二条乐观块并可能被后端以 busy 拒收。
  // hasAttachments：只附件没文本也允许发（后端以「[附件]」行充当正文）。
  const route = (text: string) => routeSubmit({ text, busy, sending, locked, hasQueue: Boolean(onQueue), hasAttachments: hasPendingAtts(images, files) });
  const canSend = route(value.trim()) === "send";

  // 斜杠面板：输入以 / 开头（单行——/ 出现在行中不算命令）时开
  const slashOpen = !locked && value.startsWith("/") && !value.includes("\n");
  const slashQuery = slashOpen ? value.replace(/^\/+/, "") : "";

  const flashNotice = (text: string) => {
    setNotice(text);
    if (noticeTimerRef.current !== null) window.clearTimeout(noticeTimerRef.current);
    noticeTimerRef.current = window.setTimeout(() => setNotice(""), 4000);
  };

  const submit = () => {
    const text = value.trim();
    const where = route(text);
    if (where === "none") return;
    // 附件组装（wire 形状，数组按引用——队列/发送参数共享，不复制 base64）
    const atts = attsFromPending(images, files);
    if (where === "queue") onQueue?.(text, atts);
    else onSend(text, atts);
    // 发出即清空附件区：发送失败时 App 经 attsDraft 把附件装回来
    //（与乐观气泡回滚同一拍——文本丢了附件不能丢）。
    setValue("");
    setImages([]);
    setFiles([]);
    requestAnimationFrame(() => taRef.current?.focus());
  };

  // 面板选中：清输入、跑动作、回焦输入框
  const pick = (c: SlashCommand) => {
    setValue("");
    c.run();
    requestAnimationFrame(() => taRef.current?.focus());
  };

  const onKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (slashOpen) return; // 面板接管键盘（palette 在 window capture 层处理）
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  // ---- 附件：本地选择 + 粘贴（共用一条异步读入路径） ----

  /** File → {base64, size}（异步——大图读入不卡 UI）。 */
  const readAsBase64 = (file: File) =>
    new Promise<string>((resolve, reject) => {
      const r = new FileReader();
      r.onload = () => {
        const result = String(r.result ?? "");
        const idx = result.indexOf(",");
        resolve(idx >= 0 ? result.slice(idx + 1) : result);
      };
      r.onerror = () => reject(r.error ?? new Error("读取文件失败"));
      r.readAsDataURL(file);
    });

  const ingest = async (list: File[]) => {
    if (list.length === 0) return;
    // 尺寸先于读取拦截：超大文件不读进内存（读 100MB 只为报错是浪费）
    const oversizeImg = list.find((f) => isImageMime(f.type) && f.size > MAX_IMAGE_BYTES);
    if (oversizeImg) {
      flashNotice(`图片 ${oversizeImg.name} 超过 5MB 上限（当前 ${formatBytes(oversizeImg.size)}）`);
      return;
    }
    const oversizeFile = list.find((f) => !isImageMime(f.type) && f.size > MAX_FILE_BYTES);
    if (oversizeFile) {
      flashNotice(`文件 ${oversizeFile.name} 超过 20MB 上限（当前 ${formatBytes(oversizeFile.size)}）`);
      return;
    }
    try {
      const imgs = list.filter((f) => isImageMime(f.type));
      const others = list.filter((f) => !isImageMime(f.type));
      if (imgs.length > 0) {
        const datas = await Promise.all(imgs.map(readAsBase64));
        // 追加基于 ref 镜像（await 期间用户可能又加了/删了附件）
        const res = addPendingImages(
          imagesRef.current,
          datas.map((data, i) => ({ mime: imgs[i].type, name: imgs[i].name, data })),
        );
        setImages(res.images);
        if (res.notice) flashNotice(res.notice);
      }
      if (others.length > 0) {
        const datas = await Promise.all(others.map(readAsBase64));
        const res = addPendingFiles(
          filesRef.current,
          datas.map((data, i) => ({ name: others[i].name, size: others[i].size, data })),
        );
        setFiles(res.files);
        if (res.notice) flashNotice(res.notice);
      }
    } catch (e) {
      flashNotice(`读取文件失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  };

  const pickFiles = (e: React.ChangeEvent<HTMLInputElement>) => {
    void ingest(Array.from(e.target.files ?? []));
    e.target.value = ""; // 允许重复选同一个文件
  };

  // 粘贴：剪贴板里的**图片文件**走附件流程（多张也支持）；文本粘贴照常不拦，
  // 非图片的剪贴板文件忽略（文件来源是「+」菜单的本地选择）。
  const onPaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    if (locked) return;
    const items = e.clipboardData?.items;
    if (!items) return;
    const imgs: File[] = [];
    for (const item of Array.from(items)) {
      if (item.kind !== "file") continue;
      const f = item.getAsFile();
      if (f && isImageMime(f.type)) imgs.push(f);
    }
    if (imgs.length === 0) return;
    e.preventDefault();
    void ingest(imgs);
  };

  const removeImage = (id: string) => setImages((cur) => removePending(cur, id));
  const removeFile = (id: string) => setFiles((cur) => removePending(cur, id));

  // 外部注入草稿（撤回/编辑把原文放回输入框）：写入 + 聚焦 + 光标停在末尾。
  // 光标必须到末尾——用户接着改的是这句话，停在开头或全选都别扭。
  // atts 非空 = 队列条目「编辑」的附件一并装回附件区。
  useEffect(() => {
    if (!draft || draft.id === draftIdRef.current) return;
    draftIdRef.current = draft.id;
    setValue(draft.text);
    if (draft.atts) {
      setImages(draft.atts.images.map((im, i) => ({ id: `img-restore-${draft.id}-${i}`, mime: im.mime, name: "", data: im.data })));
      setFiles(draft.atts.files.map((f, i) => ({ id: `file-restore-${draft.id}-${i}`, name: f.name, size: 0, data: f.data })));
    }
    requestAnimationFrame(() => {
      const ta = taRef.current;
      if (!ta) return;
      ta.focus();
      ta.setSelectionRange(ta.value.length, ta.value.length);
    });
  }, [draft]);

  // 外部注入附件（发送失败装回）：只动附件区，不碰输入框文本。
  useEffect(() => {
    if (!attsDraft || attsDraft.id === attsDraftIdRef.current) return;
    attsDraftIdRef.current = attsDraft.id;
    setImages(attsDraft.atts.images.map((im, i) => ({ id: `img-restore-${attsDraft.id}-${i}`, mime: im.mime, name: "", data: im.data })));
    setFiles(attsDraft.atts.files.map((f, i) => ({ id: `file-restore-${attsDraft.id}-${i}`, name: f.name, size: 0, data: f.data })));
  }, [attsDraft]);

  // 自增高（DSH 形态）：随内容长高到上限（CSS max-height 200px），之内
  // **不出内部滚动条**——多行输入在 44px 固定高度里滚是 ugliness 本身。
  // 到上限后才允许内部滚动。发送清空 / 注入草稿都走 value 变化 → 自动复位。
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    const max = 200;
    ta.style.height = Math.min(ta.scrollHeight, max) + "px";
    ta.style.overflowY = ta.scrollHeight > max ? "auto" : "hidden";
  }, [value]);

  // 输入区（含清单卡）的真实高度发布给所在工作区面板——线程区按它预留底部空间，
  // 清单展开多高就留多少：浮层永远不遮挡对话内容（把清单当输入区的一部分）。
  // 发布目标是**最近的工作区面板**而不是 .main：工作区面板是保活的（隐藏但不卸载，
  // App.WorkspaceViewPanels），聊天页与子会话页的 Composer 可能同时挂载——都发布
  // 到 .main 会互踩（子会话页的高度盖掉聊天页的，聊天底部留白错位）。
  useEffect(() => {
    const el = zoneRef.current;
    const panel = (el?.closest(".workspace-view-panel") ?? el?.closest(".main")) as HTMLElement | null;
    if (!el || !panel) return;
    const publish = () => panel.style.setProperty("--composer-h", el.offsetHeight + "px");
    const ro = new ResizeObserver(publish);
    ro.observe(el);
    publish();
    return () => {
      ro.disconnect();
      panel.style.removeProperty("--composer-h");
    };
  }, []);

  return (
    <div className="composer-zone" ref={zoneRef}>
      <div className="composer-inner">
        {/* 任务清单卡：输入框正上方（与 busy 行同层——浮层内不被遮挡） */}
        {todos.length > 0 && (
          <div className="composer-plan">
            <TodoList items={todos} />
          </div>
        )}
        {/* 发送缓冲区：busy 期间提交的消息排队在这里（等空闲自动发出）。
            每条一行：序号 + 单行摘要 + 附件标记 + 直接发送/编辑/删除三个动作。 */}
        {queue && queue.length > 0 && (
          <div className="send-queue" role="list" aria-label="发送缓冲区">
            {queueNotice && (
              <div className="send-queue-notice" role="status">{queueNotice}</div>
            )}
            {queue.map((item, index) => (
              <div className="send-queue-item" role="listitem" key={item.id}>
                <span className="send-queue-index" aria-hidden="true">{index + 1}</span>
                <span className="send-queue-text" title={item.text}>{clipOneLine(item.text, 80)}</span>
                {(item.images?.length || item.files?.length) ? (
                  <span className="send-queue-atts" aria-label={`附件：图片 ${item.images?.length ?? 0} 张，文件 ${item.files?.length ?? 0} 个`}>
                    {item.images?.map((im, i) => (
                      <AttachmentThumb key={i} mime={im.mime} data={im.data} alt="排队消息的图片附件" small />
                    ))}
                    {item.files?.map((f, i) => (
                      <span key={i} className="send-queue-file" title={f.name}>{clipOneLine(f.name, 18)}</span>
                    ))}
                  </span>
                ) : null}
                <button type="button" className="send-queue-act" title="直接发送" aria-label="直接发送该条" onClick={() => onQueueSend?.(item.id)}>
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M12 19V5" />
                    <path d="m5 12 7-7 7 7" />
                  </svg>
                </button>
                <button type="button" className="send-queue-act" title="编辑（放回输入框）" aria-label="编辑该条（放回输入框）" onClick={() => onQueueEdit?.(item.id)}>
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" />
                  </svg>
                </button>
                <button type="button" className="send-queue-act" title="删除" aria-label="删除该条" onClick={() => onQueueDelete?.(item.id)}>
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
                    <path d="M18 6 6 18M6 6l12 12" />
                  </svg>
                </button>
              </div>
            ))}
          </div>
        )}
        {/* 编辑态提示行：必须说清"发送会先撤回它及其之后的对话"——不写清楚
            用户会以为编辑只是改字，而历史会被清掉 */}
        {editing && (
          <div className="edit-row">
            <span className="edit-text">正在编辑这条消息 · 发送时会先撤回它及其之后的对话</span>
            <Button className="edit-cancel" onClick={onCancelEdit}>取消编辑</Button>
          </div>
        )}
        {/* busy/sending 状态行：浮在输入框上方（发送中 + 生成中共用一行——
            busy 到达后文案无缝从「发送中…」切到「生成中」） */}
        {(busy || sending) && (
          <div className="busy-row" ref={busyRowRef}>
            <span className="mset-spinner" aria-hidden />
            <span className="busy-text">{busy ? "生成中" : "发送中…"}</span>
          </div>
        )}
        {/* 附件预览区：贴输入框上方（队列区之下）。图片缩略图 + 文件卡，点 × 移除；
            一次性提示（超限拒绝等）也显示在这里。 */}
        {(images.length > 0 || files.length > 0 || notice) && (
          <div className="att-preview" role="region" aria-label="附件预览">
            {notice && <div className="att-notice" role="status">{notice}</div>}
            {images.length > 0 && (
              <div className="att-thumbs">
                {images.map((im) => (
                  <AttachmentThumb key={im.id} mime={im.mime} data={im.data} alt={`图片附件 ${im.name || ""}`} title={im.name || undefined} onRemove={() => removeImage(im.id)} />
                ))}
              </div>
            )}
            {files.length > 0 && (
              <div className="att-file-cards">
                {files.map((f) => (
                  <span key={f.id} className="att-file-card" title={f.name}>
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                      <path d="M14 2v6h6" />
                    </svg>
                    <span className="att-file-name">{clipOneLine(f.name, 24)}</span>
                    {f.size > 0 && <span className="att-file-size">{formatBytes(f.size)}</span>}
                    <button type="button" className="att-remove" title="移除该文件" aria-label={`移除文件 ${f.name}`} onClick={() => removeFile(f.id)}>
                      <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
                        <path d="M18 6 6 18M6 6l12 12" />
                      </svg>
                    </button>
                  </span>
                ))}
              </div>
            )}
          </div>
        )}
        <div className="pi">
          {slashOpen && (
            <SlashPalette
              query={slashQuery}
              commands={commands}
              onPick={pick}
              onClose={() => setValue("")}
            />
          )}
          <textarea
            ref={taRef}
            className="piInput"
            placeholder={locked ? "只读视图——回到主会话才能发消息" : busy ? "生成中… 可先输入，Enter 排队发送" : "让智能体构建、审查或解释点什么…"}
            rows={1}
            value={value}
            disabled={locked}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={onKey}
            onPaste={onPaste}
          />
          <div className="piBar">
            {/* 「+」添加附件：图片（受 vision 能力门控）/文件。子会话只读时不渲染
                （locked 与选择器同款语义——只读视图不该出现添加入口）。 */}
            {!locked && (
              <span className="pop-wrap att-add-wrap" ref={addMenu.rootRef}>
                <button
                  type="button"
                  className="att-add"
                  title="添加图片或文件"
                  aria-label="添加图片或文件"
                  aria-haspopup="menu"
                  aria-expanded={addMenu.open}
                  onClick={addMenu.toggle}
                >
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden="true">
                    <path d="M12 5v14M5 12h14" />
                  </svg>
                </button>
                {addMenu.open && (
                  <div className="att-menu" role="menu" data-pop>
                    <button
                      type="button"
                      className="att-menu-item"
                      role="menuitem"
                      disabled={Boolean(visionBlocked)}
                      title={visionBlocked ?? "从本地选择图片（png/jpeg/webp/gif，最多 4 张，单张 5MB 内）"}
                      onClick={() => { imgInputRef.current?.click(); addMenu.requestClose(); }}
                    >
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                        <rect x="3" y="3" width="18" height="18" rx="2" />
                        <circle cx="8.5" cy="8.5" r="1.5" />
                        <path d="m21 15-5-5L5 21" />
                      </svg>
                      图片
                    </button>
                    <button
                      type="button"
                      className="att-menu-item"
                      role="menuitem"
                      title="从本地选择文件（最多 4 个，单个 20MB 内；交给智能体读取）"
                      onClick={() => { fileInputRef.current?.click(); addMenu.requestClose(); }}
                    >
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                        <path d="M14 2v6h6" />
                      </svg>
                      文件
                    </button>
                  </div>
                )}
                {/* 隐藏的文件选择入口（菜单项触发；multiple 一次选多个） */}
                <input
                  ref={imgInputRef}
                  type="file"
                  accept="image/png,image/jpeg,image/webp,image/gif"
                  multiple
                  className="att-input"
                  aria-hidden="true"
                  tabIndex={-1}
                  onChange={pickFiles}
                />
                <input
                  ref={fileInputRef}
                  type="file"
                  multiple
                  className="att-input"
                  aria-hidden="true"
                  tabIndex={-1}
                  onChange={pickFiles}
                />
              </span>
            )}
            {/* 三个选择器包一组：子会话页只读时整组锁定（样式不变，只是不可交互）。
                ContextIndicator/StatsPills 是读数，留在组外保持可点开。 */}
            <span className={"pi-controls" + (readOnly ? " pi-locked" : "")}>
              <AgentPicker />
              <PermPicker />
              <ModelPicker />
            </span>
            <ContextIndicator usage={context} stats={stats} onCompact={onCompact} busy={busy} />
            {/* 会话统计胶囊（时间）：紧挨上下文环（2026-09-30 用户拍板）。累计消耗那一半
                并进了上下文环的「会话用量」弹层——输入条因此不再拥挤 */}
            <StatsPills stats={stats} />
            <span className="piTips" />
            {busy ? (
              <button type="button" className="send-btn stop" onClick={onCancel} aria-label="停止生成" title="停止生成">
                <svg width="11" height="11" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                  <rect x="5" y="5" width="14" height="14" rx="2.5" />
                </svg>
              </button>
            ) : (
              <button
                type="button"
                className="send-btn"
                disabled={!canSend}
                onClick={submit}
                aria-label="发送"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M12 19V5" />
                  <path d="m5 12 7-7 7 7" />
                </svg>
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

/** 附件缩略图（预览区与队列条目共用）：base64 → Blob → objectURL，挂载时创建、
 *  卸载/数据变化时 revoke（不泄漏）。小尺寸（small）用于队列行。 */
export function AttachmentThumb({ mime, data, alt, title, onRemove, small = false }: {
  mime: string;
  data: string;
  alt: string;
  title?: string;
  onRemove?: () => void;
  small?: boolean;
}) {
  const [url, setUrl] = useState("");
  useEffect(() => {
    // base64 → 二进制 → Blob（mime 随图）；objectURL 只在这里活，卸载即 revoke
    const bin = atob(data);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    const objectUrl = URL.createObjectURL(new Blob([bytes], { type: mime }));
    setUrl(objectUrl);
    return () => URL.revokeObjectURL(objectUrl);
  }, [mime, data]);
  return (
    <span className={"att-thumb" + (small ? " att-thumb-small" : "")}>
      {url && <img src={url} alt={alt} title={title} />}
      {onRemove && (
        <button type="button" className="att-remove" title="移除该图片" aria-label={alt ? `移除 ${alt}` : "移除该图片"} onClick={onRemove}>
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
            <path d="M18 6 6 18M6 6l12 12" />
          </svg>
        </button>
      )}
    </span>
  );
}

/** 队列条目/文件卡的单行摘要：折叠空白 + 按码点截断（与 workspace-tabs.ts 的
 *  clipOneLine 同一份算法、不同预算；不共享是刻意的不引依赖：组件层不进 shared）。 */
function clipOneLine(text: string, max: number): string {
  const one = text.replace(/\s+/g, " ").trim();
  const chars = [...one];
  return chars.length <= max ? one : chars.slice(0, max).join("") + "…";
}
