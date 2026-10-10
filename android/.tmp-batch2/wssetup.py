# 临时实例初始化：hello + agent.update 给主 Agent 白名单加 bash（确认门验收用）
import json, sys
from websocket import create_connection

ws = create_connection("ws://127.0.0.1:7790/rpc", timeout=10)
def call(i, method, params=None):
    ws.send(json.dumps({"jsonrpc": "2.0", "id": i, "method": method, **({"params": params} if params else {})}))
    while True:
        m = json.loads(ws.recv())
        if m.get("id") == i:
            return m

r = call(1, "connection.hello", {"client": "probe", "version": "2"})
print("hello:", r.get("result", r.get("error")))
r = call(2, "agent.list")
agents = r["result"]
main = next(a for a in agents if a["id"] == "main")
print("main tools before:", main["tools"])
if "bash" not in main["tools"]:
    main["tools"] = main["tools"] + ["bash"]
    r = call(3, "agent.update", {"agent": main})
    print("agent.update:", r.get("result", r.get("error")))
r = call(4, "agent.list")
print("main tools after:", next(a for a in r["result"] if a["id"] == "main")["tools"])
ws.close()
