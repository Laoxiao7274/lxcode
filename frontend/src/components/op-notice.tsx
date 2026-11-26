// 操作提示条（请求级失败的一次性提示）：项目模式与助手模式**共用一份渲染**
//（P0-2 助手域错误可见——AssistantWorkspace 也挂它，双模式共享层）。App 持有
// 状态（state.operationError / clearError），经 props 注入，两处不各写一份标记。
//
// **不复用 .error-block**——那条是时间线内的行内文本（Block/SettingsPanel 也在
// 用），给它加底色会连带改掉那两处的排版。这里要的是一条独立的、可关闭的提示条
//（与 .job-notice 同款形态：左缘竖线 + 圆角 + 全宽），只是走危险色。
import { Button } from "./form";

export function OpNotice({ message, onClose }: { message: string; onClose: () => void }) {
  return (
    <div className="op-notice" role="alert">
      <span className="op-notice-icon" aria-hidden>
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
          <path d="M12 9v4" />
          <path d="M12 17h.01" />
        </svg>
      </span>
      <span className="op-notice-text">{message}</span>
      {/* 关闭走表单套件（.fd-btn-g），不裸写原生 button——裸写就是 OS 默认皮肤 */}
      <Button className="op-notice-close" onClick={onClose}>关闭</Button>
    </div>
  );
}
