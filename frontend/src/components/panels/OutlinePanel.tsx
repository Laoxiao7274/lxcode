// 右侧「已发送消息」大纲面板：长会话里把自己发过的消息列成可跳转的单行清单。
//
// 这一层只做展示：条目判定与截断全在 ./outline（纯函数，有测试钉住），跳转的
// 副作用（querySelector + scrollIntoView + 高亮）由 App 传进来——组件不碰 DOM、
// 不碰历史（与 Thread 只做透传的分工一致）。
import { Button } from "../form";
import { outlineLabel, type OutlineItem } from "./outline";

export function OutlinePanel({
  items,
  activeUid,
  onJump,
}: {
  items: OutlineItem[];
  /** 当前高亮项（最近发送或最近跳转的那条；null = 还没有可高亮的）。 */
  activeUid: number | null;
  /** 点击条目 → 跳到对应块（uid 是 Thread 挂的 data-uid）。 */
  onJump: (uid: number) => void;
}) {
  return (
    <div className="outline-panel">
      {items.length === 0 ? (
        // 空态一句话，不留空框：新会话还没发过消息时面板不该像坏掉了
        <div className="outline-empty">还没有发过消息</div>
      ) : (
        <div className="outline-list">
          {items.map((item) => (
            <Button
              key={item.uid}
              className={"outline-item" + (item.uid === activeUid ? " active" : "")}
              // 悬停显示原文（列表里是截断后的单行摘要）
              title={item.text}
              onClick={() => onJump(item.uid)}
            >
              {outlineLabel(item.text)}
            </Button>
          ))}
        </div>
      )}
    </div>
  );
}
