import { useEffect, useRef, useState } from "react";
import { Button } from "../form";
import { AgentPicker } from "../agents/AgentPicker";
import { PermPicker } from "../perm-picker";
import { ModelPicker } from "../model-picker";
import { ContextIndicator } from "../context-indicator";
import { StatsPills } from "../stats-pills";
import { TodoList } from "../../aicss/TodoList";
import { SlashPalette, type SlashCommand } from "./SlashPalette";
import { useEnterRef } from "../../shared/anim";
import type { ContextUsage, SessionStats, TodoItem } from "../../shared/types";

/** 输入区（Codex 式）：busy 时输入框保留（可预输入），发送钮变停止。
 *  斜杠命令：输入以 / 开头时上方弹命令面板（关键字过滤 + 键盘导航 +
 *  Enter/Tab 补全——面板开着时 Enter 不发送）。
 *  任务清单卡浮在输入框正上方（与 busy 行同层——都在 composer 浮层内，
 *  不会被绝对定位的输入区遮挡；清单不进对话流）。 */
/** 外部注入输入框的草稿（撤回/编辑把原文放回输入框）。
 *
 *  **用单调递增的 id 而不是按文本比较**：同一条消息连续撤回两次（或撤回后再编辑
 *  同一条）文本一模一样，按文本判重的话第二次是 no-op——用户看到"点了没反应"。 */
export interface ComposerDraft {
  id: number;
  text: string;
}

