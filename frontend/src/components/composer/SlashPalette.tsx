// 斜杠命令面板（Codex/DSH 的 Slash Command Palette）：输入以 / 开头时
// 在输入框上方弹出，关键字过滤 + 上下键导航 + Enter/Tab 补全。
// 键盘：面板开着时自己监听 window keydown（textarea 的按键冒泡到
// document 前被这里截获）——Enter/Tab 选中、↑↓ 导航、Esc 关闭。
// 原型：命令集由 App 注入（页面导航）；后端化时同一面板接会话/工具域
// 命令（/resume /archive /compact …）。
import { useEffect, useMemo, useRef, useState } from "react";
import { useDismissal } from "../../shared/popover";
import { motionAllowed } from "../../shared/motion";
import { gsap } from "gsap";

export interface SlashCommand {
  /** 命令名（不含 /——输入 /new 匹配 "new"）。 */
  name: string;
  desc: string;
  run: () => void;
}

export function SlashPalette({
  query,
  onPick,
  onClose,
  commands,
}: {
  /** 当前输入的 / 前缀词（空串 = / 刚输入，显示全部）。 */
  query: string;
  onPick: (cmd: SlashCommand) => void;
  onClose: () => void;
  commands: SlashCommand[];
}) {
  const [active, setActive] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>( null);

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return commands;
    return commands.filter((c) => c.name.includes(q) || c.desc.toLowerCase().includes(q));
  }, [query, commands]);

  useDismissal(rootRef, true, onClose);

  // 匹配集变化时重置选中（关键字变了原选中可能不在列表里）
  useEffect(() => {
    setActive((a) => Math.min(a, Math.max(matches.length - 1, 0)));
  }, [query, matches.length]);

  // 键盘：面板开着时接管（textarea 的 keydown 冒到 window 前截获）
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (matches.length === 0 && e.key !== "Escape") return;
      switch (e.key) {
        case "ArrowDown":
          e.preventDefault();
          setActive((a) => (a + 1) % matches.length);
          break;
        case "ArrowUp":
          e.preventDefault();
          setActive((a) => (a - 1 + matches.length) % matches.length);
          break;
        case "Enter":
        case "Tab":
          e.preventDefault();
          if (matches[active]) onPick(matches[active]);
          break;
        case "Escape":
          e.preventDefault();
          onClose();
          break;
      }
    };
    window.addEventListener("keydown", onKey, true); // capture：先于 textarea 的默认行为
    return () => window.removeEventListener("keydown", onKey, true);
  }, [matches, active, onPick, onClose]);

  // 入场（gsap——面板每次挂载播一次）
  useEffect(() => {
    const el = rootRef.current;
    if (!el || !motionAllowed()) return;
    gsap.fromTo(el, { opacity: 0, y: 8 }, { opacity: 1, y: 0, duration: 0.22, ease: "power2.out", clearProps: "transform,opacity" });
  }, []);

  // 选中项滚进可见区
  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>(".cmd-item.on")?.scrollIntoView({ block: "nearest" });
  }, [active]);

  return (
    <div className="slash-palette" ref={rootRef} role="listbox" aria-label="斜杠命令">
      <div className="cmd-list" ref={listRef}>
        {matches.length === 0 && <div className="cmd-empty">没有匹配「{query.trim()}」的命令</div>}
        {matches.map((c, i) => (
          <button
            key={c.name}
            type="button"
            className={"cmd-item" + (i === active ? " on" : "")}
            role="option"
            aria-selected={i === active}
            onMouseEnter={() => setActive(i)}
            onClick={() => onPick(c)}
          >
            <span className="cmd-name mono">/{c.name}</span>
            <span className="cmd-desc">{c.desc}</span>
          </button>
        ))}
      </div>
      <div className="cmd-foot">
        <span>↑↓ 选择</span>
        <span>Enter 确认</span>
        <span>Esc 关闭</span>
      </div>
    </div>
  );
}
