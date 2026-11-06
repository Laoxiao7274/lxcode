// 顶层模式切换（助手模式 P2）：Topbar 左区的 project/assistant 双态开关。
//
// no-drag 豁免：.topbar 是 -webkit-app-region: drag（窗口拖拽区），按钮必须
// 豁免出来才可点击（topbar.css 的 .conn-pill/.win-btn 同一条纪律——本组件的
// .mode-switch 也走同款，样式在 assistant-mode.css）。
import { memo } from "react";
import type { AppMode } from "../../shared/app-mode";

export const AppModeSwitch = memo(function AppModeSwitch({
  mode,
  onChange,
}: {
  mode: AppMode;
  onChange: (mode: AppMode) => void;
}) {
  return (
    <div className="mode-switch" role="tablist" aria-label="应用模式">
      <button
        type="button"
        role="tab"
        aria-selected={mode === "project"}
        className={"mode-switch-item" + (mode === "project" ? " on" : "")}
        onClick={() => onChange("project")}
        title="项目模式：会话、Agent、Git 与拓展"
      >
        项目
      </button>
      <button
        type="button"
        role="tab"
        aria-selected={mode === "assistant"}
        className={"mode-switch-item" + (mode === "assistant" ? " on" : "")}
        onClick={() => onChange("assistant")}
        title="助手模式：通用个人助理，项目任务经 run_task 派进项目"
      >
        助手
      </button>
    </div>
  );
});
