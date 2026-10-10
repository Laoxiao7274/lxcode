#!/usr/bin/env python
"""远程部署辅助（paramiko）：跑命令 + SFTP 传文件。

为什么用 paramiko 而不是 ssh/scp：Windows 上没有稳定可用的 ssh 客户端，
venv 里的 paramiko 是这条链上已经验证过的方式；Git Bash 下要加
MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*' 防路径被篡改。

用法：
  python scripts/remote.py run 'uname -m && systemctl --user status lxcode-site.service --no-pager'
  python scripts/remote.py put <本地文件> <远程路径>
  python scripts/remote.py get <远程路径> <本地文件>
"""

import sys
import paramiko

HOST = "10.10.0.146"
USER = "linaro"
PASSWORD = "linaro"


def connect() -> paramiko.SSHClient:
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(HOST, username=USER, password=PASSWORD, timeout=20)
    return client


def run(client: paramiko.SSHClient, script: str) -> int:
    """exec_command("bash -s") + stdin：多行脚本走 stdin，避免引号地狱。"""
    stdin, stdout, stderr = client.exec_command("bash -s")
    stdin.write(script)
    stdin.channel.shutdown_write()
    sys.stdout.write(stdout.read().decode("utf-8", "replace"))
    err = stderr.read().decode("utf-8", "replace")
    if err.strip():
        sys.stderr.write(err)
    return stdout.channel.recv_exit_status()


def main() -> int:
    client = connect()
    try:
        if sys.argv[1] == "run":
            return run(client, sys.argv[2] if len(sys.argv) > 2 else sys.stdin.read())
        sftp = client.open_sftp()
        if sys.argv[1] == "put":
            sftp.put(sys.argv[2], sys.argv[3])
            print(f"上传完成 {sys.argv[2]} → {sys.argv[3]}")
            return 0
        if sys.argv[1] == "putdir":
            import os

            local, remote = sys.argv[2], sys.argv[3]
            made = set()

            def ensure(path: str) -> None:
                parts = [p for p in path.strip("/").split("/") if p]
                for i in range(1, len(parts) + 1):
                    piece = "/" + "/".join(parts[:i])
                    if piece in made:
                        continue
                    try:
                        sftp.mkdir(piece)
                    except OSError:
                        pass
                    made.add(piece)

            count = 0
            for root, _dirs, files in os.walk(local):
                rel = os.path.relpath(root, local).replace("\\", "/")
                target_dir = remote if rel == "." else f"{remote}/{rel}"
                ensure(target_dir)
                for name in files:
                    sftp.put(os.path.join(root, name), f"{target_dir}/{name}")
                    count += 1
            print(f"上传目录完成 {local} → {remote}（{count} 个文件）")
            return 0
        if sys.argv[1] == "get":
            sftp.get(sys.argv[2], sys.argv[3])
            print(f"下载完成 {sys.argv[2]} → {sys.argv[3]}")
            return 0
        raise SystemExit(f"未知命令 {sys.argv[1]}")
    finally:
        client.close()


if __name__ == "__main__":
    raise SystemExit(main())
