# 假后端：脚本化推送 dispatch 事件序列，验证派发卡实时子工具流（不耗真实 LLM 消息）。
# 序列：hello → session.new → chat.send 接受 → userMessage → dispatchStart
#       → toolCall(read_file, dispatch_id=d-1) → [停 6s] → toolResult → dispatchEnd
import json, threading
from http.server import BaseHTTPRequestHandler, HTTPServer
import socketserver

WS_PORT = 8888
from websocket import create_connection, WebSocket

clients = []

def push(obj):
    frame = json.dumps(obj)
    for c in list(clients):
        try:
            c.send(frame)
        except Exception:
            pass

def event(method, params):
    push({"jsonrpc": "2.0", "method": method, "params": params})

def handle(c):
    clients.append(c)
    print("client connected")
    try:
        while True:
            m = json.loads(c.recv())
            i = m.get("id")
            method = m.get("method")
            print("<-", method, i)
            if method == "connection.hello":
                push({"jsonrpc": "2.0", "id": i, "result": {"server": "lxcode", "version": "2", "busy": False}})
            elif method == "session.list":
                push({"jsonrpc": "2.0", "id": i, "result": []})
            elif method == "session.new":
                push({"jsonrpc": "2.0", "id": i, "result": {"session_id": "fake-1"}})
            elif method == "chat.send":
                push({"jsonrpc": "2.0", "id": i, "result": {"session_id": "fake-1"}})
                threading.Thread(target=run_turn, daemon=True).start()
            else:
                push({"jsonrpc": "2.0", "id": i, "result": {}})
    except Exception as e:
        print("closed", e)
        clients.remove(c)

def run_turn():
    time.sleep(0.5)
    sid = "fake-1"
    event("chat.userMessage", {"session_id": sid, "message": {"role": "user", "content": "hi", "seq": 1}})
    event("chat.dispatchStart", {
        "owner_session_id": sid, "dispatch_id": "d-1", "session_id": "child-1",
        "agent_id": "coder", "agent_name": "代码 Agent", "agent_color": "#3b82f6",
        "task": "fake task",
    })
    time.sleep(1)
    event("chat.toolCall", {
        "session_id": sid, "dispatch_id": "d-1", "id": "t1", "name": "read_file",
        "arguments": "{\"path\": \"go.mod\"}",
    })
    time.sleep(6)
    event("chat.toolResult", {
        "session_id": sid, "dispatch_id": "d-1", "id": "t1", "name": "read_file",
        "content": "module example", "is_error": False,
    })
    event("chat.dispatchEnd", {
        "owner_session_id": sid, "dispatch_id": "d-1", "session_id": "child-1",
        "result": "fake conclusion", "is_error": False,
    })
    event("chat.done", {"session_id": sid, "message": {"role": "assistant", "content": "done", "seq": 2}})

class H(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b"ok")
    def log_message(self, *a): pass

def serve_ws():
    while True:
        try:
            c = create_connection("ws://localhost:%d/x" % 0)  # never used
        except Exception:
            break

# 简易 WS 服务器：websocket-client 没有 server 能力，改用原始 socket 实现握手+帧。
import socket, base64, hashlib, struct

GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

def ws_send(conn, text):
    data = text.encode()
    header = bytearray([0x81])
    n = len(data)
    if n < 126:
        header.append(n)
    elif n < 65536:
        header.append(126); header += struct.pack(">H", n)
    else:
        header.append(127); header += struct.pack(">Q", n)
    conn.send(bytes(header) + data)

def ws_recv(conn):
    # 读一帧（服务器侧，客户端帧带 mask）
    b1, b2 = conn.recv(2)
    if not b2: return None
    n = b2 & 0x7F
    if n == 126: n = struct.unpack(">H", conn.recv(2))[0]
    elif n == 127: n = struct.unpack(">Q", conn.recv(8))[0]
    mask = conn.recv(4)
    data = bytearray()
    while len(data) < n:
        chunk = conn.recv(n - len(data))
        if not chunk: return None
        data += chunk
    data = bytes(b ^ mask[i % 4] for i, b in enumerate(data))
    return data.decode()

def client_thread(conn):
    c = FakeClient(conn)
    clients.append(c)
    print("client connected")
    try:
        while True:
            m = ws_recv(conn)
            if m is None: break
            m = json.loads(m)
            i, method = m.get("id"), m.get("method")
            print("<-", method, i)
            if method == "connection.hello":
                c.send({"jsonrpc": "2.0", "id": i, "result": {"server": "lxcode", "version": "2", "busy": False}})
            elif method == "session.list":
                c.send({"jsonrpc": "2.0", "id": i, "result": []})
            elif method == "session.new":
                c.send({"jsonrpc": "2.0", "id": i, "result": {"session_id": "fake-1"}})
            elif method == "chat.send":
                c.send({"jsonrpc": "2.0", "id": i, "result": {"session_id": "fake-1"}})
                threading.Thread(target=run_turn, daemon=True).start()
            else:
                c.send({"jsonrpc": "2.0", "id": i, "result": {}})
    except Exception as e:
        print("closed:", e)
        if c in clients: clients.remove(c)

class FakeClient:
    def __init__(self, conn): self.conn = conn
    def send(self, obj): ws_send(self.conn, json.dumps(obj))

def main():
    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("127.0.0.1", WS_PORT))
    srv.listen(5)
    print("fake backend on", WS_PORT)
    while True:
        conn, _ = srv.accept()
        req = conn.recv(4096).decode(errors="replace")
        key = ""
        for line in req.split("\r\n"):
            if line.lower().startswith("sec-websocket-key:"):
                key = line.split(":", 1)[1].strip()
        accept = base64.b64encode(hashlib.sha1((key + GUID).encode()).digest()).decode()
        conn.send(("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n"
                   "Connection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n" % accept).encode())
        threading.Thread(target=client_thread, args=(conn,), daemon=True).start()

threading.Thread(target=main, daemon=True).start()
import time
while True:
    time.sleep(3600)
