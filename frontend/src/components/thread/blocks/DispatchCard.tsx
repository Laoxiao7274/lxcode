// 主 Agent 调度子 Agent 的卡（agent_dispatch 的渲染形态——M3 语义的
// 前端部分）：Agent 身份头（色点 + 名 + 任务摘要）+ 状态（运行中 spinner/
// 已完成）+ 子执行过程（嵌套块缩进——子上下文隔离）+ 最终结果（验收）。
// 子块的事件流已按 dispatchId 归属到 subBlocks（store 的归属路由）。
import { useRef, useState } from "react";
import type { ThreadBlock } from "../../../shared/store";
import { AGENT_COLORS, useAgents } from "../../../shared/agents";
import { useEnterRef } from "../../../shared/anim";
import { Markdown } from "../../../shared/markdown";
import { Button } from "../../form";
import { Block } from "./Block";

/** agentId 也查不到时的名字（历史里参数坏了——比如 arguments 是截断的 JSON）。
 *  **不许留空白**：空白的卡头让用户不知道是谁在干活，比一个明确的"未知"更糟。 */
const UNKNOWN_AGENT = "未知 Agent";

export function DispatchCard({ block, onConfirm, "data-uid": dataUid }: {
  block: Extract<ThreadBlock, { kind: "dispatch" }>;
  onConfirm: (id: string, allow: boolean) => void;
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
  const subCount = block.subBlocks.length;
  // 可展开 = 有子过程，或有最终结果（结果也在折叠区内——用户报告
  // 「自动收缩和手动都收不掉最终结果」，根因是结果原本渲染在折叠区之外）
  const expandable = subCount > 0 || Boolean(done && block.result);

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
          {block.subBlocks.map((sub) => (
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
