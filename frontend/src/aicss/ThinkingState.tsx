// aicss 组件 vendor 适配版（@aicss/react 0.1.3, MIT, aicss.dev）
// 适配：去掉 lucide-react 依赖（图标内联 SVG）；演示文案改受控 props；
// 中文标签。CSS module 原样保留（视觉与动效是组件的核心资产）。
import styles from "./ThinkingState.module.css";

/** 思考状态行：shimmer 流光文字（"正在分析…"）。phase=done 时静态。 */
export function ThinkingState({ text, done }: { text: string; done?: boolean }) {
  return (
    <span className={done ? undefined : styles.shimmer} style={done ? { color: "#a1a1a1" } : undefined}>
      {text}
    </span>
  );
}
