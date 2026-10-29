// @aicss/react 0.1.3 (MIT) vendor — 视觉资产原样，标签中文化
import styles from "./TextResponse.module.css";
import type { ReactNode } from "react";

export function TextResponse({ children }: { children?: ReactNode }) {
  return <div className={styles.prose}>{children}</div>;
}
