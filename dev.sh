#!/usr/bin/env bash
# dev.sh —— 开发环境一键入口（bash 版）。
# 薄包装：定位仓库根 + 依赖预检 + 参数默认，然后 exec 移交 scripts/dev.mjs——
# 进程管理（后端/vite/electron 的拉起、杀树、端口等待）全在 dev.mjs，
# 不在 bash 里复制第二份逻辑（重复治理纪律）。
#
#   ./dev.sh              壳开发模式（无参默认 --electron——日常形态）
#   ./dev.sh --electron   同上（dev.mjs 的检测幂等，重复传参无害）
#   ./dev.sh <其它参数>   原样透传（纯浏览器模式仍用 node scripts/dev.mjs）
set -euo pipefail

# 定位仓库根（dev.mjs 用 process.cwd() 做 ROOT，必须从仓库根启动）
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

command -v node >/dev/null 2>&1 || { echo "错误: node 不在 PATH"; exit 1; }

# 无参数默认壳模式——壳是产品形态，日常开发就是要它
if [ $# -eq 0 ]; then
  set -- "--electron"
  echo "[dev.sh] 未传参数，默认壳模式（--electron）"
fi

# 依赖预检：缺了自动装（幂等，已装目录直接跳过；config/local.json 之类不受影响）
if [ ! -d frontend/node_modules ]; then
  echo "[dev.sh] frontend 依赖缺失，安装中..."
  (cd frontend && npm install --no-audit --no-fund)
fi
for a in "$@"; do
  if [ "$a" = "--electron" ] && [ ! -d shell/node_modules ]; then
    echo "[dev.sh] shell 依赖缺失，安装中..."
    (cd shell && npm install --no-audit --no-fund)
  fi
done

exec node scripts/dev.mjs "$@"
