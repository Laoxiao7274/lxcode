// 对话块的纯工具函数（参数摘要/终端首行还原/截断）。

/** 截断显示（超出加省略号）。 */
export function clip(s: string, max: number): string {
  if (s.length <= max) return s;
  return s.slice(0, max) + "…";
}

/** 工具头部的参数摘要（bash 显示命令，别的显示 path/pattern 等首字段）。 */
export function shortArgs(block: { name: string; arguments: string }): string {
  try {
    const a = JSON.parse(block.arguments);
    const first = a.command ?? a.path ?? a.pattern ?? a.cwd ?? "";
    return String(first);
  } catch {
    return block.arguments;
  }
}

/** 终端块首行：还原"看起来像命令"的那一行（bash 原样，read_file 拼成 cat）。 */
export function prettyCmdline(name: string, args: string): string {
  try {
    const a = JSON.parse(args);
    switch (name) {
      case "bash":
        return a.command ?? "";
      case "read_file":
        return "cat " + (a.path ?? "");
      case "search":
        return "grep " + (a.pattern ?? "") + " " + (a.path ?? ".");
      case "write_file":
        return "write " + (a.path ?? "");
      case "edit":
        return "edit " + (a.path ?? "");
      default:
        return name;
    }
  } catch {
    return name;
  }
}

/** 确认卡的命令文本。 */
export function prettyCommand(req: { arguments: string }): string {
  try {
    const a = JSON.parse(req.arguments);
    return a.command ?? req.arguments;
  } catch {
    return req.arguments;
  }
}

/** 确认卡的工作目录（无则不显示）。 */
export function prettyCwd(req: { arguments: string }): string | undefined {
  try {
    const a = JSON.parse(req.arguments);
    return a.cwd;
  } catch {
    return undefined;
  }
}
