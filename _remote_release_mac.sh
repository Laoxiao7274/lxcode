#!/bin/bash
# mac 发版脚本（与 _remote_release.sh 同模式：经 SSH 在远程执行；token 从进程
# environ 运行时取，不落盘不回显）。
#
# 用途：上传 mac 产物（dmg×2 + update-mac zip + manifest-mac 哈希）到
# site-backend，走 check → upload → publish → 验证 manifest-mac.json。
# Windows 产物（installer/update）应由 _remote_release.sh 单独发（或同一版本
# 先跑 Windows 再跑本脚本补 mac 产物——两次上传同一 version：第二次必须包含
# **全部**产物文件，因为 upload 是整体替换该版本的产物集合与清单）。
#
# 产物来源（在 mac 上跑 scripts/build.mjs --mac 产出，位于 shell/release/）：
#   Lxcode-<version>-arm64.dmg / Lxcode-<version>-x64.dmg（electron-builder 默认名）
#   update-mac-<version>.zip
#   manifest-mac.json（其中 sha256 = update-mac zip 的哈希，即 manifestMacSha256）
#
# 用法（SSH 远程）：
#   ssh <host> 'bash -s' < _remote_release_mac.sh
# 或本地改 VERSION/路径后按段执行。发版前先在 mac 上：
#   node scripts/build.mjs --mac        # arm64（默认）
#   node scripts/build.mjs --mac --x64  # x64（第二份 dmg；update zip 哈希以最后
#                                       #   一轮为准，两轮产物内容一致——app.asar
#                                       #   与后端同版本同内容，仅启动架构不同）
set -e

VERSION=0.2.0          # ← 发版前改：与 shell/package.json 的 version 一致
REL=/tmp/lxmac         # ← mac 产物所在目录（shell/release 拷到远程的落点）
ELECTRON=40.10.2       # ← 与 shell/package.json devDependencies.electron 一致

cd "$REL"
PID=$(systemctl --user show -p MainPID lxcode-site.service --value)
TOKEN=$(tr '\0' '\n' < /proc/$PID/environ | grep '^LXCODE_SITE_TOKEN=' | cut -d= -f2-)
if [ -z "$TOKEN" ]; then echo "TOKEN-FAIL (pid=$PID)"; exit 1; fi
echo "token ok (pid=$PID)"
AUTH="Authorization: Bearer $TOKEN"
BASE=http://127.0.0.1:13100

# manifest-mac.json 里的 sha256 就是 update-mac zip 的哈希——直接从产物清单读，
# 不手抄（build.mjs 产出的 manifest-mac.json 与 zip 同目录同轮产出，天然一致）。
MAC_SHA=$(python3 -c "import json;print(json.load(open('manifest-mac.json'))['sha256'])")
echo "update-mac sha256 = $MAC_SHA"

echo "=== check (dry-run) ==="
curl -sS -X POST -H "$AUTH" \
  -F "version=$VERSION" -F "channel=stable" -F "electronVersion=$ELECTRON" \
  -F "manifestSha256=$(python3 - <<PY
import json,hashlib
# Windows 侧声明哈希：同一版本若已由 _remote_release.sh 发过，这里必须一致；
# 若本脚本单独发（不带 Windows 产物），留空即可——check 会回落到上传 zip 的真哈希。
PY
)" \
  -F "manifestMacSha256=$MAC_SHA" \
  -F "installer=@$REL/Lxcode Setup $VERSION.exe" \
  -F "update=@$REL/update-$VERSION.zip" \
  -F "macInstallerArm64=@$REL/Lxcode-$VERSION-arm64.dmg" \
  -F "macInstallerX64=@$REL/Lxcode-$VERSION-x64.dmg" \
  -F "macUpdate=@$REL/update-mac-$VERSION.zip" \
  "$BASE/api/admin/releases/check" > check.json
cat check.json; echo
if ! grep -q '"canPublish":true' check.json && ! grep -q '"canPublish": *true' check.json; then
  echo "CHECK-FAIL"; exit 1
fi

echo "=== upload (draft) ==="
curl -sS -X POST -H "$AUTH" \
  -F "version=$VERSION" -F "channel=stable" -F "electronVersion=$ELECTRON" \
  -F "manifestMacSha256=$MAC_SHA" \
  -F "installer=@$REL/Lxcode Setup $VERSION.exe" \
  -F "update=@$REL/update-$VERSION.zip" \
  -F "macInstallerArm64=@$REL/Lxcode-$VERSION-arm64.dmg" \
  -F "macInstallerX64=@$REL/Lxcode-$VERSION-x64.dmg" \
  -F "macUpdate=@$REL/update-mac-$VERSION.zip" \
  "$BASE/api/admin/releases" > upload.json
cat upload.json; echo
if ! grep -q "\"version\":\"$VERSION\"" upload.json; then
  echo "UPLOAD-FAIL"; exit 1
fi

echo "=== publish ==="
curl -sS -X POST -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"status":"publish"}' "$BASE/api/admin/releases/$VERSION/status"; echo

echo "=== verify: manifest-mac.json ==="
curl -sS "$BASE/manifest-mac.json"; echo
echo "=== verify: manifest.json（Windows 契约不变） ==="
curl -sS "$BASE/manifest.json"; echo
echo "=== verify: 下载 update-mac zip 并核对哈希 ==="
curl -sS -o /tmp/verify-mac.zip "$BASE/releases/update-mac-$VERSION.zip"
echo "实际 sha256 = $(sha256sum /tmp/verify-mac.zip | cut -d' ' -f1)（应等于 manifest-mac.json 的 sha256）"
echo "=== verify: latest ==="
curl -sS "$BASE/api/releases/latest"; echo
