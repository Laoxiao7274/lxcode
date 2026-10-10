// sha256 常驻行（替掉上轮的折叠块）。
//
// 为什么不再折叠：`<details>` 展开会让卡脚 80 → 190px、卡片高度突变，窄屏还会换行——
// 用户原话「卡片变化很丑」。现在哈希常驻一行：中段省略（首 8 + 尾 6）+ 完整值放
// title/aria-label，行尾一个「复制」——**没有展开态，高度恒定**。
//
// 复制走 navigator.clipboard（需要安全上下文），失败回落 execCommand，
// 再失败就如实显示「复制失败」——不假装复制成功。

import { useEffect, useRef, useState } from "react";

/** 中段省略：首 8 + 尾 6。完整值在 title/aria-label 里，信息没有丢。 */
export function shortHash(hash: string): string {
  return hash.length <= 18 ? hash : `${hash.slice(0, 8)}…${hash.slice(-6)}`;
}

async function writeClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // 非安全上下文或权限被拒：回落到 execCommand
  }
  try {
    const area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.appendChild(area);
    area.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(area);
    return ok;
  } catch {
    return false;
  }
}

const COPY_LABEL = { idle: "复制", copied: "已复制 ✓", failed: "复制失败" } as const;

export function HashLine({ label, hash }: { label: string; hash: string }) {
  const [state, setState] = useState<keyof typeof COPY_LABEL>("idle");
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    [],
  );

  async function copy() {
    const ok = await writeClipboard(hash);
    setState(ok ? "copied" : "failed");
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setState("idle"), 1500);
  }

  return (
    <span className="hash-line">
      <span className="meta hash-k">{label} SHA256</span>
      <code className="hash hash-v" title={hash} aria-label={`${label} 的完整 SHA256：${hash}`}>
        {shortHash(hash)}
      </code>
      <button
        className="btn btn-quiet btn-sm hash-copy"
        type="button"
        aria-label={`复制 ${label} 的完整 SHA256`}
        onClick={copy}
      >
        {COPY_LABEL[state]}
      </button>
      <span className="sr-only" role="status">
        {state === "copied" ? "已复制到剪贴板" : state === "failed" ? "复制失败" : ""}
      </span>
    </span>
  );
}
