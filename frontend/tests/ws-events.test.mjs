// events.ts 的纯映射测试：字段名、默认值、归属是最容易写错的一层——
// 后端改一个键名不会让前端编译失败，只会静默变成空字符串。
import test from "node:test";
import assert from "node:assert/strict";
import { mapEvent } from "../src/agent/ws/events.ts";

test("dispatch 事件按 owner_session_id 归属主时间线，子会话 id 另带", () => {
  const ev = mapEvent("chat.dispatchStart", {
    session_id: "child-1", owner_session_id: "main-1", dispatch_id: "d1",
    agent_id: "coder", agent_name: "代码", agent_color: "#f00", task: "改一下",
  });
  assert.deepEqual(ev, {
    type: "dispatchStart", sessionId: "main-1", dispatchId: "d1", childSessionId: "child-1",
    agentId: "coder", agentName: "代码", agentColor: "#f00", task: "改一下",
  });
  // 子会话 id 缺席时不能变成字符串 "undefined"
  assert.equal(mapEvent("chat.dispatchStart", { owner_session_id: "m", dispatch_id: "d" }).childSessionId, undefined);
});

test("agent_color 有默认值（后端没给也要有颜色）", () => {
  assert.equal(mapEvent("chat.dispatchStart", {}).agentColor, "#3b82f6");
});

test("子轮的事件带 dispatchId，主轮不带", () => {
  const child = mapEvent("chat.delta", { session_id: "s", kind: "text", text: "x", dispatch_id: "d1" });
  assert.equal(child.dispatchId, "d1");
  const main = mapEvent("chat.delta", { session_id: "s", kind: "text", text: "x" });
  assert.equal(main.dispatchId, undefined);
});

test("done 只在后端报了 context 时才带 context", () => {
  const withCtx = mapEvent("chat.done", { session_id: "s", usage_tokens: 7, context: { used: 3 } });
  assert.equal(withCtx.usageTokens, 7);
  assert.deepEqual(withCtx.context, { used: 3 });
  // 未知占用整键缺席（不是编一个 0——指示器要显示中性态）
  assert.equal(mapEvent("chat.done", { session_id: "s" }).context, undefined);
  assert.equal(mapEvent("chat.done", { session_id: "s" }).finishReason, "stop");
});

test("files.changed 没有 files 数组时不产出事件", () => {
  assert.equal(mapEvent("files.changed", { session_id: "s" }), null);
  const ev = mapEvent("files.changed", { session_id: "s", files: [{ path: "a.go", added: 1, deleted: 2, diff: "@@" }] });
  assert.equal(ev.type, "filesChanged");
  assert.equal(ev.files[0].path, "a.go");
});

test("userMessage 兼容 message 对象与早期扁平形状", () => {
  assert.equal(mapEvent("chat.userMessage", { session_id: "s", message: { content: "hi" } }).text, "hi");
  assert.equal(mapEvent("chat.userMessage", { session_id: "s", text: "hi" }).text, "hi");
});

test("只重拉事实源的事件不产出前端事件（由 reactTo 负责）", () => {
  for (const m of ["model.changed", "project.changed", "agent.changed", "catalog.changed", "search.changed"]) {
    assert.equal(mapEvent(m, {}), null, m);
  }
});

test("未知方法与 null 载荷都安全", () => {
  assert.equal(mapEvent("nope", {}), null);
  assert.equal(mapEvent("connection.ready", null).type, "ready");
  assert.equal(mapEvent("connection.ready", null).busy, false);
});
