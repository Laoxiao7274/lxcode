// 工作区视图的**浏览器式导航历史**（鼠标侧键后退/前进用，2026-10-07 用户要求）。
//
// 与 WorkspaceTabsState.history 的区别：那份是「关闭当前标签后的回退」（最近焦点
// 优先，条目会随关闭被摘掉），这份是**时间线**——你去过哪一页、按顺序退回去、还能
// 前进回来。两份语义不同，合用一份会把「关标签的回退」与「浏览历史」搅在一起。
//
// 纯函数（便于 node:test 就地测试，不必渲染 React）。
import type { WorkspaceView } from "./workspace-tabs";

export interface ViewHistory {
  /** 最早 → 最近（不含 present）。 */
  past: WorkspaceView[];
  present: WorkspaceView;
  /** 最近 → 最早（后退压回来的）。 */
  future: WorkspaceView[];
}

/** 上限：历史不是收藏夹，退不到头的记忆没有价值（浏览器也没有）。 */
export const VIEW_HISTORY_LIMIT = 50;

export function initialViewHistory(start: WorkspaceView = "chat"): ViewHistory {
  return { past: [], present: start, future: [] };
}

/** 导航到新视图（UI 点击驱动）：连续相同的视图去重（重复点同一个标签不是"去了一页"）；
 *  从中间退回去再点新页 = 浏览器语义，前进栈整段作废。 */
export function pushView(h: ViewHistory, view: WorkspaceView): ViewHistory {
  if (view === h.present) return h;
  const past = [...h.past, h.present];
  return {
    past: past.length > VIEW_HISTORY_LIMIT ? past.slice(past.length - VIEW_HISTORY_LIMIT) : past,
    present: view,
    future: [],
  };
}

/** 后退/前进一步。返回下一步的视图与新历史；无处可退/可进返回 null（调用方原样不动）。
 *
 *  **不可达的条目跳过并丢弃**：历史里记着的子会话标签可能已被关闭——按浏览器惯例，
 *  死条目不挡路（继续往更早找），也不会被"复活"（悄悄重开一个用户关掉的标签页是惊吓）。 */
export function stepView(
  h: ViewHistory,
  dir: "back" | "forward",
  isAlive: (view: WorkspaceView) => boolean,
): { history: ViewHistory; view: WorkspaceView } | null {
  if (dir === "back") {
    for (let i = h.past.length - 1; i >= 0; i--) {
      const view = h.past[i];
      if (!isAlive(view)) continue;
      return {
        history: {
          past: h.past.slice(0, i),
          present: view,
          future: [h.present, ...h.future],
        },
        view,
      };
    }
    return null;
  }
  for (let i = 0; i < h.future.length; i++) {
    const view = h.future[i];
    if (!isAlive(view)) continue;
    return {
      history: {
        past: [...h.past, h.present],
        present: view,
        future: h.future.slice(i + 1),
      },
      view,
    };
  }
  return null;
}
