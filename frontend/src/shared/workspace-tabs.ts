export type WorkspacePage = "agents" | "catalog" | "git";
export type WorkspaceView = "chat" | WorkspacePage;

export interface WorkspaceTabsState {
  /** 按打开顺序排列；聚焦不会改变顺序。 */
  tabs: WorkspacePage[];
  active: WorkspaceView;
  /** 最近焦点优先，用于关闭当前标签后的回退。 */
  history: WorkspaceView[];
}

export function createWorkspaceTabs(): WorkspaceTabsState {
  return { tabs: [], active: "chat", history: [] };
}

export function focusWorkspacePage(state: WorkspaceTabsState, page: WorkspacePage): WorkspaceTabsState {
  const tabs = state.tabs.includes(page) ? state.tabs : [...state.tabs, page];
  return focusView(state, page, tabs);
}

export function focusChatTab(state: WorkspaceTabsState): WorkspaceTabsState {
  return focusView(state, "chat", state.tabs);
}

export function closeWorkspacePage(state: WorkspaceTabsState, page: WorkspacePage): WorkspaceTabsState {
  if (!state.tabs.includes(page)) return state;

  const tabs = state.tabs.filter((item) => item !== page);
  const history = state.history.filter((item) => item === "chat" || (item !== page && tabs.includes(item)));
  if (state.active !== page) return { ...state, tabs, history };

  const active = history.find((item) => item === "chat" || tabs.includes(item)) ?? "chat";
  return {
    tabs,
    active,
    history: history.filter((item) => item !== active),
  };
}

function focusView(state: WorkspaceTabsState, active: WorkspaceView, tabs: WorkspacePage[]): WorkspaceTabsState {
  if (state.active === active) return { ...state, tabs };
  return {
    tabs,
    active,
    history: [state.active, ...state.history.filter((item) => item !== state.active && item !== active)],
  };
}
