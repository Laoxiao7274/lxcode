// 子会话标签页：把一张 dispatch 卡上的子会话作为**独立工作区标签**打开的完整视图。
//
// 子 Agent = 独立会话（AGENTS.md §2.3）：它有自己的 messages、自己的压缩检查点。
// 所以这一页读的就是**会话历史**——与 DispatchCard 卡内那段子时间线是同一份数据，
// 两条路径都走 source.childHistory + **historyBlocks**（唯一一份映射）。
// 在这里写第二份映射的代价是同一段历史在卡里与标签页里换一张脸——那正是前两个 bug 的成因。
//
// 只读：这是子 Agent 自己的对话，用户没有要求改它。撤回/编辑**不是**"传 undefined 让它
// 静默失效"，而是整条动作条不渲染（Block 的 readOnly），只读语义在页头说明一次。
import { useCallback, useEffect, useRef, useState } from "react";
import type { AgentSource } from "../../shared/types";
import type { ThreadBlock } from "../../shared/store";
import { historyBlocks } from "../../shared/history";
import { Button } from "../form";
import { Block } from "../thread/blocks/Block";
import { ScrollToBottom } from "../thread/ScrollToBottom";

export function ChildSessionPage({
  sessionId,
  source,
  onBack,
}: {
  /** 子会话 id（标签键 child:<sessionId> 解析出来的那个）。 */
  sessionId: string;
  /** 数据源：childHistory 按子会话 id 寻址（后端 chat.history 已支持）。 */
  source: AgentSource;
  /** 返回主会话（标签栏的「聊天」标签）。 */
  onBack: () => void;
}) {
  const [blocks, setBlocks] = useState<ThreadBlock[]>([]);
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  // 重试计数：失败后点「重试」要能重新触发 effect（状态没变也能再试一次）
  const [nonce, setNonce] = useState(0);
  // source 放 ref：App 重建回调身份不该重新发请求（与 DispatchCard 同款纪律）
  const sourceRef = useRef(source);
  sourceRef.current = source;

  useEffect(() => {
    // alive：切标签/卸载后迟到的应答不许再写 state（会画到别的子会话页上）
    let alive = true;
    setStatus("loading");
    setError("");
    sourceRef.current.childHistory(sessionId).then(
      (snapshot) => {
        if (!alive) return;
        // **同一份映射**：与主时间线回放（reduceHistory）、DispatchCard 卡内子时间线共用
        setBlocks(historyBlocks(snapshot));
        setStatus("ready");
      },
      (e: unknown) => {
        if (!alive) return;
        // 失败必须说话：静默降级成"空历史"是编结论（用户会以为子会话本来就是空的）
        setError(e instanceof Error ? e.message : String(e));
        setStatus("error");
      },
    );
    return () => { alive = false; };
  }, [sessionId, nonce]);

  /** 确认门：子会话的确认由父会话代理（AGENTS.md §2.3），这一页正常不该出现挂起确认；
   *  万一出现，按钮必须真的能用——点了没反应比没有按钮更糟。ApprovalCard 自己会定格显示裁决。
   *  失败进**独立的提示行**而不是把整页换成错误态：历史已经读出来了，不该被一次裁决失败抹掉。 */
  const handleConfirm = useCallback((id: string, allow: boolean) => {
    void sourceRef.current.confirm(sessionId, id, allow).catch((e: unknown) => {
      setNotice(`裁决失败: ${e instanceof Error ? e.message : String(e)}`);
    });
  }, [sessionId]);

  return (
    <div className="child-session-page">
      <div className="child-session-head">
        <span className="child-session-title">子会话</span>
        <span className="child-session-id mono" title={sessionId}>{sessionId}</span>
        {/* 只读语义说明一次（页头）——而不是在每条消息上重复禁用原因：
         *  禁用按钮的 title 提示在多数浏览器里不弹（禁用元素不派发鼠标事件）。 */}
        <span className="child-session-readonly">只读 · 这是子 Agent 自己的会话，不提供撤回/编辑</span>
        <Button className="child-session-back" onClick={onBack}>返回主会话</Button>
      </div>
      {notice !== "" && (
        <div className="child-session-note error" role="alert">{notice}</div>
      )}
      <div className="child-session-body">
        {status === "loading" && <div className="child-session-note">正在读取子会话历史…</div>}
        {status === "error" && (
          <div className="child-session-note error" role="alert">
            <span className="child-session-msg">读不到子会话历史：{error}</span>
            <Button className="child-session-retry" onClick={() => setNonce((n) => n + 1)}>重试</Button>
          </div>
        )}
        {status === "ready" && blocks.length === 0 && (
          <div className="child-session-note">这个子会话没有可显示的历史（它可能还没开始执行，或历史已被清空）。</div>
        )}
        {status === "ready" && blocks.length > 0 && (
          <>
            {/* 复用 .thread（主时间线的容器类：块间距、720px 居中、左右 24px 内边距）——
             *  同一个视觉语言，不是第二套排版 */}
            <div className="thread">
              {blocks.map((block) => (
                // readOnly：不渲染用户气泡的动作条（撤回/编辑在这里没有意义，见 Block 的说明）
                <Block key={block.uid} block={block} onConfirm={handleConfirm} readOnly />
              ))}
            </div>
            {/* 回到底部：与主时间线**共用同一份组件**（自己找滚动祖先 = 这里的
             *  .child-session-body）。各写一份的下场是修了一处漏一处。 */}
            <ScrollToBottom />
          </>
        )}
      </div>
    </div>
  );
}
