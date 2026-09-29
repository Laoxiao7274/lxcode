// 对话渲染块分发器：user 气泡 / assistant 回复 / 工具行 / 确认卡 /
// 任务清单 / 产物卡 / 错误条——各渲染器分文件实现（blocks/ 目录）。
// Block 用 memo：reduce 只给变化的块换新引用，未动的兄弟块跳过协调；
// 配合稳定的 onConfirm（useAgent/App 的 useCallback）生效。
import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ThreadBlock } from "../../../shared/store";
import { canRewind } from "../../../shared/blocks";
import { Button } from "../../form";
import { ThinkingReasoning } from "../../../aicss/ThinkingReasoning";
import { ApprovalCard } from "../../../aicss/ApprovalCard";
import { playEnter } from "../../../shared/anim";
import { useSettings } from "../../../shared/settings";
import { Markdown } from "../../../shared/markdown";
import { prettyCommand, prettyCwd } from "../helpers";
import { ToolBlock } from "./ToolBlock";
import { FilesCard } from "./FilesCard";
import { AnswerBody } from "./AnswerBody";
import { DispatchCard } from "./DispatchCard";
import { CompactionCard } from "./CompactionCard";
import { JobCard } from "./JobCard";
import { NoticeBar } from "./NoticeBar";

/** 没有 seq 的块（老后端 / 更早落库的历史）不能撤回/编辑时的说明——禁用按钮上
 *  的 title。写清楚原因而不是默默点不动：用户至少知道为什么。 */
const NO_SEQ_HINT = "这条消息来自旧版后端（没有 seq），无法定位要撤回的位置";

export const Block = memo(
  function Block({ block, onConfirm, replayed, onEdit, onRewind }: {
    block: ThreadBlock;
    onConfirm: (id: string, allow: boolean) => void;
    replayed?: boolean;
    /** 用户气泡的撤回/编辑（复制在组件内自足，不必上抛）。回调由 App 用
     *  useCallback 固定身份——每次新建会击穿 memo（见文件末尾的比较器）。 */
    onEdit?: (block: ThreadBlock) => void;
    onRewind?: (block: ThreadBlock) => void;
  }) {
  const bubbleRef = useRef<HTMLDivElement>(null);
  const { settings } = useSettings();

  // 用户气泡入场：从 composer 方向的明显回弹（back.out），配合缓动滚底
  // 形成"发送出去"的手感。
  // **必须用 useLayoutEffect**：useEffect 在浏览器绘制之后才跑，gsap 的
  // fromTo 会把已经画出来的气泡再拽回 opacity:0/scale:0.93 重播一遍——帧级
  // 实测（进入会话时）气泡先满不透明度画出一帧，30ms 后跳到 0.26 再淡入，
  // 这就是用户看到的第二下闪。layout effect 在绘制前落 from 态，首帧即起点。
  // **replayed（换会话/冷启动的回放）不播**：那不是"刚发出"，一个历史气泡
  // 弹一下既不表达任何东西，又是每个块一次 gsap 计算样式读取（实测进入项目
  // 会话时这一帧的主线程长任务里 gsap 占大头）。依赖只有 block.kind，所以
  // 这里读到的是**挂载那一刻**的 replayed——正是我们要的语义。
  useLayoutEffect(() => {
    if (block.kind === "user" && !replayed) {
      playEnter(
        bubbleRef.current,
        { y: 14, opacity: 0, scale: 0.93 },
        { y: 0, opacity: 1, scale: 1, duration: 0.5, ease: "back.out(1.8)", transformOrigin: "right bottom", clearProps: "transform" },
      );
    }
  }, [block.kind]);

  // 思考句数组只在 reasoning 字符串真的变了才重切——正文打字机流式阶段
  // 该块每帧都重渲染（块对象每 delta 换新），不锁字符串就每帧 split 一遍
  const reasoningText = block.kind === "assistant" ? block.reasoning : "";
  const sentences = useMemo(
    () => (reasoningText ? reasoningText.split("\n").filter(Boolean) : null),
    [reasoningText],
  );

  switch (block.kind) {
    case "user":
      // 动作条与气泡**并列**（不进流）：.msg.user 是 flex 容器且右对齐，动作条
      // 绝对定位在气泡**上方**（right:0 与气泡右缘对齐）——进流会给每条用户消息
      // 永久多留一行高度，hover 显隐还会顶动下方内容（长会话里整屏位移）。
      return (
        <div className="msg user">
          <div className="bubble" ref={bubbleRef}>{block.text}</div>
          <MessageActions block={block} onEdit={onEdit} onRewind={onRewind} />
        </div>
      );

    case "assistant":
      // DSH 形态：无角色标签行（对话流 = user 气泡 + assistant 内容）。
      // 思考链 + 正文 + 轮末 usage。
      return (
        <div className="msg">
          {settings.showThinking && sentences && (
            <ThinkingReasoning
              sentences={sentences}
              phase={block.streaming ? "thinking" : "done"}
              elapsedMs={4200}
            />
          )}
          {block.content === "" ? null : <AnswerBody text={block.content} streaming={block.streaming} />}
          {!block.streaming && block.usageTokens ? (
            <div className="usage-line">已完成 · {block.usageTokens} tokens</div>
          ) : null}
        </div>
      );

    case "tool":
      return <ToolBlock block={block} />;

    case "files":
      return <FilesCard files={block.files} />;

    case "dispatch":
      return <DispatchCard block={block} onConfirm={onConfirm} />;

    case "compacted":
      return <CompactionCard block={block} />;

    case "job":
      return <JobCard block={block} />;

    case "notice":
      return <NoticeBar block={block} />;

    case "confirm":
      return (
        <ApprovalCard
          command={prettyCommand(block.request)}
          cwd={prettyCwd(block.request)}
          resolved={block.resolved ?? null}
          autoFocus={!block.resolved}
          onDecide={(allow) => onConfirm(block.request.id, allow)}
        />
      );

    case "error":
      return (
        <div className="error-block" data-aborted={block.aborted ? "true" : undefined}>
          {block.message}
        </div>
      );
  }
},
  // 比较器**故意不看 replayed**：它只在挂载那一刻有意义（见上面的 layout effect），
  // 而进会话后它就从 true 翻成 false——默认的浅比较会让整屏几十个块为此重渲染一遍
  // （markdown 重解析 + 协调），而这次翻转不表达任何新内容。
  (prev, next) =>
    prev.block === next.block &&
    prev.onConfirm === next.onConfirm &&
    prev.onEdit === next.onEdit &&
    prev.onRewind === next.onRewind,
);