export function Composer({
  busy,
  disabled,
  todos = [],
  context = null,
  stats = null,
  onSend,
  onCancel,
  onCompact,
  commands = [],
  draft = null,
  editing = false,
  onCancelEdit,
}: {
  busy: boolean;
  disabled?: boolean;
  /** 任务清单（空数组不渲染卡片）。 */
  todos?: TodoItem[];
  /** 上下文占用（后端测量；null = 未知——指示器显示中性态）。 */
  context?: ContextUsage | null;
  /** 整段会话统计（后端折叠整段日志；null = 还没有任何一步——整行不渲染）。 */
  stats?: SessionStats | null;
  onSend: (text: string) => void;
  onCancel: () => void;
  /** 手动压缩历史（空闲才可用；不传 = 指示器不显示入口）。 */
  onCompact?: () => void;
  /** 斜杠命令集（App 注入——页面导航；选择器聚焦命令由 piBar 控件自身
   *  的打开态承载，/model 等 = 聚焦后打开对应选择器的实现放命令集里）。 */
  commands?: SlashCommand[];
  /** 撤回/编辑把原文放回输入框（null = 没有待注入的草稿）。 */
  draft?: ComposerDraft | null;
  /** 编辑态（正在编辑某条历史消息）——发送时会先撤回那条及其之后的对话。 */
  editing?: boolean;
  onCancelEdit?: () => void;
}) {
  const [value, setValue] = useState("");
  const taRef = useRef<HTMLTextAreaElement>(null);
  const zoneRef = useRef<HTMLDivElement>(null);
  /** 已注入的草稿 id（按 id 判重，不按文本——见 ComposerDraft 的注释）。 */
  const draftIdRef = useRef(0);
  // 「生成中」状态行挂载即上浮淡入（busy 翻转时才挂载/卸载）
  const busyRowRef = useEnterRef<HTMLDivElement>({ opacity: 0, y: 6 }, { opacity: 1, y: 0, duration: 0.26, ease: "power2.out", clearProps: "transform,opacity" });
  const canSend = value.trim().length > 0 && !busy && !disabled && !value.startsWith("/");

  // 斜杠面板：输入以 / 开头（单行——/ 出现在行中不算命令）时开
  const slashOpen = !disabled && value.startsWith("/") && !value.includes("\n");
  const slashQuery = slashOpen ? value.replace(/^\/+/, "") : "";

  const submit = () => {
    if (!canSend) return;
    onSend(value.trim());
    setValue("");
    requestAnimationFrame(() => taRef.current?.focus());
  };

  // 面板选中：清输入、跑动作、回焦输入框
  const pick = (c: SlashCommand) => {
    setValue("");
    c.run();
    requestAnimationFrame(() => taRef.current?.focus());
  };

  const onKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (slashOpen) return; // 面板接管键盘（palette 在 window capture 层处理）
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  // 外部注入草稿（撤回/编辑把原文放回输入框）：写入 + 聚焦 + 光标停在末尾。
  // 光标必须到末尾——用户接着改的是这句话，停在开头或全选都别扭。
  useEffect(() => {
    if (!draft || draft.id === draftIdRef.current) return;
    draftIdRef.current = draft.id;
    setValue(draft.text);
    requestAnimationFrame(() => {
      const ta = taRef.current;
      if (!ta) return;
      ta.focus();
      ta.setSelectionRange(ta.value.length, ta.value.length);
    });
  }, [draft]);

  // 输入区（含清单卡）的真实高度发布给 .main——线程区按它预留底部空间，
  // 清单展开多高就留多少：浮层永远不遮挡对话内容（把清单当输入区的一部分）。
  useEffect(() => {
    const el = zoneRef.current;
    const main = el?.closest(".main") as HTMLElement | null;
    if (!el || !main) return;
    const publish = () => main.style.setProperty("--composer-h", el.offsetHeight + "px");
    const ro = new ResizeObserver(publish);
    ro.observe(el);
    publish();
    return () => {
      ro.disconnect();
      main.style.removeProperty("--composer-h");
    };
  }, []);

  return (
    <div className="composer-zone" ref={zoneRef}>
      <div className="composer-inner">
        {/* 任务清单卡：输入框正上方（与 busy 行同层——浮层内不被遮挡） */}
        {todos.length > 0 && (
          <div className="composer-plan">
            <TodoList items={todos} />
          </div>
        )}
        {/* 编辑态提示行：必须说清"发送会先撤回它及其之后的对话"——不写清楚
            用户会以为编辑只是改字，而历史会被清掉 */}
        {editing && (
          <div className="edit-row">
            <span className="edit-text">正在编辑这条消息 · 发送时会先撤回它及其之后的对话</span>
            <Button className="edit-cancel" onClick={onCancelEdit}>取消编辑</Button>
          </div>
        )}
        {/* busy 状态行：浮在输入框上方（生成中 + 停止入口在按钮位） */}
        {busy && (
          <div className="busy-row" ref={busyRowRef}>
            <span className="mset-spinner" aria-hidden />
            <span className="busy-text">生成中</span>
          </div>
        )}
        <div className="pi">
          {slashOpen && (
            <SlashPalette
              query={slashQuery}
              commands={commands}
              onPick={pick}
              onClose={() => setValue("")}
            />
          )}
          <textarea
            ref={taRef}
            className="piInput"
            placeholder={busy ? "生成中… 可以先输入下一条（完成后发送）" : "让智能体构建、审查或解释点什么…"}
            rows={1}
            value={value}
            disabled={disabled}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={onKey}
          />
          <div className="piBar">
            <AgentPicker />
            <PermPicker />
            <ModelPicker />
            <ContextIndicator usage={context} stats={stats} onCompact={onCompact} busy={busy} />
            {/* 会话统计胶囊（时间）：紧挨上下文环（2026-09-30 用户拍板）。累计消耗那一半
                并进了上下文环的「会话用量」弹层——输入条因此不再拥挤 */}
            <StatsPills stats={stats} />
            <span className="piTips" />
            {busy ? (
              <button type="button" className="send-btn stop" onClick={onCancel} aria-label="停止生成" title="停止生成">
                <svg width="11" height="11" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                  <rect x="5" y="5" width="14" height="14" rx="2.5" />
                </svg>
              </button>
            ) : (
              <button
                type="button"
                className="send-btn"
                disabled={!canSend}
                onClick={submit}
                aria-label="发送"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M12 19V5" />
                  <path d="m5 12 7-7 7 7" />
                </svg>
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
