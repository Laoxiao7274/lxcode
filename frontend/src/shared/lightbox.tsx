// 灯箱（图片点击放大）shared/lightbox.tsx：全屏遮罩 + 原图 + 缩放/平移/
// 多图切换/工具条。千问式体验的手写实现——零新依赖、纯 React + CSS，动效经
// motionAllowed 门控；纯函数（钳制/索引/偏移算术）在同目录 lightbox.ts。
//
// 状态管理（全部本地 state，关闭即销毁）：
// - 打开基准 = 「适配」：natural 尺寸经 fitSize 算出 contain 显示尺寸，scale=1
//   就是适配态（img 无 CSS max 限制、由 transform 缩放控制——语义统一）；
// - 滚轮/双击/按钮改 scale，光标锚点经 anchorZoomOffset 换算偏移再钳制；
// - 拖动平移改 offset，钳制保证图至少一角留在视口内（拖不丢）；
// - 多图：items + index，左右/键盘环形切换，切换即重置缩放与偏移。
//
// 入口统一：消息侧图片网格 / 卡内图 / 子会话成果区全部经 openImageLightbox
// （openRef 全局收口、LightboxHost 单实例）进这一个组件；渲染走 portal 挂
// body（防父容器 overflow/transform 裁剪），Body 滚动锁定，Esc/遮罩点击关闭。
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { AttachmentMeta } from "./attachments";
import { attachmentURL } from "./attachments";
import { motionAllowed } from "./motion";
import {
  anchorZoomOffset,
  clampOffset,
  clampScale,
  doubleClickScale,
  fitSize,
  scaleLabel,
  stepIndex,
  wheelScale,
} from "./lightbox-math";

/** 灯箱里的一张图：附件元数据 + 可用 URL（构造失败的不进集合）。 */
export interface LightboxItem {
  att: AttachmentMeta;
  url: string;
}

/** 从附件列表挑出可进灯箱的图片（URL 构造失败的不进）。 */
export function lightboxImages(atts: AttachmentMeta[]): LightboxItem[] {
  const out: LightboxItem[] = [];
  for (const att of atts) {
    if (att.kind !== "image") continue;
    const url = attachmentURL(att.path);
    if (url) out.push({ att, url });
  }
  return out;
}

const openRef: { current: ((items: LightboxItem[], index: number) => void) | null } = { current: null };

/** 打开灯箱：items 非空才开；index 钳到合法范围。挂在缩略图 onClick 上。 */
export function openImageLightbox(items: LightboxItem[], index = 0): void {
  if (!items.length) return;
  openRef.current?.(items, Math.max(0, Math.min(items.length - 1, index)));
}

/** 灯箱宿主：挂在 App 根部（单实例——openRef 全局收口，多处入口共用）。 */
export function LightboxHost() {
  const [state, setState] = useState<{ items: LightboxItem[]; index: number } | null>(null);
  useEffect(() => {
    openRef.current = (items, index) => setState({ items, index });
    return () => {
      openRef.current = null;
    };
  }, []);
  if (!state) return null;
  return (
    <ImageLightbox
      items={state.items}
      index={state.index}
      onClose={() => setState(null)}
      onNavigate={(i) => setState((s) => (s ? { ...s, index: i } : s))}
    />
  );
}

type Size = { width: number; height: number };
type Offset = { x: number; y: number };

const ZERO: Offset = { x: 0, y: 0 };

