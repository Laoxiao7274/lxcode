// 打字机缓冲（移植自 lx-dsh harness 的 ui-primitives/markdown/stream-reveal）：
// 流式文本新到达的字符不整块蹦出，而是按固定「浇注窗口」匀速流出——
// 积压越大流速越快（30~1000 字/秒），流结束后缓冲继续排空，观感即 Codex
// 那种顺滑打字机。节奏属于内容呈现而非装饰动效：reduced-motion 不关闭，
// 仅测试通道（__LX_TEST_MOTION_OFF__）禁用。
//
// 页面隐藏（document.hidden）纪律（2026-12 卡顿修复）：
// - rAF 在隐藏页停转，浇注循环本来就跑不动；修复前积压一旦超过
//   REVEAL_MAX_LAG 还会**每条 delta 全量对齐**（setShown(全文) → Markdown
//   对全文重新解析）——为不可见的内容持续烧 CPU。
// - 现在 hidden 期间一律**冻结** shown（revealPlan 返回 "hold"）：只低成本
//   累积 state（TextBlocks 的 memo 按字符串命中，shown 不变 = 零解析），
//   不浇注、不对齐、不 setShown。
// - 恢复可见那一刻（visibilitychange）统一落账：积压 > REVEAL_RESUME_JUMP
//   直接整块落下（一次提交，跳过动画——大积压浇注的每帧成本是 O(全文)
//   解析 + 置底强制布局，恢复后会是数秒的逐帧风暴）；小积压照常浇注，
//   观感不变。落账判定在 revealPlan（纯函数，单测钉住）。
import { useEffect, useRef, useState } from "react";

/** 有积压时最慢揭示速度（字/秒）。 */
const REVEAL_MIN_CPS = 30;
/** 无论积压多大都不超过的最快揭示速度——爆发段也要看得见地流出。 */
const REVEAL_MAX_CPS = 1000;
/** 每段积压都按这个秒数浇注完；积压越大按比例浇得越快。 */
const REVEAL_POUR_SECONDS = 0.35;
/** 积压超过这个字符数直接对齐：分叉的流不能回放。 */
const REVEAL_MAX_LAG = 4000;
/** 恢复可见那一刻的落账阈值（字）：隐藏期间冻结的积压超过它就整块落下。
 *  1000 字 ≈ 最快速度下 1s 的浇注——超过这个时长，逐帧全文解析 + 置底
 *  强制布局的代价已经大于「整块落下」的一次性成本。 */
export const REVEAL_RESUME_JUMP = 1000;

function pacingAllowed(): boolean {
  if (typeof window === "undefined") return false;
  return (window as unknown as { __LX_TEST_MOTION_OFF__?: boolean }).__LX_TEST_MOTION_OFF__ !== true;
}

/** 页面当前是否隐藏（无 DOM 环境——node 测试/SSR——视为可见）。 */
export function documentHidden(): boolean {
  return typeof document !== "undefined" && document.visibilityState === "hidden";
}

/** 一次 text 到达（或恢复可见）的落账决策（纯函数，可单测）。
 *  - "none"：无积压，不用动；
 *  - "snap"：整块落下（分叉流不能回放 / 积压超限 / 动效禁用）；
 *  - "pour"：按节奏浇注（rAF 驱动，走 pourStep 推进）；
 *  - "hold"：页面隐藏，冻结 shown 等恢复钩子落账——**无论积压多大都冻结**
 *    （修复前 hidden 期间积压超限会每条 delta 全量对齐，纯烧 CPU）。 */
export type RevealPlan = "none" | "snap" | "pour" | "hold";

export function revealPlan(
  shown: string,
  text: string,
  opts: { visible: boolean; motion: boolean; lagLimit: number },
): RevealPlan {
  if (!opts.visible) return "hold";
  if (!text.startsWith(shown)) return "snap"; // 分叉的流不能回放
  if (text === shown) return "none";
  if (!opts.motion) return "snap";
  if (text.length - shown.length > opts.lagLimit) return "snap";
  return "pour";
}

