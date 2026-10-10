#!/bin/bash
# 0.1.5 内网发布脚本（经 SSH 在远程执行；token 从进程 environ 运行时取，不落盘不回显）
set -e
cd /tmp/lx015
PID=$(systemctl --user show -p MainPID lxcode-site.service --value)
TOKEN=$(tr '\0' '\n' < /proc/$PID/environ | grep '^LXCODE_SITE_TOKEN=' | cut -d= -f2-)
if [ -z "$TOKEN" ]; then echo "TOKEN-FAIL (pid=$PID)"; exit 1; fi
echo "token ok (pid=$PID)"
AUTH="Authorization: Bearer $TOKEN"
BASE=http://127.0.0.1:13100

echo "=== check (dry-run) ==="
curl -sS -X POST -H "$AUTH" \
  -F "version=0.1.5" -F "channel=stable" -F "electronVersion=40.10.2" \
  -F "manifestSha256=4f8230eea6ca49c186524e801f21b89c6f9da49a43513cae5ea44f99ca0760a5" \
  -F "notes=<notes.txt" \
  -F "installer=@/tmp/lx015/Lxcode Setup 0.1.5.exe" \
  -F "update=@/tmp/lx015/update-0.1.5.zip" \
  "$BASE/api/admin/releases/check" > check.json
cat check.json; echo
if ! grep -q '"canPublish": *true' check.json && ! grep -q '"canPublish":true' check.json; then
  echo "CHECK-FAIL"; exit 1
fi

echo "=== upload (draft) ==="
curl -sS -X POST -H "$AUTH" \
  -F "version=0.1.5" -F "channel=stable" -F "electronVersion=40.10.2" \
  -F "manifestSha256=4f8230eea6ca49c186524e801f21b89c6f9da49a43513cae5ea44f99ca0760a5" \
  -F "notes=<notes.txt" \
  -F "installer=@/tmp/lx015/Lxcode Setup 0.1.5.exe" \
  -F "update=@/tmp/lx015/update-0.1.5.zip" \
  "$BASE/api/admin/releases" > upload.json
cat upload.json; echo
if ! grep -q '"version":"0.1.5"' upload.json; then
  echo "UPLOAD-FAIL"; exit 1
fi

echo "=== publish ==="
curl -sS -X POST -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"status":"publish"}' "$BASE/api/admin/releases/0.1.5/status"; echo

echo "=== verify: manifest ==="
curl -sS "$BASE/manifest.json"; echo
echo "=== verify: latest ==="
curl -sS "$BASE/api/releases/latest"; echo
echo "=== verify: all releases ==="
curl -sS "$BASE/api/releases"; echo
