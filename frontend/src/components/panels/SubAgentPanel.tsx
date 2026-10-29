// 右侧「子 Agent 执行」面板：把时间线里的派发卡列成可跳转的清单，让用户在长会话里
// 不用翻找就知道子 Agent 跑到哪一步了。
//
// 与 OutlinePanel 同构：这一层只做展示（条目判定在 ./dispatch-list，纯函数有测试钉住），
// 跳转的副作用（扩窗 + 滚 + 高亮）由 App 传进来——组件不碰 DOM、不碰历史。
import { Button } from "../form";
import { outlineLabel } from "./outline";
import type { DispatchItem } from "./dispatch-list";

export function SubAgentPanel({
  items,
  activeUid,
  onJump,
}: {
  items: DispatchItem[];
  /** 当前高亮项（最近跳转或最近一次派发；null = 还没有可高亮的）。 */
  activeUid: number | null;
  /** 点击条目 → 跳到时间线里那张派发卡（uid 是 Thread 挂的 data-uid）。 */
  onJump: (uid: number) => void;
}) {
  return (
    <div className="subagent-panel">
      {items.length === 0 ? (
        // 空态一句话，不留空框：这次会话还没派发过子 Agent 时面板不该像坏掉了
        <div className="subagent-empty">还没有派发子 Agent</div>
      ) : (
        <div className="subagent-list">
          {items.map((item) => {
            // 三态 → 一个 data-state（CSS 按它上色）：运行中 / 完成 / 失败
            const state = item.status === "running" ? "running" : item.isError ? "error" : "done";
            return (
              <Button
                key={item.uid}
                className={"subagent-item" + (item.uid === activeUid ? " active" : "")}
                data-state={state}
                // 悬停显示完整任务（列表里是截断后的单行摘要）
                title={item.task}
                onClick={() => onJump(item.uid)}
              >
                <span className="subagent-top">
                  {/* 色点：与时间线卡片同款（agentColor 内联，缺席时用 CSS 的中性色） */}
                  <span className="subagent-dot" style={{ background: item.agentColor }} aria-hidden />
                  <span className="subagent-agent">{item.agentName}</span>
                  <span className="subagent-state">
                    {state === "running" ? (
                      <>
                        <span className="mset-spinner" aria-hidden />
                        执行中
                      </>
                    ) : state === "error" ? (
                      "✗ 失败"
                    ) : (
                      "✓ 完成"
                    )}
                    {/* 有用量才显示——没有就不占位（老后端不带 usageTokens） */}
                    {item.usageTokens ? <span className="subagent-tokens mono">{item.usageTokens} tk</span> : null}
                  </span>
                </span>
                {/* 任务摘要：单行截断。复用大纲的单行折叠函数——同一件事不要有第二份实现
                 *  （换行/制表符折叠 + 按码点截断，emoji 不会被切成半个字符）。 */}
                <span className="subagent-task">{outlineLabel(item.task, 60)}</span>
              </Button>
            );
          })}
        </div>
      )}
    </div>
  );
}