/** 浇注一步：从 shown 前缀按积压自适应速度推进一步（纯函数，可单测）。
 *  返回新的 shown 前缀（无积压时原样返回）。 */
export function pourStep(shown: string, text: string, elapsedMs: number): string {
  const gap = text.length - shown.length;
  if (gap <= 0 || !text.startsWith(shown)) return text;
  const rate = Math.min(REVEAL_MAX_CPS, Math.max(REVEAL_MIN_CPS, gap / REVEAL_POUR_SECONDS));
  const step = Math.max(1, Math.round((rate * elapsedMs) / 1000));
  return text.slice(0, Math.min(text.length, shown.length + step));
}

// ---------- 已定稿前缀（两段渲染，2026-12 渲染性能 Top2）----------
//
// 打字机浇注期间 shown 每帧推进，若整段 shown 都走 Markdown 解析，每帧的成本
// 是 O(全文)。两段渲染把 shown 切成「已定稿前缀 committed + 尾段 tail」：
// committed 只在整行完成时才前进（前进频率 = 行完成频率，远低于帧率），它的
// TextBlocks memo 按字符串比较恒命中（零解析零协调）；每帧真正重解析的只有
// tail（≤2 行）。切点必须落在**块边界**上——切在 quote/list/table/代码围栏
// 中间会把一个块渲染成两个（与整段解析的 DOM 形态分叉），所以候选切点要
// 回退到更早的安全行（splitCommitted 纯函数，测试覆盖各未闭合形态）。

const RE_QUOTE_LINE = /^>\s?/;
const RE_UL_LINE = /^(\s*)[-*+]\s+/;
const RE_OL_LINE = /^(\s*)(\d+)[.)]\s+/;
const isTableRowLine = (l: string): boolean => l.trimStart().startsWith("|");

/** shown 的已定稿前缀切分（纯函数，可单测）：
 *  - 候选切点从「最后一行之前」起逐行回退（增长中的末行恒在 tail；比「倒数第
 *    2 行之前」多保一行已定稿——尾段更小，每帧重解析更便宜）；
 *  - 候选切点不安全（见 safeCutAt）就逐行回退，退到头则 committed 为空、
 *    tail 承接全文——退回空段总是安全的（单段渲染 = 原行为）；
 *  - committed + "\n" + tail 恒等于原文（只切行，不丢字符；committed 为空时
 *    tail === 原文）。
 *  性能纪律：本函数在**每个浇注帧**被调用（AnswerBody），不许对全文做
 *  split("\n")/join——那与 parseBlocks 同量级，两段渲染的收益会被它吃光。
 *  实现：行边界用 lastIndexOf 定位、围栏奇偶用「全文计数 ⊕ tail 计数」，
 *  全文只做一次 C 速扫描（countFences），其余操作都只在末尾几行上。 */
export function splitCommitted(text: string): { committed: string; tail: string } {
  let cut = text.lastIndexOf("\n"); // committed = text.slice(0, cut)，tail = cut+1 起
  if (cut <= 0) return { committed: "", tail: text };
  const totalFences = countFences(text);
  while (cut > 0) {
    const tail = text.slice(cut + 1);
    if (safeCutAt(text, cut, tail, totalFences)) {
      return { committed: text.slice(0, cut), tail };
    }
    cut = text.lastIndexOf("\n", cut - 1);
  }
  return { committed: "", tail: text };
}

/** ``` 出现次数（与 Markdown 入口的 text.split("```") 同语义——行内出现也计入）。 */
function countFences(s: string): number {
  let n = 0, i = s.indexOf("```");
  while (i >= 0) {
    n++;
    i = s.indexOf("```", i + 3);
  }
  return n;
}

