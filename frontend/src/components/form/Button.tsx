// 表单组件 · Button：黑主 / 描边次两档（原生 button 的默认皮肤不进表单）。
import type { ButtonHTMLAttributes } from "react";

export function Button({
  variant = "ghost",
  className,
  type = "button",
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "ghost" }) {
  return (
    <button
      type={type}
      className={(variant === "primary" ? "fd-btn-p" : "fd-btn-g") + (className ? " " + className : "")}
      {...rest}
    />
  );
}
