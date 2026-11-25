#!/bin/bash
# 0.3.66 发布脚本（经 SSH 在远程执行；token 从 lxcode-site.service 进程 environ 运行时取，不落盘不回显）
# 用法：本地产物先由 remote.py put 到 /tmp/lx066/，再执行本脚本
set -e
cd /tmp/lx066
PID=$(systemctl --user show -p MainPID lxcode-site.service --value)
TOKEN=$(tr '\0' '\n' < /proc/$PID/environ | grep '^LXCODE_SITE_TOKEN=' | cut -d= -f2-)
if [ -z "$TOKEN" ]; then echo "TOKEN-FAIL (pid=$PID)"; exit 1; fi
echo "token ok (pid=$PID)"
AUTH="Authorization: Bearer $TOKEN"
BASE=http://127.0.0.1:13100
VER=0.3.66
MANIFEST_SHA=91582b118746f6d3ce914e4216477bd61980163609f492cc3dd2e8eed3c916be

echo "=== check (dry-run) ==="
curl -sS -X POST -H "$AUTH" \
  -F "version=$VER" -F "channel=stable" -F "electronVersion=40.10.2" \
  -F "manifestSha256=$MANIFEST_SHA" \
  -F "notes=<notes.txt" \
  -F "installer=@/tmp/lx066/Lxcode Setup 0.3.66.exe" \
  -F "update=@/tmp/lx066/update-0.3.66.zip" \
  "$BASE/api/admin/releases/check" > check.json
cat check.json; echo
if ! grep -q '"canPublish": *true' check.json && ! grep -q '"canPublish":true' check.json; then
  echo "CHECK-FAIL"; exit 1
fi

echo "=== upload (draft) ==="
curl -sS -X POST -H "$AUTH" \
  -F "version=$VER" -F "channel=stable" -F "electronVersion=40.10.2" \
  -F "manifestSha256=$MANIFEST_SHA" \
  -F "notes=<notes.txt" \
  -F "installer=@/tmp/lx066/Lxcode Setup 0.3.66.exe" \
  -F "update=@/tmp/lx066/update-0.3.66.zip" \
  "$BASE/api/admin/releases" > upload.json
cat upload.json; echo
if ! grep -q "\"version\":\"$VER\"" upload.json; then
  echo "UPLOAD-FAIL"; exit 1
fi

echo "=== publish ==="
curl -sS -X POST -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"status":"publish"}' "$BASE/api/admin/releases/$VER/status"; echo

echo "=== verify: manifest ==="
curl -sS "$BASE/manifest.json" | head -5; echo
echo "=== verify: latest ==="
curl -sS "$BASE/api/releases/latest"; echo
