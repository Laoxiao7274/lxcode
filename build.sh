#!/usr/bin/env bash
# build.sh —— Windows 安装包一键构建（bash 版）。
# 薄包装：定位仓库根 + 工具链与依赖预检，然后 exec 移交 scripts/build.mjs
# （Go 编译 → 渲染层 → stage → esbuild → electron-builder 全在 mjs 里）。
# 产物：shell/release/Lxcode Setup <version>.exe（NSIS one-click per-user）+ blockmap。
set -euo pipefail

# 定位仓库根（build.mjs 用 process.cwd() 做 ROOT，必须从仓库根启动）
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

command -v node >/dev/null 2>&1 || { echo "错误: node 不在 PATH"; exit 1; }
command -v go  >/dev/null 2>&1 || { echo "错误: go 不在 PATH"; exit 1; }

# 依赖预检（幂等）
if [ ! -d frontend/node_modules ]; then
  echo "[build.sh] frontend 依赖缺失，安装中..."
  (cd frontend && npm install --no-audit --no-fund)
fi
if [ ! -d shell/node_modules ]; then
  echo "[build.sh] shell 依赖缺失，安装中..."
  (cd shell && npm install --no-audit --no-fund)
fi

exec node scripts/build.mjs "$@"
