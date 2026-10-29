// 回放出的 dispatch 块要能在右侧面板里显示出 Agent 名——这是「重启后子 Agent 卡退化」
// 那个 bug 的**第二半**：时间线卡片修好了，但面板读的是 block.agentName，而回放块
// 的 agentName 是空串（父会话历史里只有 tool_call 的 arguments，没有展示名——那是注册表
// 的知识）。所以 dispatchItems 必须**带出 agentId**，面板层才能按它回落。
//
// 这条测试钉的是「回落的前提条件」本身：agentId 一旦没被带出来，面板就会显示空白行，
// 而界面上看起来一切正常（空字符串不报错）——正是这个 bug 当初的形态。
import test from "node:test";
import assert from "node:assert/strict";
import { dispatchItems } from "../src/components/panels/dispatch-list.ts";

test("回放出的 dispatch 块（agentName 空）仍带得出 agentId——面板据此回落名字，不留空白", () => {
  const blocks = [
    { kind: "dispatch", uid: 1, id: "c1", agentId: "researcher", agentName: "", agentColor: "", task: "通读仓库", status: "done", result: "读完", subBlocks: [] },
    { kind: "user", uid: 2, text: "你好" },
  ];
  const items = dispatchItems(blocks);
  assert.equal(items.length, 1, "只取 dispatch 块");
  assert.equal(items[0].agentId, "researcher", "agentId 必须带出来（面板回落的唯一依据）");
  assert.equal(items[0].agentName, "", "agentName 回放时确实是空的——所以回落必须在面板层做");
});

test("没有 agentId 的残缺 dispatch 块不崩（回落到空串，面板显示「未知 Agent」）", () => {
  const blocks = [{ kind: "dispatch", uid: 3, id: "c2", task: "t", status: "done", subBlocks: [] }];
  const items = dispatchItems(blocks);
  assert.equal(items.length, 1);
  assert.equal(items[0].agentId, "", "缺字段时收窄成空串而不是 undefined");
});