/** 用户气泡上的三个动作（hover 出现）：复制 / 编辑 / 撤回。
 *
 *  单独一个子组件是为了**在 switch 的分支之外合法地用 useState**——复制的「已复制」
 *  反馈要组件态，而 Block 的 switch 里不能按分支调 hook。
 *
 *  复制走与 DiffBody / TerminalCard / CopyBtn 同一套写法（navigator.clipboard 的
 *  可选链 + 1.2s 文案反馈，不弹 toast）。没有 seq 的块把撤回/编辑**禁用**并给出
 *  原因（title）：老后端 + 新前端不许炸（AGENTS.md §5 坑 11）——复制照常可用。 */
function MessageActions({ block, onEdit, onRewind }: {
  block: ThreadBlock;
  onEdit?: (block: ThreadBlock) => void;
  onRewind?: (block: ThreadBlock) => void;
}) {
  const [copied, setCopied] = useState(false);
  const rewindable = canRewind(block);
  const text = block.kind === "user" ? block.text : "";
  const copy = () => {
    if (copied) return;
    // 可选链会短路整条链：navigator.clipboard 缺席时不会去调 undefined.then
    void navigator.clipboard?.writeText(text).then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1200);
    });
  };
  return (
    <div className="msg-actions">
      <Button className="msg-action" onClick={copy}>{copied ? "已复制" : "复制"}</Button>
      <Button
        className="msg-action"
        disabled={!rewindable}
        title={rewindable ? "编辑这条消息（发送时才会撤回它及其之后的对话）" : NO_SEQ_HINT}
        onClick={() => onEdit?.(block)}
      >
        编辑
      </Button>
      <Button
        className="msg-action"
        disabled={!rewindable}
        title={rewindable ? "撤回这条及其之后的全部对话，原文回到输入框" : NO_SEQ_HINT}
        onClick={() => onRewind?.(block)}
      >
        撤回
      </Button>
    </div>
  );
}