export function ImageLightbox({ items, index, onClose, onNavigate }: {
  items: LightboxItem[];
  index: number;
  onClose: () => void;
  onNavigate: (index: number) => void;
}) {
  const item = items[index];
  const [scale, setScale] = useState(1);
  const [offset, setOffset] = useState<Offset>(ZERO);
  const [loaded, setLoaded] = useState(false);
  const [failed, setFailed] = useState(false);
  const [natural, setNatural] = useState<Size | null>(null);
  const [viewport, setViewport] = useState<Size | null>(null);
  const [closing, setClosing] = useState(false);
  const dragRef = useRef<{ pointerId: number; startX: number; startY: number; base: Offset } | null>(null);
  const [dragging, setDragging] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);

  const multi = items.length > 1;

  const requestClose = useCallback(() => {
    if (!motionAllowed()) {
      onClose();
      return;
    }
    setClosing(true);
    window.setTimeout(onClose, 140);
  }, [onClose]);

  // 切图/换图：重置视图状态（每张图的缩放位置独立，不串）
  useEffect(() => {
    setScale(1);
    setOffset(ZERO);
    setLoaded(false);
    setFailed(false);
    setNatural(null);
  }, [item?.url]);

  // Body 滚动锁定 + 视口尺寸跟踪（resize 时重算钳制与适配）
  useLayoutEffect(() => {
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const measure = () => setViewport({ width: window.innerWidth, height: window.innerHeight });
    measure();
    window.addEventListener("resize", measure);
    return () => {
      document.body.style.overflow = prev;
      window.removeEventListener("resize", measure);
    };
  }, []);

  // 键盘：Esc 关闭、←→ 切图
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        requestClose();
      } else if (multi && e.key === "ArrowLeft") {
        e.preventDefault();
        onNavigate(stepIndex(index, items.length, -1));
      } else if (multi && e.key === "ArrowRight") {
        e.preventDefault();
        onNavigate(stepIndex(index, items.length, 1));
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [requestClose, multi, index, items.length, onNavigate]);

  /** 钳制当前偏移（scale/viewport/natural 就绪时）。 */
  const clamped = useCallback((o: Offset, s: number): Offset => {
    if (!natural || !viewport) return o;
    const fit = fitSize(natural, viewport);
    if (!fit) return o;
    return clampOffset(o, s, fit, viewport);
  }, [natural, viewport]);

  const zoomTo = useCallback((to: number, anchor: Offset) => {
    setScale((s) => {
      const next = clampScale(to);
      setOffset((o) => clamped(anchorZoomOffset(o, s, next, anchor), next));
      return next;
    });
  }, [clamped]);

  const onWheel = useCallback((e: React.WheelEvent) => {
    e.preventDefault();
    const rect = rootRef.current?.getBoundingClientRect();
    if (!rect) return;
    zoomTo(wheelScale(scale, e.deltaY), { x: e.clientX - rect.left - rect.width / 2, y: e.clientY - rect.top - rect.height / 2 });
  }, [scale, zoomTo]);

  const onDoubleClick = useCallback((e: React.MouseEvent) => {
    const rect = rootRef.current?.getBoundingClientRect();
    const fit = natural && viewport ? fitSize(natural, viewport) : null;
    if (!rect || !fit) return;
    const { scale: next, reset } = doubleClickScale(scale, 1);
    setScale(next);
    setOffset(reset ? ZERO : clamped(anchorZoomOffset(offset, scale, next, { x: e.clientX - rect.left - rect.width / 2, y: e.clientY - rect.top - rect.height / 2 }), next));
  }, [scale, offset, natural, viewport, clamped]);

  const onPointerDown = useCallback((e: React.PointerEvent) => {
    if (scale <= 1) return;
    (e.target as Element).setPointerCapture?.(e.pointerId);
    dragRef.current = { pointerId: e.pointerId, startX: e.clientX, startY: e.clientY, base: offset };
    setDragging(true);
  }, [scale, offset]);

  const onPointerMove = useCallback((e: React.PointerEvent) => {
    const d = dragRef.current;
    if (!d || d.pointerId !== e.pointerId) return;
    setOffset(clamped({ x: d.base.x + (e.clientX - d.startX), y: d.base.y + (e.clientY - d.startY) }, scale));
  }, [clamped, scale]);

  const endDrag = useCallback(() => {
    dragRef.current = null;
    setDragging(false);
  }, []);

  const onMaskClick = useCallback((e: React.MouseEvent) => {
    if (e.target === e.currentTarget) requestClose();
  }, [requestClose]);

  const download = useCallback(() => {
    if (!item) return;
    const a = document.createElement("a");
    a.href = item.url;
    a.download = item.att.name || "image";
    document.body.appendChild(a);
    a.click();
    a.remove();
  }, [item]);

  if (!item) return null;
  const canPrev = multi && index > 0;
  const canNext = multi && index < items.length - 1;
  const imgStyle: React.CSSProperties = natural ? { width: natural.width, height: natural.height } : {};
  const stageStyle: React.CSSProperties | undefined = scale !== 1 || offset.x !== 0 || offset.y !== 0
    ? { transform: `translate(${offset.x}px, ${offset.y}px) scale(${scale})` }
    : undefined;

  // SSR（renderToStaticMarkup）不支持 portal：服务端渲染路径直接返回 null——
  // 灯箱是纯客户端交互（点击后才有），SSR 输出里本来就不该出现它。
  const portal = typeof document !== "undefined" && document?.body != null;
  if (!portal) return null;

  return createPortal(
    <div
      ref={rootRef}
      className={"lightbox" + (dragging ? " lightbox-dragging" : "") + (closing ? " lightbox-closing" : "")}
      role="dialog"
      aria-modal="true"
      aria-label={item.att.caption || item.att.name || "图片预览"}
      onClick={onMaskClick}
      onWheel={onWheel}
    >
      {/* 大图舞台：点击不冒泡成关闭（点图片不等于点遮罩空白） */}
      <div className="lightbox-stage" style={stageStyle} onDoubleClick={onDoubleClick} onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={endDrag} onPointerCancel={endDrag} onClick={(e) => e.stopPropagation()}>
        {!loaded && !failed && <div className="lightbox-loading" aria-label="图片加载中"><span className="lightbox-spinner" /></div>}
        <img
          className="lightbox-img"
          style={imgStyle}
          src={item.url}
          alt={item.att.caption || item.att.name || "图片附件"}
          draggable={false}
          decoding="async"
          onLoad={(e) => {
            const el = e.currentTarget;
            setNatural({ width: el.naturalWidth, height: el.naturalHeight });
            setLoaded(true);
          }}
          onError={() => setFailed(true)}
        />
        {failed && <div className="lightbox-failed">图片加载失败（源文件可能已不在）</div>}
      </div>

      {/* 顶栏：文件名 + 计数 + 关闭 */}
      <div className="lightbox-topbar">
        <span className="lightbox-name">{item.att.caption || item.att.name || "图片"}</span>
        {multi && <span className="lightbox-count">{index + 1} / {items.length}</span>}
        <button type="button" className="lightbox-btn lightbox-close" onClick={requestClose} aria-label="关闭（Esc）">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden><path d="M18 6 6 18M6 6l12 12" /></svg>
        </button>
      </div>

      {/* 左右切换 */}
      {multi && (
        <>
          <button type="button" className="lightbox-btn lightbox-nav lightbox-prev" disabled={!canPrev} onClick={() => onNavigate(stepIndex(index, items.length, -1))} aria-label="上一张（←）">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden><path d="m15 18-6-6 6-6" /></svg>
          </button>
          <button type="button" className="lightbox-btn lightbox-nav lightbox-next" disabled={!canNext} onClick={() => onNavigate(stepIndex(index, items.length, 1))} aria-label="下一张（→）">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden><path d="m9 18 6-6-6-6" /></svg>
          </button>
        </>
      )}

      {/* 底部工具条：缩放比例 / 缩放按钮 / 提示 / 下载（贴底细条不挡图） */}
      <div className="lightbox-toolbar">
        <button type="button" className="lightbox-btn" onClick={() => zoomTo(wheelScale(scale, -1), ZERO)} aria-label="缩小">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden><path d="M5 12h14" /></svg>
        </button>
        <span className="lightbox-scale" aria-live="polite">{scaleLabel(scale)}</span>
        <button type="button" className="lightbox-btn" onClick={() => zoomTo(wheelScale(scale, 1), ZERO)} aria-label="放大">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden><path d="M12 5v14M5 12h14" /></svg>
        </button>
        <span className="lightbox-sep" aria-hidden />
        <span className="lightbox-hint">滚轮缩放 · 双击 100% / 适配 · 拖动平移</span>
        <span className="lightbox-sep" aria-hidden />
        <button type="button" className="lightbox-btn" onClick={download} aria-label="下载">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3" /></svg>
        </button>
      </div>
    </div>,
    document.body,
  );
}
