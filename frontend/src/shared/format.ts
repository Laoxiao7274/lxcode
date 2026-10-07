// 跨组件小格式化工具（此前 kfmt/kfmtTok 两处各写了一份）。

/** token 数显示：128000 → 128k；34200 → 34.2k；800 → 800。 */
export function kfmtTokens(n: number): string {
  if (n >= 100_000) return `${Math.round(n / 1000)}k`;
  if (n >= 1000) return `${(n / 1000).toFixed(1).replace(/\.0$/, "")}k`;
  return String(n);
}

/** 上下文/输出上限显示：0 = 没配（未知），显示「未知」而不是 0。
 *
 *  0 不是「窗口是 0」，是「不知道」——与上下文指示器对未知窗口显示中性态同一条
 *  口径。显示 0 会让人以为这是个真实读数（而窗口未知时压缩根本不触发）。 */
export function kfmtLimit(n: number): string {
  return n > 0 ? kfmtTokens(n) : "未知";
}
