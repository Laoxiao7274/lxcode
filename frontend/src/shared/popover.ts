// 浮层/弹窗通用行为（此前四个弹层各自手写「点外关闭 + Esc」的接线）。
//  - useDismissal(rootRef, active, close)：document 级 pointerdown + Escape
//  - useEscape(active, close)：模态框只要 Esc
//  - usePopover：受控开合 + gsap 退场动画再卸载（直接 setOpen(false) 是
//    瞬灭，开合不对称）。约定：弹层面板挂 data-pop；退场中重开会打断
//    退场并补一次 gsap 入场。
import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "./motion";

/** active 期间：点击 root 外部或按 Esc 触发 close。 */
export function useDismissal(rootRef: RefObject<HTMLElement | null>, active: boolean, close: () => void): void {
  useEffect(() => {
    if (!active) return;
    const onDown = (e: PointerEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) close();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    document.addEventListener("pointerdown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [active, close, rootRef]);
}

/** active 时按 Esc 触发 close（模态框用；无「点外关闭」语义）。 */
export function useEscape(active: boolean, close: () => void): void {
  useEffect(() => {
    if (!active) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [active, close]);
}

export function usePopover() {
  const rootRef = useRef<HTMLDivElement>(null);
  const [open, setOpenState] = useState(false);
  const openRef = useRef(false);
  const closingRef = useRef(false);

  const popEls = () =>
    rootRef.current ? Array.from(rootRef.current.querySelectorAll<HTMLElement>("[data-pop]")) : [];

  const setOpen = useCallback((v: boolean) => {
    if (closingRef.current) {
      // 退场中重开：杀掉动画、恢复样式，gsap 补一次入场保持手感一致
      const els = popEls();
      gsap.killTweensOf(els);
      if (v && motionAllowed() && els.length) {
        gsap.set(els, { animation: "none" });
        gsap.fromTo(
          els,
          { opacity: 0, y: -4, scale: 0.98 },
          {
            opacity: 1, y: 0, scale: 1, duration: 0.16, ease: "power2.out",
            clearProps: "transform,opacity,pointerEvents",
            onComplete: () => { closingRef.current = false; },
          },
        );
        openRef.current = true;
        setOpenState(true);
        return;
      }
      gsap.set(els, { clearProps: "opacity,transform" });
      closingRef.current = false;
    }
    openRef.current = v;
    setOpenState(v);
  }, []);

  const requestClose = useCallback(() => {
    if (!openRef.current || closingRef.current) return;
    const els = popEls();
    if (!els.length || !motionAllowed()) {
      openRef.current = false;
      setOpenState(false);
      return;
    }
    closingRef.current = true;
    // 入场 CSS 是 fill:both——结束后仍占住 opacity/transform，退场前先
    // 禁掉动画（内联 animation:none 覆盖样式表），gsap 才能接管
    gsap.set(els, { animation: "none", pointerEvents: "none" });
    // 面板锚定在触发器上方：退场向触发器方向收（下沉 + 收缩 + 淡出）；
    // 0.2s/8px/0.96 是有「收回去」存在感的缓冲档位，再小就成了瞬灭
    gsap.to(els, {
      opacity: 0,
      y: 8,
      scale: 0.96,
      transformOrigin: "left bottom",
      duration: 0.2,
      ease: "power2.in",
      overwrite: true,
      onComplete: () => {
        closingRef.current = false;
        openRef.current = false;
        setOpenState(false);
      },
    });
  }, []);

  // 点外/Escape 关闭（含退场动画）
  useDismissal(rootRef, open, requestClose);

  const toggle = useCallback(() => {
    if (openRef.current) requestClose();
    else setOpen(true);
  }, [requestClose, setOpen]);

  return { open, toggle, setOpen, requestClose, rootRef };
}
