// 对话渲染块分发器：user 气泡 / assistant 回复 / 工具行 / 确认卡 /
// 任务清单 / 产物卡 / 错误条——各渲染器分文件实现（blocks/ 目录）。
// Block 用 memo：reduce 只给变化的块换新引用，未动的兄弟块跳过协调；
// 配合稳定的 onConfirm（useAgent/App 的 useCallback）生效。
import { memo, useEffect, useMemo, useRef } from "react";
import type { ThreadBlock } from "../../../shared/store";
import { ThinkingReasoning } from "../../../aicss/ThinkingReasoning";
import { TodoList } from "../../../aicss/TodoList";
import { ApprovalCard } from "../../../aicss/ApprovalCard";
import { playEnter } from "../../../shared/anim";
import { useSettings } from "../../../shared/settings";
import { Markdown } from "../../../shared/markdown";
import { prettyCommand, prettyCwd } from "../helpers";
import { ToolBlock } from "./ToolBlock";
import { FilesCard } from "./FilesCard";
import { AnswerBody } from "./AnswerBody";
import { DispatchCard } from "./DispatchCard";

export const Block = memo(function Block({ block, onConfirm }: { block: ThreadBlock; onConfirm: (id: string, allow: boolean) => void }) {
  const bubbleRef = useRef<HTMLDivElement>(null);
  const { settings } = useSettings();

  // 用户气泡入场：从 composer 方向的明显回弹（back.out），配合缓动滚底
  // 形成"发送出去"的手感
  useEffect(() => {
    if (block.kind === "user") {
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
      return (
        <div className="msg user">
          <div className="bubble" ref={bubbleRef}>{block.text}</div>
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

    case "todo":
      return <TodoList items={block.items} />;

    case "error":
      return (
        <div className="error-block" data-aborted={block.aborted ? "true" : undefined}>
          {block.message}
        </div>
      );
  }
});
