// 产物链接：**真的下载**（/releases/... 经 vite proxy 到后端静态服务）。
//
// 上轮这里是「原型拦截」（点击不导航、出提示条）——那时 /releases/ 下没有真文件。
// 现在后端真的在发这些产物（已发布版本 200、撤回/草稿 404），所以拦截层撤掉：
// 链接保留 href（后端返回的文件名），点击就是下载。

import type { ReactNode } from "react";

import type { Artifact } from "../shared/release";
import { SITE_BASE } from "../shared/base";
import { artifactUrl } from "../shared/release";

export function ArtifactLink({
  artifact,
  className = "mono",
  children,
}: {
  artifact: Artifact;
  className?: string;
  children: ReactNode;
}) {
  return (
    <a
      className={className}
      href={SITE_BASE + artifactUrl(artifact.name)}
      title={`${artifact.name}（${artifact.sha256.slice(0, 12)}…）`}
      download={artifact.name}
    >
      {children}
    </a>
  );
}
