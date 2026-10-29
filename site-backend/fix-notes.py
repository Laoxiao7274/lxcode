#!/usr/bin/env python3
# 修正 0.1.2 发布记录的 notes（curl 上传时 PowerShell 把中文搞成了乱码）。
import json, sqlite3, sys

DB = "/home/linaro/lxcode-site/data/site.db"
NOTES = [
    "远程访问：局域网直连（访问令牌门）/ 樱花frp 公网穿透 / Tailscale 三模式",
    "更新器：manifest 校验 + 后端热替换 + asar 冷替换 + 自动重启",
    "孤儿兜底：壳被强杀后端自动退出（stdin-watch）",
    "修复：子会话滚动跟随、切换卡顿（冻结渲染 + 窗口化）、输入框自增高",
]
conn = sqlite3.connect(DB)
cur = conn.execute("SELECT notes FROM releases WHERE version = '0.1.2'")
row = cur.fetchone()
if row is None:
    print("没有 0.1.2"); sys.exit(1)
conn.execute("UPDATE releases SET notes = ? WHERE version = '0.1.2'", (json.dumps(NOTES, ensure_ascii=False),))
conn.commit()
row = conn.execute("SELECT notes FROM releases WHERE version = '0.1.2'").fetchone()
print("修后 notes:", row[0])
conn.close()
