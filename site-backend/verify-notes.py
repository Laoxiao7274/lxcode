#!/usr/bin/env python3
# 验证 0.1.1 的状态与 notes（走本地 API，避免 PS 引号地狱）。
import json, urllib.request
rs = json.load(urllib.request.urlopen("http://127.0.0.1:13100/api/releases"))
r = next(x for x in rs if x["version"] == "0.1.1")
print("status:", r["status"])
print("notes:", json.dumps(r["notes"], ensure_ascii=False))
m = json.load(urllib.request.urlopen("http://127.0.0.1:13100/manifest.json"))
print("manifest:", m["version"], m["url"])
