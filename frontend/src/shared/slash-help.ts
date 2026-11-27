// 斜杠命令的「/help」清单文案（A2）：主域与助手域共用一份——两边命令面板的
// 命令集略有差别（助手域没有页面导航），清单按域拆两条。纯文案常量，测试直测。

/** 主域（项目模式）命令清单。 */
export const SLASH_HELP_TEXT = [
  "/new 开始新对话 · /compact 压缩早期历史",
  "/model 选本会话模型 · /effort 推理强度 · /approval 权限档",
  "/jobs 后台任务 · /agents Agent 组装 · /catalog 拓展 · /settings 设置",
].join("\n");

/** 助手域命令清单。 */
export const SLASH_HELP_TEXT_ASSISTANT = [
  "/compact 压缩早期历史",
  "/model 选本会话模型 · /effort 推理强度 · /approval 权限档",
].join("\n");
