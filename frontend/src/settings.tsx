// Mock 设置：模型 / 推理强度 / 高危确认模式（原型数据，后端接入时
// 换成 model.list + 会话参数）。选中态存内存——刷新即复位，符合原型定位。
import { createContext, useContext, useState, type ReactNode } from "react";

export interface Settings {
  model: string;
  effort: "low" | "medium" | "high";
  approval: "auto" | "confirm" | "strict";
  showThinking: boolean;
}

const DEFAULTS: Settings = {
  model: "MYT",
  effort: "medium",
  approval: "confirm",
  showThinking: true,
};

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
  { id: "auto", label: "自动执行", hint: "低危直接跑，高危也放行" },
  { id: "confirm", label: "确认后执行", hint: "高危操作先问你（推荐）" },
  { id: "strict", label: "全部确认", hint: "每个工具都问" },
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