/** 切点 cut（text[cut] 是 "\n"，tail = cut+1 起）是否安全：committed 与 tail
 *  各自解析出的块序列拼接后与整段解析逐块一致。四条判定：
 *  ① committed 内 ``` 计数为偶数（切点不在围栏内——围栏内任何切分都会把一段
 *     代码渲染成两段 <pre>）；
 *  ② committed 不以空行收尾（否则 tail 首块会丢段距 gap 标记——与整段解析
 *     不一致；回退到更早的非空行）；
 *  ③ tail 首行不得延续 committed 末行的多行块（quote/list/table——这些块是
 *     「连续同类行聚块」，切在中间 = 一个块渲染成两个）。 */
function safeCutAt(text: string, cut: number, tail: string, totalFences: number): boolean {
  const committedFences = totalFences - countFences(tail);
  if (committedFences % 2 !== 0) return false;
  const prevStart = text.lastIndexOf("\n", cut - 1) + 1;
  const prev = text.slice(prevStart, cut);
  if (prev.trim() === "") return false;
  const next = tail.split("\n", 1)[0] ?? "";
  const prevList = RE_UL_LINE.test(prev) || RE_OL_LINE.test(prev);
  const nextList = RE_UL_LINE.test(next) || RE_OL_LINE.test(next);
  if (prevList && nextList) return false;
  if (RE_QUOTE_LINE.test(prev) && RE_QUOTE_LINE.test(next)) return false;
  if (isTableRowLine(prev) && isTableRowLine(next)) return false;
  return true;
}

/**
 * 按节奏揭示持续增长的流式文本。
 * @param text - 已累计的全部流式文本。
 * @returns 当前可见的 text 前缀。
 */
export function useStreamReveal(text: string): string {
  // 挂载即显示全文（流中途重挂载不能从头回放）；只有挂载后的增长走节奏。
  const [shown, setShown] = useState(text);
  const shownRef = useRef(text);
  const textRef = useRef(text);
  const frameRef = useRef(0);
  const lastRef = useRef(0);

  const cancelFrame = (): void => {
    if (frameRef.current !== 0) {
      cancelAnimationFrame(frameRef.current);
      frameRef.current = 0;
    }
  };
  const snap = (): void => {
    cancelFrame();
    shownRef.current = textRef.current;
    setShown(shownRef.current);
  };
  const pour = (): void => {
    cancelFrame();
    const tick = (now: number): void => {
      const elapsed = Math.max(0, now - lastRef.current);
      lastRef.current = now;
      const next = pourStep(shownRef.current, textRef.current, elapsed);
      if (next === shownRef.current) {
        frameRef.current = 0;
        return;
      }
      shownRef.current = next;
      setShown(next);
      frameRef.current = next === textRef.current ? 0 : requestAnimationFrame(tick);
    };
    lastRef.current = performance.now();
    frameRef.current = requestAnimationFrame(tick);
  };

  useEffect(() => {
    textRef.current = text;
    const plan = revealPlan(shownRef.current, text, {
      visible: !documentHidden(),
      motion: pacingAllowed(),
      lagLimit: REVEAL_MAX_LAG,
    });
    if (plan === "pour") {
      pour();
      return cancelFrame;
    }
    if (plan === "snap") snap();
    else cancelFrame(); // none / hold（hold：冻结，等恢复钩子落账）
  }, [text]);

  // 恢复可见的统一落账口：hidden 期间冻结的积压在这里处理。流在隐藏期间
  // 已结束（不再有 delta 触发 effect）也必须落账，否则内容永远不上屏。
  useEffect(() => {
    const onVisibilityChange = (): void => {
      if (documentHidden()) {
        cancelFrame();
        return;
      }
      const plan = revealPlan(shownRef.current, textRef.current, {
        visible: true,
        motion: pacingAllowed(),
        lagLimit: REVEAL_RESUME_JUMP,
      });
      if (plan === "snap") snap();
      else if (plan === "pour") pour();
      else cancelFrame();
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => document.removeEventListener("visibilitychange", onVisibilityChange);
  }, []);

  return shown;
}
