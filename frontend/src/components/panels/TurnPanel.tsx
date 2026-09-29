// 右侧「轮次」面板：把「我说过什么」与「子 Agent 跑到哪」合并成一棵按轮次分组的树
// ——一条用户消息开一轮，它派出去的子 Agent 都挂在那一轮下面。
//
// 为什么合并（原先并排两个平铺列表）：子 Agent 列表里看不出某次派发属于哪一轮，用户原话
// 「不方便找到对应的子 Agent」；两个各自滚动的平铺列表并排也显得别扭。分组把「哪一轮」变成
// 结构（组头 + 组内行），而不是靠时间顺序去猜。
//
// 这一层只做展示：分组判定与单行摘要在 ./turns 与 ./outline（纯函数，有测试钉住），跳转的
// 副作用（扩窗 + 滚 + 高亮）由 App 传进来——组件不碰 DOM、不碰历史（与 Thread 只做透传的
// 分工一致）。
import { useEffect, useRef } from "react";
import { Button } from "../form";
import { outlineLabel } from "./outline";
import type { TurnGroup } from "./turns";

export function TurnPanel({
  groups,
  activeUid,
  onJump,
}: {
  groups: TurnGroup[];
  /** 当前高亮项（最近发送或最近跳转的那个块；null = 还没有可高亮的）。它可能落在组头
   *  （user 块）上，也可能落在组内某个子 Agent 上——两种都要认。 */
  activeUid: number | null;
  /** 点击行 → 跳到对应块（uid 是 Thread 挂的 data-uid）。 */
  onJump: (uid: number) => void;
}) {
  const listRef = useRef<HTMLDivElement>(null);
  // 当前轮：activeUid 落在组头或组内任一子 Agent 上，都算「这一轮是当前轮」——高亮与
  // 滚动跟随必须按组判定，否则点了组内的子 Agent 只有那一行亮、侧栏不知道该滚到哪。
  const activeTurn = (() => {
    if (activeUid === null) return null;
    const hit = groups.find((g) => g.uid === activeUid || g.agents.some((a) => a.uid === activeUid));
    return hit ? hit.turn : null;
  })();
  // 滚动跟随：**只在当前轮次变化时**把它滚进可见区。
  // block: "nearest" 而不是 "center"——用户只是滚动时间线时当前轮会一路变化，center 会把
  // 侧栏每变一次就重新居中一次（面板自己在抖，用户手动滚到的位置被反复拽走）；nearest 对
  // 已经可见的组是 no-op，只在它跑出可见区时才动。
  useEffect(() => {
    if (activeTurn === null) return;
    const el = listRef.current?.querySelector(`[data-turn="${activeTurn}"]`);
    el?.scrollIntoView({ block: "nearest" });
  }, [activeTurn]);

  if (groups.length === 0) {
    // 空态一句话，不留空框：新会话还没发过消息时面板不该像坏掉了
    return (
      <div className="turn-panel">
        <div className="turn-empty">还没有发过消息</div>
      </div>
    );
  }

  return (
    <div className="turn-panel">
      <div className="turn-list" ref={listRef}>
        {groups.map((group) => {
          // 组头的锚点先落到局部 const：属性访问的收窄在闭包里不成立（TS 会认为
          // group.uid 可能已经变了），局部 const 的收窄才跟着闭包走。
          const headUid = group.uid;
          return (
            // 分组本体：左侧导轨线（CSS 的 ::before）把组头与其下的子 Agent 串成一组
            <section className="turn-group" key={headUid ?? `before-${group.turn}`} data-turn={group.turn}>
              {headUid !== null ? (
                // 组头 = 那条用户消息：点它跳到那条消息（与旧大纲同一个跳转入口）
                <Button
                  className="turn-row turn-head"
                  data-active={headUid === activeUid ? "true" : "false"}
                  // 悬停显示原文（列表里是截断后的单行摘要）
                  title={group.text}
                  onClick={() => onJump(headUid)}
                >
                  <span className="turn-badge">{group.turn}</span>
                  <span className="turn-label">{group.label}</span>
                </Button>
              ) : (
                // 第一条 user 之前的派发：没有可跳的锚点 → 不做成按钮（点它没有目标，
                // 做成按钮就是骗键盘用户按一次回车什么都没发生）
                <div className="turn-row turn-head-pre">
                  <span className="turn-badge">{group.turn}</span>
                  <span className="turn-label">会话开始</span>
                </div>
              )}
              {group.agents.map((item) => {
                // 三态 → 一个 data-state（CSS 按它上色）：运行中 / 完成 / 失败
                const state = item.status === "running" ? "running" : item.isError ? "error" : "done";
                return (
                  <Button
                    key={item.uid}
                    className="turn-row turn-agent"
                    data-state={state}
                    data-active={item.uid === activeUid ? "true" : "false"}
                    // 悬停显示完整任务（列表里是截断后的单行摘要）
                    title={item.task}
                    onClick={() => onJump(item.uid)}
                  >
                    {/* 色点：与时间线卡片同款（agentColor 内联，缺席时用 CSS 的中性色） */}
                    <span className="turn-dot" style={item.agentColor ? { background: item.agentColor } : undefined} aria-hidden />
                    <span className="turn-agent-name">{item.agentName}</span>
                    {/* 状态：颜色 + 形状 + 文字三通道（颜色不能是唯一的信息通道——色盲/灰度屏
                     *  下「执行中/完成/失败」靠脉冲点、对勾、叉号仍然分得开） */}
                    <span className="turn-state">
                      {state === "running" ? (
                        <>
                          <span className="turn-pulse" aria-hidden />
                          执行中
                        </>
                      ) : state === "error" ? (
                        "✗ 失败"
                      ) : (
                        "✓ 完成"
                      )}
                    </span>
                    {/* 任务摘要：单行截断（完整原文在 title 里）。复用大纲的单行折叠函数——
                     *  同一件事不要有第二份实现（换行/制表符折叠 + 按码点截断）。任务描述可能
                     *  残缺（历史来自 SQLite）：outlineLabel 直接 .replace 会抛，收窄成字符串再给。 */}
                    <span className="turn-task">{outlineLabel(item.task ?? "", 40)}</span>
                  </Button>
                );
              })}
            </section>
          );
        })}
      </div>
    </div>
  );
}
