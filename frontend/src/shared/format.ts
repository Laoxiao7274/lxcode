// 跨组件小格式化工具（此前 kfmt/kfmtTok 两处各写了一份）。

/** token 数显示：128000 → 128k；34200 → 34.2k；800 → 800。 */
export function kfmtTokens(n: number): string {
  if (n >= 100_000) return `${Math.round(n / 1000)}k`;
  if (n >= 1000) return `${(n / 1000).toFixed(1).replace(/\.0$/, "")}k`;
  return String(n);
}
