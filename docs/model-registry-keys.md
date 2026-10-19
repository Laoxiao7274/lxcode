# 模型注册表的主键：组合键 `provider::id`

> 落地形态（用户拍板）。落地前的形态：注册表主键是纯 model id，两家提供商
> 想用同一个模型名就得让其中一家改名（现实里改不了——id 是端点定的）。

## 1. 一句话

注册表条目的主键从「纯 model id」扩展为「`provider::model`」，**provider 是
可选字段**：有它就用组合键，没有就退回纯 id。存量 models.json 没有这个字段，
于是**零迁移照旧工作**。

## 2. 规则（`internal/config/types.go`）

| 位置 | 值 |
|---|---|
| `ModelConfig.Provider` | 可选，JSON `provider`（`omitempty`——不写就不进磁盘） |
| `ModelConfig.Key()` | 有 provider → `provider + "::" + id`；无 provider → 纯 id |
| `config.SplitKey(ref)` | 拆引用为 (provider, id)；无分隔符时 provider 为空 |
| `config.Registry.Resolve/Get/Remove/SetRole` | 全部接受**引用**，内部走 `resolveLocked` |

**解析只有两条规则，顺序即优先级**：

1. 组合键精确匹配（`provider::id`）；
2. 纯 id 精确匹配（存量引用：老会话的 `sessions.model`、老角色的绑定值，本来
   对应的条目就没有 provider 字段）。

两条都不中就是**不存在**。**不跨 provider 猜**：`gpt-4o` 在 p1 下有、p3 下没有
时，拿 p1 的顶上是在替用户做决定，而引用多半已经错了——这类静默回落正是
modelByID / SetModel 一直坚持「不静默回落」的原因。

**校验**：`id` 与 `provider` 里都不能含 `::`（否则引用解析歧义，早拒）。

**磁盘格式版本仍是 1**：新增的是可选字段，旧文件加载时该字段为空即走纯 id
路径，没有需要迁移的东西——递增版本反而会触发「拒绝加载」的迁移逻辑。
`TestFileVersionUnchanged` 钉住这条。

## 3. 链路上谁存引用

| 位置 | 存什么 | 存量值 |
|---|---|---|
| `Registry.models` 的 map 键 | 规范键 `Key()` | 纯 id |
| `Registry.roles` | 规范键（SetRole/Load 都归一） | 纯 id（等价，读时能解析） |
| `sessions.model` | `SetModel` 返回的规范键 | 纯 id，解析时第 2 条命中 |
| `agents.model`（AgentDef.Model） | 用户写的引用，解析同上 | 纯 id |
| wire `chat.model.model_id` | 入参接受两种；回包 `Session.SetModel` 的规范键 | 纯 id |
| 前端 `ModelMeta.id` | 组合键（`mapModels` 产出）；`ModelMeta.modelId` 是纯 id 展示用 | 纯 id |

`Session.ModelID()` 也改回规范键——前端按它对齐选择态，两家提供商的同名模型
才不会互相高亮。

## 4. Update 的定位语义（刻意不做隐式搬家）

`Update` 按**传入的** `provider::id` 定位：目标不存在就报 `ErrNotFound`。
改 id 或改 provider **等于换主键**，与改动前「按 id 定位、改 id 等于删一条加
一条」逐字一致。刻意**不**做「按旧键隐式搬家」：换主键是破坏性操作，引用方
（会话选择 / Agent 绑定 / 角色）得自己跟着改；隐式搬家会留下「一半引用还指着
旧键」的半吊子状态。

## 5. UI 操作指引

- **加模型**：设置 → 模型 → 展开提供商卡 → 「+ 添加模型」，两个输入框：
  模型 ID（必填）+ 提供商标签（选填）。填了标签，注册表键就是 `标签::模型 ID`，
  于是同一个模型 ID 在另一家提供商下可以再加一条而不冲突。
- **改标签**：模型行右侧铅笔（模型配置）里改「提供商标签」；清空即回到存量
  的纯 ID 语义（注意这会换主键）。
- **怎么用**：同一个模型 id（如 `gpt-4o`）在 OpenRouter 和自建 vLLM 上各加一条，
  分别给标签 `openrouter` 与 `local`，然后在输入区的模型选择器里挑——列表按
  端点分组，同名的两个条目各自可独立选中，**且会标出「来源」**（只有真出现
  重名时才标，组标题已经写着 host 时不重复噪音）。
- **不做的事**：不给已有条目自动补标签（那是迁移，不是本轮目标）。存量条目
  照样能用，只是不与「同 id 不同 provider」并存。

## 6. 测试钉子

`internal/config/registry_key_test.go`：键拼装 / 分隔符校验 / 并存不冲突 /
存量文件零改动加载 / 角色绑定两种形式 / Update 按组合键定位 / 重复组合键拒绝
加载 / 解析不跨 provider 猜 / 文件版本不变。
`internal/agent/session_model_key_test.go`：会话级选择接受组合键与纯 id、
优先级不变、存量会话模型照旧解析。
`frontend/tests/models.test.mjs`：`modelKey`/`modelIdOfKey`、同名并存投影、
添加重复判定按组合键、编辑可改/可清标签。