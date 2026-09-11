// Mock 设置：模型 / 推理强度 / 高危确认模式（原型数据，后端接入时
// 换成 model.list + 会话参数）。选中态存内存——刷新即复位，符合原型定位。
import { createContext, useContext, useState, type ReactNode } from "react";

export interface Settings {
  model: string;
  effort: "low" | "medium" | "high";
  approval: "auto" | "confirm" | "strict";
  /** 思考链显示（对话里的推理过程折叠块）。 */
  showThinking: boolean;
  /** 命令输出完整展示（Codex General 同款；关闭折叠为摘要）。 */
  showFullOutput: boolean;
  /** 生成时阻止休眠（Codex General 同款）。 */
  keepAwake: boolean;
  /** Enter 发送（关闭则 Cmd+Enter 多行——Codex General 同款）。 */
  enterToSend: boolean;
  /** 回答语气（Codex Personalization：friendly/pragmatic/none）。 */
  personality: "friendly" | "pragmatic" | "none";
}

const DEFAULTS: Settings = {
  model: "MYT",
  effort: "medium",
  approval: "confirm",
  showThinking: true,
  showFullOutput: true,
  keepAwake: false,
  enterToSend: true,
  personality: "pragmatic",
};;

export const MODELS = [
  { id: "MYT", desc: "本机网关 · 日常任务" },
  { id: "MYT-Deep", desc: "本机网关 · 深度推理" },
  { id: "deepseek-chat", desc: "DeepSeek 官方 API" },
];

export const EFFORTS: { id: Settings["effort"]; label: string; hint: string }[] = [
  { id: "low", label: "低", hint: "快，省 token" },
  { id: "medium", label: "中", hint: "均衡" },
  { id: "high", label: "高", hint: "慢，想得更深" },
];

export const APPROVALS: { id: Settings["approval"]; label: string; hint: string }[] = [
  { id: "auto", label: "完全访问", hint: "高权限，几乎不打断；仅在可随时销毁的隔离环境用" },
  { id: "confirm", label: "默认", hint: "工作区内自动改与跑命令，越界才询问（推荐）" },
  { id: "strict", label: "只读", hint: "规划、审查、问答，不改文件、不联网" },
];

const Ctx = createContext<{
  settings: Settings;
  set: (patch: Partial<Settings>) => void;
}>({ settings: DEFAULTS, set: () => {} });

export function SettingsProvider({ children }: { children: ReactNode }) {
  const [settings, setSettings] = useState<Settings>(DEFAULTS);
  const set = (patch: Partial<Settings>) => setSettings((s) => ({ ...s, ...patch }));
  return <Ctx.Provider value={{ settings, set }}>{children}</Ctx.Provider>;
}

export function useSettings() {
  return useContext(Ctx);
}
