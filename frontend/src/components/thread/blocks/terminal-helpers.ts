// 终端卡辅助：命令行还原（工具语义 → shell 形态）与 cwd 段名。
import { prettyCmdline } from "../helpers";

/** 工具调用 → 终端命令行（bash 原样；read_file/search 等按语义还原）。 */
export function toolCmdline(toolName: string, toolArgs: string, command: string): string {
  return prettyCmdline(toolName, toolArgs) || command;
}

/** cwd → 提示符段名（DSH promptLabel：取末段；home 显示 ~）。 */
export function cwdSegment(cwd?: string): string | undefined {
  if (cwd === undefined) return undefined;
  const trimmed = cwd.replace(/[\\/]+$/, "");
  const segment = trimmed.split(/[\\/]/).pop();
  return segment === undefined || segment === "" ? cwd : segment;
}
