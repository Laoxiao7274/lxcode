// 助手模式的输入区（助手模式视觉对齐主域，2026-12）：**Composer 的视觉壳**——
// DOM 与类名直接用主域 composer.css 的全局类（.pi / .pi-row / .piInput / .piBar /
// .send-btn / .busy-row 都是全局类，composer.css 随 global.css 全量加载），
// 视觉逐像素对齐主域输入卡（20px 圆角白卡 + ring shadow + focus-within 提亮、
// 14px 输入、32px 正圆发送钮），不需要复制任何样式。
//
// piBar 四件套（2026-12 piBar 接入）：PermPicker / ModelPicker / ContextIndicator /
// StatsPills——与主域 Composer 的差别：助手域**没有** AgentPicker（内置助手不在
// 库名单，串域不接）。四件套的 props 全部来自助手域 state（approval/model/context/
// stats 由同一份归约器写入 assistantStates），改档/切模型走 assistant.* 方法域。
// 附件「+」/ 斜杠面板 / 发送队列仍是助手域范围外，刻意不渲染。
//
// 保持流内定位（不套 .composer-zone absolute——会破坏 flex 贴底）；
// 不发布 --composer-h（AssistantWorkspace 的归零规则保持）。
// Enter 发送 / Shift+Enter 换行（与 Composer 的 onKey 同语义）。
import { useEffect, useRef, useState } from "react";
import { useEnterRef } from "../../shared/anim";
import { PermPicker } from "../perm-picker";
import { ModelPicker } from "../model-picker";
import { ContextIndicator } from "../context-indicator";
import { StatsPills } from "../stats-pills";
import type { ApprovalMode, ContextUsage, SessionStats } from "../../shared/types";

export function AssistantInput({
  busy,
  sending = false,
  onSend,
  onCancel,
  disabled = false,
  approval,
  onPickApproval,
  model,
  onPickModel,
  context,
  stats,
  onCompact,
  compacting = false,
}: {
  busy: boolean;
  /** 发送中态（乐观气泡已插、busy 还没到）：与主域 Composer 同语义——
   *  busy-row 显示「发送中…」，到达后无缝切「生成中」。 */
  sending?: boolean;
  onSend: (text: string) => void;
  onCancel: () => void;
  disabled?: boolean;
  /** 当前助手会话的权限档（state.approval；null = 未知——PermPicker 回落默认档显示）。 */
  approval?: ApprovalMode | null;
  /** 改档入口（assistant.setApproval——乐观对齐由 assistant.approvalChanged 广播回写）。 */
  onPickApproval?: (mode: ApprovalMode) => void;
  /** 当前助手会话的模型 id（state.model；空 = 未知——ModelPicker 显示中性态）。 */
  model?: string;
  /** 切模型入口（assistant.model——不乐观改，由 assistant.modelChanged 对齐）。 */
  onPickModel?: (id: string) => void;
  /** 上下文占用（state.context；null = 未知——指示器显示中性态「—」）。 */
  context?: ContextUsage | null;
  /** 整段会话统计（state.stats；null = 还没有任何一步——不渲染统计胶囊）。 */
  stats?: SessionStats | null;
  /** 手动压缩入口（assistant.compact——空闲才可用）。 */
  onCompact?: () => void;
  /** 压缩中态（useCompaction 的 compacting——ContextIndicator 的按钮反馈）。 */
  compacting?: boolean;
}) {
  const [text, setText] = useState("");
  const taRef = useRef<HTMLTextAreaElement>(null);
  // 「生成中」状态行挂载即上浮淡入（与 Composer 的 busyRowRef 同一件 shared/anim）
  const busyRowRef = useEnterRef<HTMLDivElement>({ opacity: 0, y: 6 }, { opacity: 1, y: 0, duration: 0.26, ease: "power2.out", clearProps: "transform,opacity" });
  const locked = disabled;
  // 发送中不可直发（消息已交出去）——与 Composer 的 route 语义一致（简化版：
  // 助手域没有队列，busy/sending 之外只有「有没有文本」一个维度）
  const canSend = !locked && !sending && !busy && text.trim().length > 0;

  // 压缩中态（compacting）由上层持有（useCompaction——超时尾巴/重入闸在钩子里，
  // 这里只透传；与主域 Composer 的 onCompact/compacting 同一形状）。

  // 自增高（DSH 形态，与 Composer 同一段逻辑）：随内容长高到上限
  //（CSS max-height 200px），之内不出内部滚动条，到上限后才允许内部滚动。
  // 发送清空走 text 变化 → 自动复位。
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    const max = 200;
    ta.style.height = Math.min(ta.scrollHeight, max) + "px";
    ta.style.overflowY = ta.scrollHeight > max ? "auto" : "hidden";
  }, [text]);

  const submit = () => {
    const t = text.trim();
    if (!t || locked || sending) return;
    onSend(t);
    setText("");
    requestAnimationFrame(() => taRef.current?.focus());
  };

  // Enter 发送 / Shift+Enter 换行（与 Composer 的 onKey 同语义；合成中不拦截）
  const onKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  return (
    <div className="assistant-input">
      {/* busy/sending 状态行：浮在输入卡上方（与主域 Composer 同一拍——
          busy 到达后文案无缝从「发送中…」切到「生成中」） */}
      {(busy || sending) && (
        <div className="busy-row" ref={busyRowRef}>
          <span className="mset-spinner" aria-hidden />
          <span className="busy-text">{busy ? "生成中" : "发送中…"}</span>
        </div>
      )}
      <div className="pi">
        <div className="pi-row">
          <textarea
            ref={taRef}
            className="piInput"
            placeholder={locked ? "当前会话不可用" : busy ? "生成中… 可先输入" : "把日常事务交给助手…"}
            rows={1}
            value={text}
            disabled={locked}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKey}
          />
        </div>
        <div className="piBar">
          {/* 权限与模型一组（与主域 Composer 的 .pi-controls 同款分组；助手域
              没有 AgentPicker）；ContextIndicator/StatsPills 是读数，留在组外。 */}
          <span className="pi-controls">
            <PermPicker
              approval={approval ?? undefined}
              onPick={onPickApproval ? (mode) => onPickApproval(mode) : undefined}
            />
            <span className="pi-model-slot">
              <ModelPicker model={model} onPickModel={onPickModel} chipTitle={busy ? "正在运行，切换将在下一轮生效" : undefined} />
            </span>
          </span>
          <ContextIndicator
            usage={context ?? null}
            stats={stats ?? null}
            onCompact={onCompact}
            busy={busy}
            compacting={compacting}
          />
          <StatsPills stats={stats ?? null} />
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
  );
}
