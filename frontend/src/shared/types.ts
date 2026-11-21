// UI 事件模型——字段语义与后端 internal/protocol 一一对应（demo 与 live
// 两个 AgentSource 实现都发这套事件，UI 层不感知数据来源）。
//
// 本文件是**桶导出**：类型按协议域拆在同目录 types/ 下（git log 见拆分提交），
// import 路径与对外 API 保持不变——现有 `from "../shared/types"` 零改动。
export * from "./types/context";
export * from "./types/rewind";
export * from "./types/jobs";
export * from "./types/attachments";
export * from "./types/events";
export * from "./types/chat";
export * from "./types/git";
export * from "./types/source";
export * from "./types/search";
export * from "./types/settings";
export * from "./types/catalog";
export * from "./types/agent-admin";
export * from "./types/screen";
