# 会话树 + 文件快照回滚 + 右侧轮次面板改造（设计稿）

> 状态：**草案，待用户拍板**（2026-12）。本文只出方案，不含生产代码改动。
> 参考：pi 的 session tree（`id`/`parentId` + `leafId`，回滚 = 只移 leaf 指针、一条不删）
> 与 pi 官方示例扩展的 `git stash create` 文件回滚（内存 Map、不持久化）。
> 现行实现的事实基础：`internal/store/messages.go`（线性 seq + 影子集合回放）、
> `internal/agent/rewind.go`（撤回 = 真删行）、`internal/protocol/chat.go`（`chat.rewind`）、
> `frontend/src/components/panels/{TurnPanel,turns,outline}.ts(x)`（右侧轮次面板）。

---

## 0. 一页纸结论

| 问题 | 结论 |
|---|---|
| 历史模型 | `messages` 从线性链升级为**树**：加 `parent_seq`；`sessions.leaf_seq` 是当前叶指针；**回滚 = 只移 leaf，一条不删** |
| 回放 | 回放 = `leaf` 回溯到 root 的路径，再套用**现有的 `surfaceRows`**（影子集合 + 检查点锚点插入一字不改）——单链会话逐字节等价，存量零风险 |
| 协议 | 保留 `chat.rewind` 名字、**语义改为移 leaf**；`removed` 数值口径保持「离开路径的条数」（= 旧的删除条数），老客户端行为不变；新增 `chat.setLeaf` / `chat.tree` / `chat.leafChanged` |
| 文件回滚 | `file_snapshots` 元数据入 SQLite，**blob 落磁盘**（`<sessions>/fileblobs/<sha256>`，与 attachments 同一先例）；写工具执行**前**抓 before 快照；回滚 fail-closed（有冲突整批拒绝、leaf 不动） |
| **回滚作用域（2026-12 拍板）** | 父会话回滚 = 父对话 leaf 移动（旧分支保留）+ **父子全部文件改动一起还原**（子会话的快照按 `owner_session_id`/`owner_seq` 摊平到父会话的轮次上）+ **子会话对话历史不动** |
| 覆盖边界 | 只有 `edit` / `write_file` 能抓（**`bash` 改文件不可回滚**，明确写在 UI 与文档里；回滚前二次确认列明无法还原的处数） |
| 右栏 | 轮次面板改树形：缩进 + 连接线 + 当前 leaf 高亮 + 分支标记 + 废弃分支灰化不隐藏；**单击 = 跳转（当前路径）/ 切分支并跳转（废弃分支）**，行尾按钮 = 从此分支 |
| 分阶段 | 4 批（+1 个可选小步）：①数据模型+迁移+路径解析（含 `readRows` 列对齐）②leaf/分支运行时+协议 ③文件快照 ④前端树面板 |

**已拍板的四条**（详见 §G.0）：`chat.rewind` 保留名改语义、`SessionStats` 仍折叠整段日志、
`bash` 不可回滚用 UI ⚠️ + 二次确认、**子会话文件改动纳入父会话回滚**（原推荐错误，已改）。

**设计里最容易做错的七处**（读这份文档时先看这七条，它们决定了方案是否成立）：

1. **压缩必须重接父指针**（§A.2/§A.3）——不是「记个影子集」就完了，被压段之后那条存活行的
   `parent_seq` 要改指向检查点；漏了它，树视图里摘要是条孤立分支；
2. **`leaf_seq = 0` 是「空路径」不是「未知」**（§A.1②）——把两者混起来会让「撤回第一条消息」
   在下次 Load 时整个失效；
3. **`removed` 口径必须两端都按 surface 数**（§B.1）——老客户端的本地截断全靠它；
4. **`setLeaf` 的 context 不能沿用落库测量**（§E.4）——那份数字属于切分支**之前**的路径；
5. **`List` 的可见性判据不能用 `leaf_len`**（§E.5）——撤回第一条消息后 `leaf_len=0`，
   而那条会话的废弃分支还在库里；按 0 过滤会让它从侧栏消失（数据在、入口没了）。
6. **压缩后 `leaf_seq` 不动**（§A.3 第 3 条）——压缩换掉的是中段，尾部保留段仍在路径上；
   把 leaf 改到检查点会让模型看不到尾部，而 `after` 占用却按完整路径算；
7. **子会话的写必须记「父会话的 seq」**（§C.2.1）——子会话的 seq 是它自己的空间，与父会话
   不可比；回滚切段只能用 `owner_seq`。而且**续跑要重算 owner_seq**（同一个子会话在不同轮次
   被派发时归属不同，边界①见 §C.5.1）。

---

## A. 数据模型

### A.1 DDL（全部幂等，追加在 `store.Open` 现有的迁移循环里）

```sql
-- ① 消息级父指针。命名为 parent_seq（不是 parent_id）：
--    它与主键 (session_id, seq) 的 seq 同域，而 sessions.parent_id 是**会话级**父子
--    （子 Agent = 独立会话，AGENTS.md §2.3）——两个 parent 必须一眼分得开。
--    0 = root（首条消息 / 被压缩到开头的检查点）。
ALTER TABLE messages ADD COLUMN parent_seq INTEGER NOT NULL DEFAULT 0;

-- ② 叶指针。**0 = 合法状态「空路径」**（用户回到了第一条消息之前 / 新会话还没发消息），
--    不是「未知」。为什么不能拿 0 兼作「未知」：撤回第一条消息后 leaf 就该是 0，
--    若 0 又表示「未知 → 回落 MAX(seq)」，那次撤回会在下一次 Load 时**整个撤销**
--    （历史全回来）。未知态用 PRAGMA user_version 的迁移标记区分（见 A.2 末尾）。
ALTER TABLE sessions ADD COLUMN leaf_seq  INTEGER NOT NULL DEFAULT 0;
-- ②' 子会话的快照归属（2026-12 拍板：父会话回滚要连子会话的文件改动一起还原）。
--     owner_id  = 回滚的发起方（沿 parent_id 上溯到顶的顶层会话 id）；
--     owner_seq = 上一次派发它时，父会话那条 dispatch 消息的 seq（**每次派发重写**）。
--     空串 / 0 = 老会话或顶层会话（调用方回落「自己就是 owner」）。
--     注意这只是**默认值/兜底**：写入点的权威 owner 由 ctx 注入（§C.2.1）。
ALTER TABLE sessions ADD COLUMN child_owner_id  TEXT    NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN child_owner_seq INTEGER NOT NULL DEFAULT 0;

-- ③ 当前路径的节点数（= 侧栏「消息数」的新口径）。为什么落列而不每次算：
--    List() 要给每条会话一个条数（侧栏每次刷新都跑），而分支之后
--    「全表数 − 影子数」不再等于路径长度（废弃分支的行也在表里）。
--    增量维护点只有三处（AppendMsg +1、AppendCheckpoint −shadowed+1、SetLeaf 重算）。
ALTER TABLE sessions ADD COLUMN leaf_len  INTEGER NOT NULL DEFAULT 0;

-- ④ 文件快照：一行 = 一次写操作**之前**的文件状态（+ 之后的期望状态，冲突检测用）。
--    `owner_*` 两列是回滚切段的唯一依据（见 §C.2.1），单独建复合索引。
CREATE TABLE IF NOT EXISTS file_snapshots (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  -- session_id = **真正执行这次写**的会话（父会话自己，或某个子会话）。
  -- 它的 seq 是**那个会话自己的**消息序号空间——子会话的 seq 与父会话不同域。
  session_id   TEXT    NOT NULL,
  -- 锚点：这次写所属的**消息 seq**（携带 tool_call 的 assistant 行——它在本轮
  -- 工具执行前就已落库，是唯一稳定的锚点；tool 结果行的 seq 在执行后才分配）。
  -- 子会话里写时这里是**子会话自己的** seq（只用于追溯/对账，不参与回滚切段）。
  seq          INTEGER NOT NULL,
  -- 归属（2026-12 用户拍板：父会话回滚要连子会话的改动一起还原）。
  -- owner_session_id = **回滚的发起方**（沿 parent_id 上溯到顶的顶层会话 id）；
  -- owner_seq        = 这次写属于 owner 的**哪一轮之后**（owner 的 seq 空间）。
  --   父会话自己的写：owner_session_id = session_id、owner_seq = seq；
  --   子会话的写：   owner_session_id = 顶层父会话、owner_seq = 那次派发的 tool_call
  --                 所在的**父会话 assistant 消息 seq**（归属链见 §C.2.1）。
  -- 为什么必须落列、不能回滚时现算：子会话可以**跨多轮续跑**（同一子会话被派发多次），
  -- 只有写的那一刻知道「这次是父会话哪一轮派下来的」；事后从 dispatch_id 反推会把
  -- 所有续跑都算成第一次派发的时刻（边界①，见 C.5）。
  owner_session_id TEXT NOT NULL DEFAULT '',
  owner_seq        INTEGER NOT NULL DEFAULT 0,
  tool_call_id TEXT    NOT NULL DEFAULT '',
  -- 工作区相对路径（统一 '/' 分隔，根 = 会话 workDir）。相对路径是**可移植**的：
  -- 项目移动/换机器后快照仍然对得上；绝对路径会把快照钉死在旧盘符上。
  path         TEXT    NOT NULL,
  -- '' = 写之前文件不存在（新建）→ 回滚 = 删掉这个文件。
  before_hash  TEXT    NOT NULL DEFAULT '',
  -- '' = 写之后文件不存在（删除类写，当前工具没有，留给将来）。
  after_hash   TEXT    NOT NULL DEFAULT '',
  size         INTEGER NOT NULL DEFAULT 0,
  -- 1 = 没抓内容（超大 / 二进制）：只记「这里发生过写」，回滚时如实报 skipped，
  -- 绝不假装还原成功。
  skipped      INTEGER NOT NULL DEFAULT 0,
  skip_reason  TEXT    NOT NULL DEFAULT '',
  created_at   TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_snapshots_path ON file_snapshots(session_id, path, seq);
CREATE INDEX IF NOT EXISTS idx_snapshots_blob ON file_snapshots(before_hash);
-- 回滚收集的主查询：按 owner 取「这一轮之后」的全部快照（父 + 子会话一次查出来）。
CREATE INDEX IF NOT EXISTS idx_snapshots_owner ON file_snapshots(owner_session_id, owner_seq);
CREATE INDEX IF NOT EXISTS idx_messages_parent ON messages(session_id, parent_seq);
CREATE INDEX IF NOT EXISTS idx_sessions_parent ON sessions(parent_id);  -- 已有（AGENTS §2.3），此处仅标注
```

**为什么快照要记 `owner_session_id` / `owner_seq`（而不是只记 `session_id` / `seq`）**：
用户 2026-12 拍板「父会话回滚要把子会话的改动一起还原」。子会话的 `seq` 是它自己的序号空间，
与父会话的 seq 不可比——要判断「这次写发生在父会话第几轮之后」，只能用**父会话的 seq**。
两个字段的分工：`session_id`/`seq` = 谁写的、在它自己的哪条消息下（追溯/对账用）；
`owner_session_id`/`owner_seq` = 这笔账记在谁的头上、哪一轮之后（**回滚切段用**）。
把两者合并成一个字段的话，「子会话内部定位」与「父会话回滚定位」必有一个做不到。

**为什么 `after_hash` 不在需求给的四列里**：冲突检测要回答「磁盘现在这份，还是我上次写完的那份吗」。
没有 `after_hash` 就只能拿「下一次写的 before」去比——而回滚目标之后往往没有下一次写（那正是要回滚的原因）。
一个 sha256 列（64 字节）换掉一整类「静默覆盖用户手改」的事故，值得。

**为什么不给 `messages` 加 `id` 列（pi 的做法）**：本仓库的 `seq` 已经是全链路身份——前端撤回锚点、
检查点影子集合、`chat.history` 分页 anchor、`before_seq`、`files.changed` 的定位全用它。
再引入一套 id 空间意味着上述所有引用要双写/迁移，收益为零。**树的节点身份 = `seq`。**

### A.2 存量迁移（`store.Open` 里跑一次，幂等）

**做法：整个迁移在 Go 侧一次遍历完成，不用「按 seq 链回填」的 SQL。** 理由见下。

```go
// 迁移（一个事务，逐会话）：按 surface 顺序重写 parent 链 + 定 leaf/len。
for _, id := range allSessionIDs(tx) {
    rows, err := s.readRowsTx(tx, id)   // 全表、seq 升序（复用现有函数，不新写一份 SELECT）
    if err != nil { return err }
    surface := surfaceRows(rows)        // ← 与回放**同一份实现**（含检查点锚点插入）
    prev := 0
    for _, r := range surface {
        tx.Exec(`UPDATE messages SET parent_seq = ? WHERE session_id = ? AND seq = ?`, prev, id, r.seq)
        prev = r.seq
    }
    // surface 为空 → leaf=0、leaf_len=0（空会话：0 就是合法的空路径）
    tx.Exec(`UPDATE sessions SET leaf_seq = ?, leaf_len = ? WHERE id = ?`, prev, len(surface), id)
}
```

**为什么不先写一句「按 seq 链回填」的 SQL**：那只是把「普通行接前一条 seq」做一遍，而紧接着的
Go 步骤会**把所有行（含检查点）重写一遍**——两套判定并存只会让「哪一套是权威」变得含糊。
本仓库已经为「两条路径各算一遍」吃过多次亏（`readRows`/`readRowsTx` 的列漂移就是活证据）。
**一份实现（`surfaceRows`）从头到尾**，代价是一次性的全表读（53k 行约 200ms，见 `stats.go` 的实测）。

**为什么必须按 surface 顺序重写**（这是整个设计里最容易做错的一处）：

```
库内：A1..A5（被 CK 影子）、CK（seq=11，影子 A1..A5）、A6..A10
按 seq 链：A6.parent = A5  ← 错！从 leaf=A10 回溯会走到 A1..A5 那堆原文，检查点根本不在路径上
按 surface：CK 落在被影子段的位置（首位）→ CK.parent = 0，A6.parent = CK
           回溯 A10 → … → A6 → CK → 0：检查点在路径上，A1..A5 被它替换掉 ✅
```

**同一个道理必须在写入侧也守住**（`AppendCheckpoint` 的新职责，见 A.3 的三件事）：压缩不只是
「记一个影子集」，它**改变了历史的形状**——被压段之后那条存活行的父指针要改指向检查点。
漏了这一步，压缩后的历史在树模型里就断成两截（前端看到的树会把摘要显示成一条孤立分支）。

`leaf_len` 就用同一个 `len(surface)` 写——**不要另起一套 `surfaceCounts()` 的 SQL 口径**
（那是「全表数 − 影子并集」，在迁移这一刻与 `len(surface)` 数值相同，但两套口径迟早分叉；
既然 surface 已经算出来了，就用它）。

**幂等守卫用 `PRAGMA user_version`，不要靠 `WHERE parent_seq = 0` 兜**：

```go
const treeSchemaVersion = 1
var v int
_ = s.db.QueryRow(`PRAGMA user_version`).Scan(&v)
if v < treeSchemaVersion {
    // 上面那段迁移全跑一遍（事务内），成功提交后：PRAGMA user_version = 1
}
```

为什么必须这样：每条会话的**第一条消息**的 `parent_seq` 天生就是 0（root），拿
`WHERE parent_seq = 0` 当幂等守卫会让那一条在**每次 Open 都重算一遍**；更糟的是它把
「合法的 root」与「还没迁移」混为一谈——一旦以后有别的迁移要复用这个判据，就会错得很隐蔽。

`Store` 因此多一个字段 `treeMigrated bool`（Open 时按 `user_version` 置位），
`pathRows` 的第三个参数就是它——**未迁移的库回落旧语义**，读路径与今天逐字节一致。

**迁移失败时的行为（fail-closed）**：整个迁移在一个事务里，失败则**回滚并让 `Open` 报错**，
不要「跳过迁移继续跑」——那样库里一半 parent 是 0、一半是链，`pathRows` 会切出
半截历史（比打不开更坏：用户会以为历史丢了）。启动失败是可见的、可修的；
静默的半截历史是不可见的。

**迁移验收钉子**：`TestMigrationChainBackfill` —— 造一个含「前缀压缩 + 中间段压缩 + 撤回过的行」的
老库（先跑旧 schema 写入，再升级），断言升级后 `pathRows(leaf)` 与旧 `surfaceRows(全表)` **逐字节一致**
（seq 序列、role、content、checkpoint 下标全部相同）。

### A.3 路径解析：一行代码的模型升级

核心洞察：**树模型不需要新的回放算法，只需要把「输入行集」从全表换成「从 leaf 可达的行」。**

```go
// pathRows 返回当前 leaf 路径上的历史（surface 顺序）。
// migrated 来自 store 字段（Open 时 PRAGMA user_version 的判定结果）——**不靠数据指纹猜**：
// 「所有 parent_seq 都是 0」既可能是没迁移，也可能是一条刚建的会话（它的第一条消息
// 本来就该是 root），猜错的代价是「老库打不开」或「新会话历史消失」。
func pathRows(rows []rowData, leaf int, migrated bool) []rowData {
    if len(rows) == 0 {
        return nil
    }
    if !migrated {
        return surfaceRows(rows) // 未迁移的库：旧语义（全表 surface）
    }
    if leaf <= 0 {
        // 0 = 空路径（用户回到了第一条消息之前）。**不是**「回落全表」——
        // 那样等于把刚撤回的历史又装回来（见 A.1 ② 的注释）。
        return nil
    }
    bySeq := make(map[int]rowData, len(rows))
    for _, r := range rows { bySeq[r.seq] = r }

    // ① 沿 parent 链回溯（防御：环/断链时钳住，不抛异常——历史来自 SQLite，
    //    畸形数据是既成事实，加载路径抛异常会让老会话直接打不开）
    reach := make(map[int]struct{}, 64)
    for cur, guard := leaf, 0; cur > 0 && guard <= len(rows); guard++ {
        r, ok := bySeq[cur]
        if !ok { break }
        if _, dup := reach[cur]; dup { break } // 环
        reach[cur] = struct{}{}
        cur = r.parentSeq
    }

    // ② 剩下的交给现有实现，一字不改：跳过被影子行 + 检查点落回锚点位置。
    //
    //    为什么**不需要**再判「检查点属于哪条分支」：因为写入侧（AppendCheckpoint）
    //    已经把被压段之后那条存活行的 parent 改指向检查点——于是检查点**在不在
    //    路径上，由 parent 链自己回答**。
    //
    //    最强的验证用例：主链 A1..A10（leaf=A10），用户切到 A3 分叉出 B4、B5 并压缩，
    //    得到 CK_B（影子 A1..A3，B4.parent=CK_B）。此时切回 A10：
    //      reach(A10) = A1..A10，CK_B 不在其中 → 不进 live →
    //      surfaceRows 的 shadowSet 只看 live 里的检查点 → A1..A3 **照常可见**。
    //    也就是说「别的分支的压缩不会污染本分支的历史」，而且是**结构上**保证的，
    //    不靠任何额外的归属判定。
    //
    //    这条纪律的关键是：**别在 pathRows 里再引入一套「分支归属」判定**。
    //    两套判定（parent 链 + 影子集包含关系）迟早分叉，而分叉的表现是
    //    「树面板显示的分支」与「时间线回放的历史」不一致——最难查的一类 bug。
    live := make([]rowData, 0, len(reach))
    for _, r := range rows {                  // 按 seq 升序遍历（rows 本来就是）
        if _, ok := reach[r.seq]; ok {
            live = append(live, r)
        }
    }
    // live **必须保持 seq 升序**：surfaceRows 依赖 others 的既有顺序（它只给检查点
    // 排序），从 map 直接铺开会把历史顺序打乱——那是静默的历史错序。
    return surfaceRows(live)
}
```

**这套规则的自洽性检查（每一条都要有单测）**：

| 场景（parent 链已按 A.2② 的 surface 顺序建好） | `reach` | 结果 | 说明 |
|---|---|---|---|
| 单链 A1..A10，前缀压缩成 CK（影子 A1..A5，A6.parent=CK），leaf=A10 | A1..A10 | `[CK, A6..A10]`（= 现状，逐字节） | 检查点在链上，影子集生效 |
| leaf 回到 A3（压缩**之前**的分支） | A1,A2,A3 | `[A1,A2,A3]` —— 原文可见 | CK 不在链上（A6 改挂 CK 之后，A3 的后继是 A4…），**这是对的** |
| leaf = CK 自身 | {CK} ∪ 前缀 | `[CK]` | 压缩后立刻停在这里 |
| 二次压缩 CK2（影子含 CK1 与 A6..A10，A11.parent=CK2），leaf=CK2 | {CK2, CK1, …} | `[CK2]` | 链式重接的必然结果 |
| leaf 被移回 A10（CK2 时代之前） | A1..A10 | `[CK1, A6..A10]`（CK2 不生效） | CK2 不在 A10 的链上 |
| 回到第一条消息之前（leaf=0）再发新消息 → 新 root R1 | {R1} | `[R1]` | 旧历史仍在库里，切回去就回来 |
| 迁移前的库（`migrated=false`） | — | `surfaceRows(全表)` | 与今天逐字节一致 |
| 主链 A1..A10，从 A3 分叉出 B4/B5 并压缩成 CK_B（影子 A1..A3），再切回 A10 | A1..A10 | `[A1..A10]` | **CK_B 不在链上 → 别的分支的压缩不影响本分支**（结构保证，见 A.3 注释） |

**新增写入侧职责（`AppendCheckpoint` 三件事，缺一不可）**：

1. `skip/count` 必须解析到**当前 leaf 路径的 surface**（`readRowsTx + pathRows(leaf)`）。
   区间越界照旧报错——分支化之后「落盘落后于内存」的形态多了一种（内存路径与库里 leaf 不一致），
   拒绝写入比写一个跨分支的无效影子集好。
2. **重接父指针**（同一事务）：检查点的 `parent_seq` = 被影子段之前的最后一条存活行；
   被影子段之后那条存活行（`surface[skip+count]`）的 `parent_seq` 改指向**检查点自己**。
   没有后继行时（压缩发生在历史末尾）不用改——检查点自己就是新 leaf。
3. **`leaf_seq` 不动**（除非 leaf 原本落在被压段里）、`leaf_len = leaf_len − count + 1`。

   正确写法：`leaf_seq = (leaf 落在被影子段里) ? 检查点 seq : 原 leaf_seq`。
   为什么通常不动：压缩区间 `[skip, skip+count)` 在**尾部保留段之前**，leaf 在保留段里
   天然不受影响。leaf 落在被压段里只发生在「手动压缩 + 用户切到了中间某个节点」——
   那种情况下那条节点被收进摘要，叶自然落到检查点上。

   ⚠️ 写成「无条件 `leaf_seq = 检查点 seq`」的后果：压缩后模型看不到保留的尾部
   （历史被截断成只有摘要），而 `chat.compact` 返回的 `after` 占用却按完整路径算
   ——两个数字互相矛盾，且下一次请求的上下文会莫名其妙地少一大截。

漏了第 2 步的后果是静默的：压缩后时间线照常（`surfaceRows` 仍按影子集回放），但**树视图里
摘要节点会变成一条孤立分支**，用户点它就切到一条只有摘要的历史上。

### A.4 写路径（`AppendMsg` 的语义变化）

```
seq      = MAX(seq) + 1     -- 不变：全局单调，检查点锚点与前端排序都靠它
parent   = sessions.leaf_seq -- 新：沿当前 leaf 前进（0 = 新 root，见下）
leaf_seq = 新 seq            -- 新：追加即前进
leaf_len = leaf_len + 1      -- 新
```

- **追加 = 沿当前叶前进**。leaf 指向旧节点时追加，自然形成新分支（pi 的同款语义）。
- **leaf = 0 时新消息是 root**：用户在第一条消息之前「回到这里」再发消息，就是开了一条
  与旧历史**并列**的新根分支（旧历史仍在，只是不在当前路径上）。这是树模型的正确语义，
  不是异常——`pathRows` 从这条新 root 回溯只会看到它自己与它的后代。
  （**不要**写成 `COALESCE(leaf, MAX(seq))`：那会把新消息接回旧链尾，等于撤回失效。）
- `AppendMsg` 自己维护 leaf（同一事务内），agent 层不需要传 parent——
  `Persistence` 接口签名**不变**（`AppendMsg(string, llm.Message) (int64, error)`）。
- `AppendCheckpoint` 同样在事务里更新 `leaf_len = leaf_len − shadowed + 1`，并重接父指针
  （见 A.3 的三件事）；**`leaf_seq` 只在它原本落在被压段内时才改到检查点**（尾部保留段仍在路径上，
  见 A.3 第 3 条的说明）。
- `SetLeaf`（新方法）在事务里 `UPDATE sessions SET leaf_seq = ?, leaf_len = ?`（len 由 pathRows 算）。
- **`leaf_len` 有三处维护点，必须同源**：`AppendMsg` 自增、`AppendCheckpoint` 的 `−shadowed+1`、
  `SetLeaf` 重算。前两处是增量（与 `surfaceRows` 的语义等价，前提是「压缩只发生在路径末尾之外的
  前缀/中段」——这是压缩的既有约束），第三处是全量重算（切分支后增量算不出来）。
  单测：`TestLeafLenMatchesPathLen`（随机跑一串 append/compact/setLeaf，每次都断言
  `leaf_len == len(pathRows(...))`）——**这是防漂移的总闸**，别的测试都替代不了它。

### A.5 两条读路径的纪律（本仓库的老坑，必须先钉住）

`store/messages.go` 里 **`readRows` 与 `readRowsTx` 的 SELECT 列表必须逐字一致**（现有注释已经写明：
两条路径各写一遍就会漂移，表现是「写进去的与读出来的对不上」）。加 `parent_seq` 时：

- 两处 SELECT 都加 `parent_seq`，两处 `rows.Scan` 都加 `&r.parentSeq`；
- `rowData` 加 `parentSeq int` 字段，`rowData.msg.ParentSeq` 一并写回（与 `Seq` 同款：
  `chat.history` 直接回放 `llm.Message`，不回填的话前端拿不到父指针）；
- `llm.Message.ParentSeq` 必须在 `sanitizeMessagesForWire` 里**清掉**（与 `Seq`/`FirstTokenMs`/
  `DurationMs`/`Model`/`UsageTokens`/`Attachments` 同一批）——它是会话簿记，模型不需要，
  严格网关多一个未知字段就 400 拒收整轮（AGENTS.md §5 坑 13）。

**顺带发现（既有漂移）**：`readRows` 的 SELECT 比 `readRowsTx` 多两列
（`images` / `attachments`）——两处都列 19 个字段名，但 `readRows` 比 `readRowsTx` 多这两个，
而 `readRows` 的注释写着「必须逐字一致」。今天不出问题是因为 `readRowsTx` 的两个调用方（`AppendCheckpoint` / `Rewind`）
只读 seq / checkpoint / shadowed_seqs；但**加 `parent_seq` 时必须两处都加**，否则
「落库时算出来的路径」与「回放时读出来的路径」会分叉——正是那条注释警告的事。
建议顺手对齐（补齐两列），属于低风险清理，可以放在批次 1 里一起做。

### A.6 索引与性能

| 查询 | 走什么 | 量级 |
|---|---|---|
| 路径构造（`Load`/`Latest`/`chat.history`） | **已有**：`readRows` 一次全表读 + Go 侧 map 回溯 | 与今天完全一样（多一次 map 构建，O(n)，纳秒级） |
| 按父查子（`chat.tree` 的分支计数） | `idx_messages_parent(session_id, parent_seq)` | 索引扫描 |
| 某文件的快照序列（回滚 plan） | `idx_snapshots_path(session_id, path, seq)` | 点查 + 区间 |
| blob 引用计数（GC） | `idx_snapshots_blob(before_hash)` | 每 hash 一次点查 |
| 侧栏条数（`List` / `ChildrenOf`） | `sessions.leaf_len` 列直读（加进 SELECT 列表） | 零额外查询（**比今天还快**：今天 `List` 之后还要跑一次 `surfaceCounts` 的全表聚合 + 检查点 JSON 解析） |

**`chat.tree` 不能走 `readRows`**：那是全列读（含 KB 级正文），53k 行实测 ~195ms
（同一实测见 `store.sessions.go` 的 `surfaceCounts` 注释——那条查询就是为躲开它才下推 SQL 的）。
树视图只需要骨架，走一条**轻量投影查询**（与 `stats.go` 的覆盖索引同一套理由）：

```sql
SELECT seq, parent_seq, role, checkpoint, notice, length(content),
       substr(content, 1, 120)          -- 摘要只取前 120 字符（中文约 40 字）
  FROM messages WHERE session_id = ? ORDER BY seq
```

再在 Go 侧做三件事：① 沿 `parent_seq` 从 leaf 回溯出 `reach` 集合（与 `pathRows` 同一段
回溯逻辑，抽成 `reachSet(rows, leaf) map[int]struct{}` 给两处共用——**不要写第二份**）；
② 按它标 `on_path`；③ 按「真实 user 消息」编号 `turn`（与 `SessionStats.turns`、
现有 `turnGroups` 同口径：`notice` 与 `checkpoint` 不算）。

### A.7 树的健壮性规则（三条，都是「畸形数据不炸」的既有纪律）

1. **孤儿节点**（`parent_seq` 指向一个不存在的 seq——库外改写/迁移中断）：挂到虚拟 root 下并
   置 `orphan: true`，面板上显示在「孤立记录」分组里。**不丢弃**（丢节点等于藏数据）、
   **不抛异常**（抛了整条会话的树就打不开）。
2. **环**（A.parent=B、B.parent=A）：按 seq 升序做一次 DFS，已访问的节点不再下探；
   环上的节点全部标 `orphan`。回溯路径（`pathRows`）那侧同样钳住（`guard <= len(rows)`）。
3. **截断**：`max_nodes` 生效时按 **seq 从大到小**取最近 N 个，再补齐它们到 root 的祖先
   （否则拿到的是一堆断了父节点的碎片）。`truncated=true` 时前端在面板顶部显示
   「更早的 N 个节点未加载」并给「加载全部」按钮（显式动作，不自动）。

---

## B. 协议变更

### B.1 `chat.rewind`：保留名字，语义改为「移 leaf」（推荐）

**推荐方案：保留 `chat.rewind`，内部等价于 `setLeaf(parent(seq))`。** 理由：

1. 语义上「撤回这条及其之后」在新模型里的**正确实现就是**「把 leaf 移到这条的父节点」——
   用户的意图没变（我要从这条之前重来），变的是「之后的东西还在不在」；
2. 老客户端的**可见行为完全一致**：它本地 `planRewind` 截断时间线 → 新路径恰好是截断后的前缀；
   它随后拉 `chat.history` → 得到的也是这条路径。**老客户端不需要改一行代码**；
3. 不新增 `chat.branch`：`branch` 与 `rewind` 的差别只有「要不要把原文回填输入框」，那是 UI 行为，
   不该在协议里开两个语义几乎相同的方法（协议面越窄越好，`chat.mergeRequest` 的遗留方法已经是教训）。

**兼容性关键：`removed` 的数值口径必须保持不变。**

```
removed = |path(leaf_old)| − |path(leaf_new)|      -- 两端都按 surface 口径数（含检查点节点）
```

两边都是「当前历史的条数」，所以与旧语义（`len(surface) − idx`，即「从锚点起真删的条数」）
**逐例相同**。举一个带检查点的例子（这不是巧合，是两条口径的定义相同）：

```
库内行：A1..A5（被 CK 影子）A6..A10，CK 的 seq 最大，leaf=A10
旧：surface = [CK, A6, A7, A8, A9, A10]（6 条），锚点 A8 在 idx=3 → removed = 6−3 = 3
新：新 leaf = parent(A8) = A7 → path(A7) 的可达集 A1..A7，CK 影子集 ⊆ 可达集 → 生效
    path(A7) = [CK, A6, A7]（3 条）→ removed = 6−3 = 3   ✅ 一致
```

**幂等与越界**：`seq` 不在**当前 leaf 路径**上（重复撤回 / 两个客户端同时撤回 / 锚点是废弃分支
的节点）→ 返回 `removed=0`、`leaf_seq` 原样，不报错（与现状同款：撤回是正常交互，重复不该变成错误码）。

**配对不变量（`AnalyzeToolPairing`）照旧守住，只是判定的对象换成路径**：现状
`Session.Rewind` 用 `AnalyzeToolPairing(s.history).BalancedBefore(idx)` 拒绝「切在
assistant 的 tool_calls 与它的 tool 结果之间」。分支化后 `s.history` 已经是 leaf 路径，
这段判定**一行不用改**；但 `setLeaf` 多了一个入口，所以：

- `setLeaf(seq)` 的判定是「**以 seq 结尾的那条路径**是配平的」= `AnalyzeToolPairing(path).BalancedBefore(len(path))`
  （游标归零 = 路径上每个 tool_call 都有配对结果）。切到 assistant 的 tool_calls 与它的
  tool 结果**之间**的节点会被拒绝——否则下一轮请求直接被端点 400；
- 拒绝时的文案要指路：「这条消息后面还有未完成的工具调用，请选它之后的节点」。

**为什么不能只靠「前端只允许点 user 节点」**：协议是公开的，CLI/安卓/脚本都能直接发
`chat.setLeaf`——服务端必须自己守（与「白名单是防御层，不指望模型自觉」同一条纪律）。

**`chat.rewound` 事件的载荷形状完全不变**（`session_id`/`seq`/`removed`/`context`/`stats`），
老客户端的 `reduceRewound` 继续按 `planRewind` 截断，结果正确。

**语义变更的诚实告知**：新增 `ChatRewindResult.branched: true`（可选键，老客户端忽略）+
`chat.leafChanged` 事件。文档与 UI 文案要把「撤回」改成「回到这里」的口径说明（见 §D.8）。

### B.2 新增方法

```go
// internal/protocol/methods.go
MethodChatSetLeaf = "chat.setLeaf"   // 切叶（无副作用的历史指针移动）
MethodChatTree    = "chat.tree"      // 会话树骨架
// 事件
EventChatLeafChanged = "chat.leafChanged"
```

```go
// internal/protocol/chat.go

// ChatSetLeafParams 切到任意历史节点：**leaf 直接指向该节点**，继续对话即从它长出新分支。
//
// Seq == 0 = **空路径**（回到第一条消息之前）——这是 `chat.rewind` 撤回第一条消息时的
// 落点，用户必须能到达；因此「回到最新」不是一个特殊值，而是客户端传树里的最大 seq
// （`chat.tree` 的 nodes 末条）。**不给 0 兼两个语义**：那是本仓库反复吃亏的那类歧义。
//
// 与 `chat.rewind` 的分工：rewind 把 leaf 移到 `parent(anchor)`（锚点自己也离开路径，
// 因为它是用户要改写的那条消息）；setLeaf 把 leaf 移到 `seq` 本身（「从这里接着长」）。
type ChatSetLeafParams struct {
    SessionID string `json:"session_id"`
    Seq       int64  `json:"seq"`
    // Files = true 时先还原文件快照再切 leaf（fail-closed：还原失败/有冲突则不切）。
    // 缺省 false（老客户端不传 → 纯历史指针移动，零文件副作用）。
    // 作用域与 chat.rewind 的 files 完全一致：**父会话 + 全部子会话**在这一段里的文件改动
    // （按 owner_session_id/owner_seq 收集，§C.5），子会话对话历史不动。
    Files bool `json:"files,omitempty"`
}

type ChatSetLeafResult struct {
    SessionID string `json:"session_id,omitempty"`
    LeafSeq int64 `json:"leaf_seq"`
    // Removed = 离开路径的条数（与 chat.rewind 的 removed 同口径、同数值）。
    Removed int `json:"removed"`
    // 刻意**不**回 messages：切 leaf 之后客户端要重载的东西不止消息（树、统计、上下文、
    // 检查点下标、分页游标），在 setLeaf 的应答里再复制一份必然与 chat.history 分叉
    // （本仓库的老坑）。客户端统一走一次 chat.history —— 多一个往返，少一类 bug。
    // （对比：chat.rewind 现在也不回消息，老客户端本来就是重拉历史，行为一致。）
    Context  *ContextUsage `json:"context,omitempty"`
    Stats    *SessionStats `json:"stats,omitempty"`
    Files    *FileRestoreResult `json:"files,omitempty"`
}

// ChatTreeParams 树骨架。Include 缺省 "compact"。
type ChatTreeParams struct {
    SessionID string `json:"session_id"`
    Include   string `json:"include,omitempty"` // compact(默认) | full
    MaxNodes  int    `json:"max_nodes,omitempty"` // 缺省 2000；超出只回最近 N 个并置 truncated
}

type ChatTreeResult struct {
    LeafSeq   int64      `json:"leaf_seq"`
    Nodes     []TreeNode `json:"nodes"`     // 按 seq 升序（前端自己按 parent 建树）
    Total     int        `json:"total"`     // 全量节点数（截断时用于「还有 N 个」提示）
    Truncated bool       `json:"truncated,omitempty"`
}

type TreeNode struct {
    Seq       int64  `json:"seq"`
    ParentSeq int64  `json:"parent_seq"` // 0 = root
    Kind      string `json:"kind"`       // user | assistant | tool | checkpoint | notice
    Turn      int    `json:"turn,omitempty"`   // 轮次序号（user/notice 才有）
    Summary   string `json:"summary,omitempty"`// ≤120 字符的单行摘要（不含正文全文）
    Children  int    `json:"children"`         // 直接子节点数（>1 = 分支点）
    OnPath    bool   `json:"on_path"`          // 是否在当前 leaf 路径上（废弃分支 = false）
    Orphan    bool   `json:"orphan,omitempty"` // 父节点缺失 / 成环（畸形数据的兜底，见 §A.7）
    // Files = 该轮（owner_seq 落在这一轮的区间内）**可还原**的文件改动数，
    // **含子会话的改动**（按 owner_seq 归到这一轮，见 §C.2.1）——用户看到的是
    // 「这一轮回滚能还原几个文件」，不是「我自己改了几个」。
    Files int `json:"files,omitempty"`
    // ChildFiles = 其中来自子会话的改动数（前端可在徽标上显示「3 文件（含 1 个子 Agent）」）。
    // 与 Files 同源、是它的子集；为 0 时整键缺席。
    ChildFiles int `json:"child_files,omitempty"`
    External   int `json:"external,omitempty"` // 该轮无法快照的写（bash/MCP/自定义工具，**含子会话里的 bash**）
    Shadowed  int    `json:"shadowed,omitempty"` // checkpoint: 影子条数
}
```

```go
// 文件还原结果（chat.setLeaf / chat.rewind 共用）。
// 覆盖范围含**子会话**的改动（2026-12 用户拍板）：每项带 session_id 标明是谁改的。
type FileRestoreResult struct {
    Restored  []FileRestoreItem `json:"restored,omitempty"`
    Deleted   []FileDeleteItem  `json:"deleted,omitempty"`   // 回滚到「当时还不存在」= 删掉
    Conflicts []FileConflict    `json:"conflicts,omitempty"` // 非空 = 整批未执行（fail-closed）
    Skipped   []FileSkip        `json:"skipped,omitempty"`   // 大文件/二进制：如实报「没抓」
    // ChildSessions 是本次回滚涉及到的子会话（父会话自己不算）。
    // 前端据此显示「本次还原涉及 N 个子 Agent 的改动，它们的对话未回滚」。
    ChildSessions []string `json:"child_sessions,omitempty"`
}
type FileDeleteItem struct {
    Path      string `json:"path"`
    SessionID string `json:"session_id,omitempty"` // 谁新建的这个文件（多半是子会话）
}
type FileRestoreItem struct {
    Path      string `json:"path"`
    FromSeq   int64  `json:"from_seq"`   // 取的是哪个快照的 before（可追溯；**写它的那个会话**的 seq）
    FromOwnerSeq int64 `json:"from_owner_seq,omitempty"` // 那次写属于父会话的哪一轮之后
    // SessionID = 写这个文件的那个会话（父会话自己 = 顶层 id；否则是子会话 id）。
    // UI 据此在结果里分组显示「父会话改动」/「子 Agent 改动」，不混成一堆。
    SessionID string `json:"session_id,omitempty"`
    Bytes     int64  `json:"bytes"`
    Recreated bool   `json:"recreated,omitempty"` // 磁盘上本来没有，按快照写回（见 C.5）
}
type FileConflict struct {
    Path     string `json:"path"`
    Expected string `json:"expected_hash"`
    Actual   string `json:"actual_hash"`
    // SessionID = 期望状态是**哪个会话**写的（冲突提示要说清「这是子 Agent 改的文件」）。
    SessionID string `json:"session_id,omitempty"`
    Reason   string `json:"reason"` // "磁盘内容与快照期望不符（可能被手动修改或 bash 改过）"
}
type FileSkip struct {
    Path   string `json:"path"`
    Reason string `json:"reason"` // "文件过大（>1MB）未快照" / "二进制文件未快照"
}
```

### B.3 `chat.rewind` 的参数/结果扩展

```go
type ChatRewindParams struct {
    SessionID string `json:"session_id"`
    Seq       int64  `json:"seq"`
    // Files = true 时同时还原文件（fail-closed）。
    // **作用域（2026-12 拍板）**：父会话 + 它派出的**全部子会话**在这一段里的文件改动
    // 一起还原；子会话的**对话历史不动**。客户端不需要（也无法）按会话挑选——
    // 「父会话回到这一轮之前」在文件系统上就是一次跨会话的整体回滚。
    Files bool `json:"files,omitempty"`
}
type ChatRewindResult struct {
    Removed  int                `json:"removed"`            // 口径不变（见 B.1）
    LeafSeq  int64              `json:"leaf_seq,omitempty"` // 新增：新叶
    Branched bool               `json:"branched,omitempty"` // 新增：true = 历史保留为分支
    Files    *FileRestoreResult `json:"files,omitempty"`    // 含子会话：Restored/Deleted 每项带 session_id
}
```

**为什么不在协议里给「子会话」开关**（`include_children: true` 之类）：文件系统是父子共享的
单一状态，「只回父会话的文件」等于制造一个**已知不一致**的工作区（父会话历史回到第 42 轮，
但子 Agent 在第 44 轮改的东西还在）。用户 2026-12 拍板的就是这个判断——**不做半截回滚**。
要给用户「不想动某些文件」的控制权，走的是**冲突清单 + 用户手动处理**（以及将来的
「按路径排除」参数），不是「按会话排除」。

**⚠️ 一个必须处理的时序坑（前端乐观截断 vs `files:true` 冲突）**：

现状 `agent/ws/index.ts` 的 `rewind()` 会**先本地乐观截断时间线**再发请求（理由：等 WS 往返
用户会觉得"点了没反应"）。`files:true` 的冲突路径是「请求**成功**、但 leaf 没动」——
这时乐观截断已经发生，而**不会有** `chat.rewound` 事件来纠正它（后端确实什么都没改）。
结果：用户看到时间线被截断了，但历史其实没动；刷新一次全回来。

处理方式（三选一，推荐 ②）：

1. `files:true` 时**不做乐观截断**（等应答），冲突时前端什么都不用回滚——代价是文件回滚本身
   就要几百 ms，用户本来也在等；
2. **应答里带 `leaf_seq` 与 `removed=0` 时，前端把乐观截断回滚**（重拉一次 `chat.history`，
   与现有的失败对齐路径同一条：`catch` 里已经在调 `loadHistory`）；
3. 让后端在冲突时返回错误码而不是成功结果——**不推荐**：冲突是正常业务结果（要带清单给用户看），
   做成错误码会让「列出哪些文件冲突」变成错误载荷，与现有错误模型不一致。

**推荐 ②**：它复用现成的「失败 → 重放历史对齐」机制，不需要给 `rewind` 加"别乐观"的分支
（那条分支会让 `files:true` 与 `files:false` 的交互手感不同，用户会以为是卡了）。

**前端回声判重（2026-12 补充，「撤回后时间线清空又闪回来」的修复）**：成功路径上
`chat.leafChanged` 是本次 rewind 的**回声**（乐观截断已是正确终态），ws 层按
「同会话 + 5s 窗口内刚发起过 rewind」跳过全量重拉——`agent/ws/index.ts` 的
`recentRewinds` / `ownRewindEcho`。窗口过后的 leafChanged、setLeaf 的、别的客户端
发起的同会话叶变更**照常重拉**（判重只消费一次记录）。失败/冲突路径显式重拉
（纠错，不受判重影响）；同会话重拉若真的发生（如旧库残留），`rebuiltGen`
（`shared/blocks.ts` 的 UIState）标记重建代次，Thread 不重播入场动画并按高度差
补偿滚动位置——同会话重建不再表现为「时间线闪一遍」。

`chat.rewound` 事件载荷**不改**（老客户端零影响）；新增 `chat.leafChanged`：

```go
type ChatLeafChangedParams struct {
    SessionID string       `json:"session_id"`
    LeafSeq   int64        `json:"leaf_seq"`
    PrevLeafSeq int64      `json:"prev_leaf_seq,omitempty"` // 移动前的叶（客户端据此判断"是不是别人动的"）
    Removed   int          `json:"removed"`
    Context   *ContextUsage `json:"context,omitempty"`
    Stats     *SessionStats `json:"stats,omitempty"`
    Files     *FileRestoreResult `json:"files,omitempty"`
}
```

- `chat.rewind` **两个事件都发**（`chat.rewound` 给老客户端、`chat.leafChanged` 给新客户端与树面板）；
- `chat.setLeaf` 只发 `chat.leafChanged`（老客户端收到未知事件会忽略——`ws` 层按事件方法名映射，
  未知方法走 `events.ts` 的 `default: return null`，是既有行为，不是新保证）。

**`chat.setLeaf` + `files:true` 的切段口径**（与 rewind 不同，必须写清）：

- `rewind(anchor)`：leaf → `parent(anchor)`，还原 `owner_seq >= anchor`（锚点自己也被移出路径）；
- `setLeaf(seq)`：leaf → **seq 本身**（seq 留在路径上），还原 `owner_seq > seq`
  （**严格大于**：seq 这条消息自己的改动属于「目标状态」，不该被还原掉）。

差一个等号就会造成「切到第 42 轮，结果第 42 轮自己改的文件被还原了」——用户会看到
「我刚切过去，那个文件就不见了」。这条差异必须写成两个具名常量/函数（`rewindCut` /
`setLeafCut`），不能在两处各写一个 `>=`/`>`。

**`chat.setLeaf` 的错误语义（每条都要有测试）**：

| 情形 | 返回 |
|---|---|
| 会话 busy（一轮在跑 / dispatch 在跑） | `CodeInvalidRequest` + `ErrBusy` 人话（与 compact/rewind 同款） |
| `seq` 不属于该会话（坏号/别家会话的号） | `CodeInvalidParams`（**不静默当 0**——静默会把用户送到一个他没点的位置） |
| `seq` 是**别的分支**上的行 | **合法**（这正是分支切换）：leaf 直接指过去，路径随之切换 |
| `seq` 是**已被压缩影子掉的行** | **合法**，且语义很自然：那条行的链不经过检查点（`A6.parent = CK`，而 A1..A5 自成一条链），所以切过去之后 `pathRows` 里没有生效的检查点 → **原文自动"解压"可见**。这正是「回到压缩前的位置继续」想要的行为 |
| `seq` 落在某条 tool_call 与它的结果之间 | `CodeInvalidParams` + 「这条消息后面还有未完成的工具调用，请选它之后的节点」（`AnalyzeToolPairing` 的配对不变量） |
| `files:true` 且有冲突 | 结果成功返回、`files.conflicts` 非空、**`leaf_seq` 未变**（`removed=0`）；不是错误码（冲突是正常业务结果，要给用户清单）。子会话的冲突同样走这条 |
| `files:true` 且还原成功 | `leaf_seq` 变、`files.restored/deleted` 列出（每项带 `session_id`）、`files.child_sessions` 列出涉及的子会话 |
| 未启用持久化 | `CodeInvalidRequest` + 「未启用持久化」 |

**并发与多客户端**：`setLeaf` 与 `rewind` 一样是**广播型状态变更**（叶指针是会话的共享状态）——
`chat.leafChanged` 让所有客户端在同一时刻对齐。两个客户端同时切到不同分支时，
后到的那个赢（最后一次写入），先到的那个会收到对方的 `leafChanged` 并重载。
不做乐观锁/版本号：单用户场景下这是正常交互，加版本号会让"两边都以为自己赢了"变成错误码，
反而更难用。

### B.4 `chat.history` 如何表达树

**默认仍是「leaf 路径的线性列表」**（老客户端零影响），只做加法：

```go
type ChatHistoryResult struct {
    // …现有字段全部不变…
    LeafSeq int64 `json:"leaf_seq,omitempty"` // 新增：这次回放对应的叶
    // Messages[].parent_seq 由 llm.Message 承载（新增 json 键，omitempty）
    // 可选：客户端带 include_tree=true 时才回树骨架（默认不回，避免每次进会话都多一份载荷）
    Tree *ChatTreeResult `json:"tree,omitempty"`
}
```

`llm.Message`（= `chatmsg.Message`）加一个簿记字段，与 `Seq` 同一条纪律（**不进模型请求**，
在 `sanitizeMessagesForWire` 里清掉）：

```go
ParentSeq int64 `json:"parent_seq,omitempty"`
```

**分页语义（`tail` / `before_seq`）在路径上做，逻辑不变**：`TailHistory` / `HistoryBefore` 的
输入是 `Snapshot.Messages`（= 现在的 leaf 路径），它们做的是「窗口 + 回退到配对平衡切点 +
`shiftCheckpoints` 平移」——三件事全是下标运算，与树无关，一行不改。
唯一的新纪律：**分页窗口与树是两个独立载荷**——窗口只含路径上的一段，而树覆盖全部分支。
所以 `Tree` 只在**非分页**请求（`tail==0 && before_seq==0`）时返回；
分页请求一律不带树（否则每翻一页都重发一份树骨架，白烧带宽）。树面板自己单独调 `chat.tree`。

**payload 评估**（基于本仓库实测：大会话 `chat.history` 全量 1.4–1.7MB，代码注释里的事实）：

| 载荷 | 每条字节 | 500 轮会话（~2500 行） | 1000 轮（~5000 行） | 说明 |
|---|---|---|---|---|
| `chat.history` 全量 | ~500–700（含正文） | ~1.4MB | ~3MB | 现状，已有 30s 宽容超时 |
| `chat.tree` compact | ~200（seq/parent/kind/turn/摘要 40 字/children/on_path/files） | ~100KB | ~200KB | **默认**，折叠 assistant/tool 进轮节点 |
| `chat.tree` full | ~120（无摘要，含 assistant/tool 行） | ~300KB | ~600KB | 只在「展开看细节」时按需拉 |

结论：`chat.tree` compact 的载荷是 `chat.history` 的 1/10 量级，可以随进会话就拉；
`max_nodes` 缺省 2000 + `truncated` 兜住极端会话（>2000 节点只回最近 2000，面板顶部显示
「更早的 N 个节点未加载」——**不静默截断**）。

---

## C. 文件快照机制

### C.1 写工具清单（`internal/tools` 实测，`Mutates=true` 的全部）

| 工具 | 改工作区文件？ | 能否快照 | 说明 |
|---|---|---|---|
| `edit` | ✅ | **✅ 第一版覆盖** | 已 `os.ReadFile` 全文，before 内容**零额外 IO** 直接交给 sink |
| `write_file` | ✅ | **✅ 第一版覆盖** | 写前 `os.Stat` 已知是否覆盖；覆盖时读一次旧内容（≤1MB 才抓） |
| `bash` | ✅（任意） | ❌ **不可能** | shell 重定向/脚本/构建都会改文件，无法枚举 |
| `workspace_sync` | ✅（git commit） | ❌ | 只提交不改内容；回滚靠 git |
| `workspace_rollback` | ✅（git） | ❌ | 本身就是回滚通道 |
| `workspace_publish` | ✅（复制到主检出） | ⚠️ 第二版 | 覆盖面可能几十个文件，可抓但成本高 |
| `agent_write` | ❌（改的是 Agent 配置库） | ❌ | 不属于工作区 |
| 自定义工具（`custom.go`） | 未知 | ❌ | 起任意进程（`Mutates=true`，确认门照旧） |
| MCP 工具 | 未知 | ❌ | 远端行为不可观测（`Mutates=true`） |
| `share_file` | ❌（只写附件目录，不碰工作区） | ❌ | `Mutates=false`，不属于工作区改动 |
| `agent_dispatch` | ✅（**经子会话间接改**） | **✅ 经子会话覆盖** | 本身不改文件，但它派出去的子会话跑 `edit`/`write_file` 时会抓快照，并按 `owner_seq` 归到父会话这一轮（§C.2.1）；子会话里跑的 `bash` 则进父会话这一轮的 `external` 计数 |

**结论**：第一版只覆盖 `edit` / `write_file`（编程场景 95% 的文件改动走这两个）；
其余一律计入「**外部操作数**」（⚠️），在树面板与回滚确认文案里如实列出——
**宁可在 UI 上承认「这几个改动回滚不了」，也不假装回滚成功。**

**外部操作数怎么数**（口径要单点定义，否则面板与确认文案会对不上）：`runTools` 里
每个**成功执行**（`finishToolCall` 时 `isError == false` 且 `durationMs > 0`）且
`IsMutating(name) == true` 且**不是** `edit`/`write_file` 的调用 → 该轮 `external++`。
`edit`/`write_file` 但**快照被跳过**（大文件/二进制）也算 `external++`（它同样回滚不了）。
被拒绝 / 被 strict 拦 / 被取消的调用**不计**（它们没改动任何东西）——这与
「工具耗时的 0 = 没执行，不计入统计」是同一条口径纪律。

**子会话的外部操作也要计入父会话那一轮**（与文件快照同一条归属纪律，2026-12 拍板）：
`agent_dispatch` 那一次调用本身就是「父会话这一轮的操作」，而子会话在里面跑的 `bash` /
MCP 改动同样无法还原——所以 dispatch 结束回填结果时，把子会话本轮累计的 `external` 计数
**加进父会话那一轮的 `external`**。不这么做的话，树面板会显示「这一轮 0 处无法还原」，
而实际上子 Agent 用 `bash` 改了一堆文件——**这正是用户最会被骗的一处**。
计数经 `tools.DispatchResult` 带回（`res.ExternalCount`），由 `runTools` 阶段三累加。

### C.2 抓取时机与接缝

**时机 = 工具 `Exec` 内部、真正落盘那一刻之前**。这个位置天然满足三条既有纪律：

1. **在权限门之后**——`runTools` 的三道门（白名单 / strict / 确认）都在 `Exec` 之前，
   被拒绝的调用根本不会进 `Exec`，所以不会记下「一次没发生的写」；
2. **在 `atomicfile.Write` 之前**——`edit` 已经 `os.ReadFile` 拿到全文（before 内容零额外 IO），
   `write_file` 在覆盖分支补一次 `os.ReadFile`（新文件不读）；
3. **不新增 agent→tools 的依赖**——工具只调 ctx 里的注入口（见下），不认识 store/agent。

实现走**会话级 ctx 注入**（与 `tools.WithTodoSink` / `WithSkillSource` / `WithAttachmentSave`
完全同一套机制与理由：注册表是进程级单例，快照是会话级状态，父子会话会互相踩）：

```go
// internal/tools/snapshot.go（新增，叶子包，不 import store/agent）
type SnapshotHandle interface {
    // Commit 在写成功之后调用：实现方据此落一行快照（afterHash = 写完的内容 hash）。
    Commit(afterHash string, size int64) error
    // Abort 在写失败/被中断时调用：不落行（避免「没写成却记了快照」）。
    Abort()
}
// FileSnapshotter 在写之前调用：实现方读旧内容、算 before_hash、返回 handle。
type FileSnapshotter func(ctx context.Context, absPath string) (SnapshotHandle, error)
func WithFileSnapshotter(ctx context.Context, fn FileSnapshotter) context.Context
func FileSnapshotterFrom(ctx context.Context) FileSnapshotter // 未注入 → nil（不抓，静默降级）
```

- `edit`：`os.ReadFile` 之后、`atomicfile.Write` 之前调 `FileSnapshotterFrom(ctx)(ctx, path)`，
  拿到 handle 后 `Commit(sha256(新内容), len)`；写失败 `Abort()`。
  `snap := FileSnapshotterFrom(ctx); if snap != nil { h, _ := snap(ctx, path); defer h.Abort(); …; h.Commit(...) }`
  —— `Abort` 在 `Commit` 成功后必须是 no-op（defer 写法才不会重复落行）。
- `write_file`：`os.Stat` 判定覆盖后，`os.ReadFile`（≤1MB 才读）→ handle；新文件
  before 内容为空 = `before_hash=''`。
- **handle 的语义必须窄**（这是能把它做成纯接缝的关键）：`Commit` 只收
  `(afterHash, size)`，**before 的内容与 hash 由实现方（store）自己从 `absPath` 读**——
  工具不需要把文件内容在内存里转手一遍（`edit` 已经有了，但让它多传一个 `[]byte` 会让
  两个工具的写法分叉）。代价是 store 侧多一次 `os.ReadFile`；1MB 上限下可忽略，
  换来的是「工具层不知道快照长什么样」。
- **`Commit` 失败不该让工具失败**：快照是**旁路记录**，写文件本身已经成功——
  记日志 + 在结果里附一句「（本次改动未记快照）」即可。反过来（为了快照失败而回滚用户的写）
  是本末倒置。
- **路径解析归工具层**：`resolveToolPath(ctx, p)` 是私有函数，且「确认门与执行必须解析同一个文件」
  是既有纪律——sink 只接收**已解析的绝对路径**，agent 层不重复实现路径规则。
- **锚点 seq**：`runTurn` 在 `s.append(res.Message)` **之后**、`s.runTools(...)` 之前把
  `res.Message.Seq` 放进 ctx（`tools.WithSnapshotAnchor(ctx, res.Message.Seq)`）。
  时序是现成的：`turn.go` 里 `s.append(res.Message)`（第 293 行）→ 工具循环（第 306 行），
  `append` 已经把 store 分配的 seq 写回消息对象。拿不到（`Seq == 0`：纯内存模式 / 落盘失败）
  → **不抓快照**（诚实降级：这一轮的改动回滚不了，UI 在轮节点上标 ⚠️）。
- **`file_snapshots.seq` 与 `messages.seq` 同域**：锚点是 assistant 消息的 seq，所以
  「这个快照是哪条消息引起的」可以直接用 seq 追溯。但**回滚切段用的不是它**——
  父会话自己写的用 `owner_seq`（= seq，两者相等），子会话写的用 `owner_seq`
  （= 父会话那条派发消息的 seq，两者**不等**）。见 §C.2.1 与 C.5。

#### C.2.1 归属链：子会话的写怎么记到父会话的账上

用户拍板的语义是「父会话回滚 = 父对话 + **父子全部文件改动**一起还原」。要把子会话的写
挂到父会话的 seq 上，需要一条链：

```
父会话 messages[seq=42]（assistant，带 tool_calls，其中 agent_dispatch 的 tc.id = D）
        │  dispatch_id = D
        ▼
sessions(子会话).dispatch_id = D  ← 同一行还有 parent_id = 父会话 id
        │
        ▼
子会话执行 edit/write_file → file_snapshots(
    session_id       = 子会话 id,
    seq              = 子会话自己的 assistant 消息 seq,
    owner_session_id = 父会话 id（再上溯到顶层，见下）,
    owner_seq        = 42 )
```

**落地做法：owner 在「每次派发」时确定，随 ctx 带到写入点。**

`sessions` 加两列（幂等 ALTER，默认 `''`/`0`）：`child_owner_id` / `child_owner_seq`。
它们的语义要精确：

| 时机 | 值 | 为什么 |
|---|---|---|
| 新建子会话（`call.Session == ""`） | 父会话是顶层：`child_owner_id = parentID`、`child_owner_seq = 那条带 dispatch_id=D 的 assistant 消息 seq`；父会话自己也是子会话（嵌套）：**继承**父会话的 `child_owner_*` | 对**顶层**而言，这次派发仍发生在「顶层第 42 轮之后」；记成子会话自己的 seq 则顶层回滚时不可比 |
| **续跑**（`call.Session != ""`） | **按本次派发重写** `child_owner_seq` = 本次那条 dispatch 消息的 seq（`child_owner_id` 不变，仍是顶层） | 这是边界①的核心：同一个子会话在第 45 轮被续跑时改的 X，必须归到 45 而不是 42——否则回滚到 43 轮时 X 会被一起还原（**多还原**） |
| 运行期写入 | ctx 里的 `WithSnapshotOwner(ownerID, ownerSeq)` 是**权威** | 库里那两列只是「上一次派发的默认值」，供崩溃恢复/未注入时兜底 |

**为什么库里那两列不能当权威**：它们是**每次派发才更新一次**的，而一个子会话在一轮里可能
跑很久（多步工具循环）；只要 ctx 注入是权威，写入点就能拿到「本轮」的准确 owner。
反过来说，如果只靠库里的列，遇到「同一子会话被并行派发两次」（今天不可能：单会话最多一轮，
但结构上别赌）就会串账。

**ctx 注入（与锚点同一个注入口）**：

```go
// tools.WithSnapshotOwner(ctx, ownerSessionID, ownerSeq) —— 每轮由 runTurn 注入
//   ownerSessionID：顶层会话 id（顶层就是自己；子会话 = 它继承来的 child_owner_id）
//   ownerSeq：父会话空间的 seq ——
//             顶层会话：就是本轮 assistant 消息自己的 seq；
//             子会话：**父会话那条派发消息**的 seq（由 runDispatch 传进子会话的 runTurn）
```

于是 `runTurn` 要注入**两个**值：`WithSnapshotAnchor(ctx, res.Message.Seq)`（本会话自己的
seq，落 `file_snapshots.seq`）与 `WithSnapshotOwner(ctx, ownerID, ownerSeq)`（回滚归属）。
两者在顶层会话里相等，在子会话里不同——**这正是必须分成两个字段的原因**。

**`runDispatch` 怎么拿到「父会话那条派发消息的 seq」**：`runTools` 在阶段一登记 dispatch 时
就知道当前轮的 assistant 消息 seq（它已经在 ctx 里，见 §C.2 的锚点）；把它塞进
`tools.DispatchCall.DispatchSeq`，`runDispatch` 再传给子会话的 `SendWait` →
`runTurn` 的 owner 注入。**不需要**回库反查 `dispatch_id`（那条链在续跑时是歧义的）。

**归属链的权威性由写入侧保证，不在回滚时反推**：回滚时只需
`SELECT … WHERE owner_session_id = ? AND owner_seq >= N`，一次索引查询拿到父 + 全部子孙
会话的全部快照。不需要（也不应该）在回滚路径里做 `parent_id` 递归 CTE 再 join
`dispatch_id`——那条路既慢（多表扫描）又脆（`dispatch_id` 是 tool_call id，续跑时**不变**，
无法区分「第几次派发」）。

**多层嵌套**：`agent_dispatch` 今天深度恒 1（子 Agent 白名单不含它，见 AGENTS §3），
所以递归实际上只有一层。设计仍按「owner 沿上溯继承」写，是为了将来放开深度时**零改动**：
只要创建子会话时继承 owner，任意深度的写都自动落到顶层那个 owner_seq 上。

### C.3 blob 存储：**文件 vs 表**的取舍

| 维度 | 存表 `file_blobs(hash, content)` | **存文件 `<sessions>/fileblobs/<h[0:2]>/<hash>`（推荐）** |
|---|---|---|
| 事务一致性 | ✅ 与元数据同一事务 | ⚠️ 两阶段（先写 blob 再插行）；崩溃留孤儿 blob（无害，GC 清） |
| 大内容 | ❌ WAL 膨胀、每次读写走 modernc 逐字节拷贝、`VACUUM` 才能回收 | ✅ 页缓存/OS 文件缓存，随机读快一个量级 |
| 备份/迁移 | ✅ 单文件 | ⚠️ 两处（sessions.db + fileblobs/），备份脚本要一起拷 |
| 去重 | ✅ 主键 | ✅ 同款（路径即 hash） |
| 回收 | `DELETE` + `VACUUM` | 扫目录 + 查表 |
| 仓库先例 | 无 | **有**：`attachments/<session-id>/<rand>.<ext>`（DB 只存引用，`store/attachments.go`） |

**推荐：存文件**。理由排序：① 与 attachments 先例一致（两种内容寻址的存储不该用两套哲学）；
② 本仓库最大的性能痛点是「SQLite 全表读含正文」（`stats.go` 的覆盖索引就是为它加的），
把可能几百 KB 的旧文件内容塞进同一个库会直接放大这个痛点；③ blob 是**不可变**的
（hash 即内容），不需要事务保护，孤儿只会浪费磁盘、不会产生错误数据。

目录布局与纪律：
- `<sessions>/fileblobs/<hash[0:2]>/<hash>`（两级散列避免单目录十万文件）；
- 写入 = 临时文件 + `Sync` + `rename`（`internal/atomicfile` 现成），已存在则跳过（天然幂等）；
- 读取前校验 `sha256(内容) == 文件名`（防磁盘损坏/人为改名）；
- hash 形态硬校验（`^[0-9a-f]{64}$`），拒绝路径穿越。

### C.4 大小上限与跳过

| 规则 | 值 | 理由 |
|---|---|---|
| 单文件快照上限 | **1MB** | 覆盖 99% 的源码/配置；再大基本是生成物/数据文件 |
| 二进制判定 | 前 8KB（`readSniffBytes`）内出现 NUL 字节 | **复用 `file_read.go` 的既有判定 `isBinary`**（同一份启发式不写第二遍——只需把它导出成包内共用函数，不改语义） |
| 超大/二进制 | `skipped=1 + skip_reason`，**不写 blob** | 回滚时进 `FileSkip`，UI 明示「这个文件当时没抓，回滚不到」 |
| 单轮快照总量 | 无硬限（单次写 ≤ 工具自身上限） | 由 C.7 的清理策略兜住 |

### C.5 回滚执行（`file_snapshots` → 磁盘）

**范围（2026-12 用户拍板）**：回滚到 `anchor` 时还原的是**父会话与它全部子孙会话**在这一段
里的文件改动——子会话的**对话历史不动**（各自有 leaf，可继续续跑），只有文件跟着回去。

设目标切点 `N` = `anchor`（回滚后 leaf = `parent(anchor)`），
**归属切段** = `owner_session_id = 顶层会话 id AND owner_seq >= anchor`。

> 注意这里的 `anchor` 是**父会话的 seq**（用户点「回到这里」的那条消息）。子会话的快照行
> 记的 `owner_seq` 也是父会话空间的 seq，两者可直接比较——这就是 §C.2.1 那两列存在的全部理由。

**这个「用 seq 比较切段」成立的前提有两条，都要写成断言**：

1. **父会话自己的快照：`owner_seq == seq`**（顶层会话的 owner 就是它自己）——
   所以「按 owner_seq 切」与「按路径 seq 切」在父会话内部是同一件事；
2. **路径上的 seq 严格递增**（追加式模型天然满足：新消息 seq = MAX+1，parent 的 seq 必然更小；
   切到旧节点再追加，新 seq 更大）。`pathRows` 的返回值在 Debug/测试里校验单调性——
   它一旦破了，`>= anchor` 就会把不该还原的快照算进来。

**还有一条跨会话的前提**：子会话与父会话必须**共享同一个 workDir**（否则快照里的相对路径
指向不同的根）。今天天然成立——`openChildSession` 把父会话的 `workDir` 直接给子会话
（`child.SetWorkDir(workDir)`）。设计上把它写成不变式：**子会话的工作目录恒继承父会话**
（若将来允许子会话独立工作区，跨会话回滚必须按 workDir 分组，不能混成一个 plan）。

**收集（一条 SQL，不做递归 CTE）**：

```sql
SELECT session_id, seq, owner_seq, path, before_hash, after_hash, size, skipped, skip_reason
  FROM file_snapshots
 WHERE owner_session_id = ?            -- 顶层会话 id
   AND owner_seq >= ?                  -- 锚点 seq
 ORDER BY path, owner_seq ASC, seq ASC -- 每组最早在前（见 plan 第 2 步）
```

走 `idx_snapshots_owner(owner_session_id, owner_seq)`。**不需要**在回滚路径里
`WITH RECURSIVE` 遍历 `parent_id`：归属在写入时就已经摊平到顶层 owner 上（§C.2.1）。
嵌套层数增加时这条查询一个字都不用改。

```
plan(snapshotRows):
  1. 按 path 分组（**跨会话混在一起分**：父会话与子会话改同一个文件要合成一条链）
  2. 每组：restore = 该 path **owner_seq 最小**的那条的 before_hash
           （同一文件多轮修改 → 还原到「第一次被改之前」的状态；
            跨父子会话时也一样：父会话第 43 轮改、子会话（派发自第 42 轮）第 44 轮改，
            回滚到第 42 轮之后 → 取「owner_seq 最小」= 父会话那条的 before）
           expected = 该 path **owner_seq 最大**的那条的 after_hash
           （= 我们现在应该看到的磁盘状态；**这是冲突检测的基准**）
     owner_seq 相同时按 (session_id, seq) 再排——同一轮里父子会话都改同一个文件时，
     子会话的写**必然发生在父会话那条 dispatch 之后**（父会话要先落库那条 assistant 消息
     才会派发），所以定序是确定的：父会话的写在前。
     若 owner_seq 最小那条 skipped=1（大文件/二进制没抓）→ **整组进 skipped，不拿后面那条的
     before 顶替**：那会还原出一个「中间态」文件，比「明说没抓」误导得多
     （用户会以为文件回到了切点，其实回到的是某个中间版本）。
  3. 逐 path 读盘算 hash，按 (restore, expected, 磁盘现状) 三者的组合分四种
     （先决条件：`expected != ''` 才有 hash 可算；`expected == ''` 表示"写之后这个文件不存在"）：
     ┌ 磁盘不存在 ─┬ restore == '' → no-op（已经是目标状态）
     │             └ restore != '' → **恢复写入**（无损：文件本来就没有，
     │                              写回快照内容不会覆盖任何东西），
     │                              结果里标 recreated=true（UI 提示「这个文件
     │                              之前被删了，已按快照恢复」）
     └ 磁盘存在 ───┬ expected == '' → **冲突**（我们以为此刻不该有这个文件——
                   │                多半是外部新建/回滚到中途），不删
                   ├ hash == expected → 安全，执行 restore（写回或删除）
                   └ hash != expected → **冲突**（用户手改过 / bash 改过），不覆盖
  4. 有任何冲突 → 整批放弃（fail-closed），返回 conflicts 清单，**leaf 不动**
```

**「删文件」也属于回滚动作**：`restore == ''` 且磁盘 hash == expected 时，回滚 = 删掉这个文件
（它在这一段里是 agent 新建的）。删除同样受冲突检测保护——**只有确认磁盘上那份就是
agent 写的那份，才删**。这条比「写回」更需要小心：写错只是内容不对，删错是文件没了。

#### C.5.1 两条边界规则（用户明确要求，各配一个测试）

**边界①：子会话跨多轮续跑 → 按「父会话 seq」切段，不是「子会话自己的 seq」。**

同一个子会话可以被父会话在不同轮次重复派发（`agent_dispatch` 的 `session` 参数续跑）。
它的 `messages.seq` 是连续累加的（第 1 次派发写 seq 1..20，第 2 次续跑写 21..40），
而它在父会话里对应的是**两个不同的轮次**。所以：

- 每次派发（新建 or 续跑）都把「本次派发 tool_call 所在父消息 seq」作为 `owner_seq`
  传给子会话（§C.2.1 的 ctx 注入）；
- 子会话**这一轮**的写用这个新的 `owner_seq` → 回滚到第 45 轮时，只有 `owner_seq >= 45`
  的快照参与还原，第 42 轮那批**不动**；
- 注意口径：这里的「回滚到第 45 轮」= `anchor` 是第 45 轮的**那条 user 消息**，
  所以 `owner_seq >= 45` 会把「第 45 轮派发的写」算进来（它在用户点的那条消息之后发生）。
  用户想要「连第 45 轮那批也不还原」时，锚点选第 45 轮之后的那条即可——与现有的撤回语义一致
  （撤回锚点是「这条及其之后离开路径」）。
- 测试 `TestChildResumeUsesDispatchTurn`：父会话第 42 轮派发子会话改 A（owner_seq=42），
  第 45 轮续跑同一子会话改 B（owner_seq=45）；**锚点 = 第 45 轮那条 user 消息** →
  只还原 B，A 保持第 45 轮时的内容。

**边界②：回滚点之后才创建的子会话 → 它的全部改动都算「之后」，全部还原。**

子会话的创建时刻就是某次派发，而派发必然发生在父会话的某条消息里（那条消息 seq = owner_seq，
且它**严格大于**该轮 user 消息的 seq）。所以「锚点之后才创建的子会话」在数据上自然满足
`owner_seq >= anchor`——**不需要额外判据**（不需要比较 `sessions.created_at`，也不需要遍历
`parent_id`）：

- 它改过的文件（`before_hash != ''`）→ 还原到它第一次改之前的内容；
- 它**新建**的文件（`before_hash == ''`）→ **删掉**（这正是「含删除它新建的文件」）；
- 子会话行本身**不删、不归档**（历史保留，用户还能切进去看它当时做了什么、也能续跑）。
  这一点要在 UI 文案里说清：「子会话的对话还在，只是它当时的文件改动被还原了」。

测试 `TestChildCreatedAfterAnchorFullyReverts`：父会话第 42 轮的 user 消息 seq=40、该轮的
dispatch 消息 seq=42；子会话新建 `foo.go` 并改 `bar.go`。**锚点 = 40** →
`foo.go` 被删、`bar.go` 还原、子会话的 `chat.history` 一条不少、子会话 leaf 未变。

**冲突检测与父会话同一套规则（用户明确要求）**：子会话改过的文件若磁盘上已被用户手改
（`hash != expected`），同样进 `conflicts`、**同样不覆盖**、同样触发整批 fail-closed。
没有「子会话的改动可以随便覆盖」这种例外——文件系统不认识是谁改的，覆盖就是覆盖。

执行顺序（跨磁盘与 DB 不可能真原子，只能把失败面缩到最小）：

```
① 算 plan（纯函数，可单测：跨父子会话合并同一 path 的链）
② 冲突检查（有冲突即返回，一个字节都不写）
③ 读盘缓存当前内容（供失败回退用）
④ 逐文件原子写（restore=='' 则删文件）；记录已写清单
⑤ 任一步失败 → 逆序还原已写文件（用 ③ 的缓存）→ 返回错误，leaf 不动
⑥ 写库（一个事务）：UPDATE sessions SET leaf_seq/leaf_len（**只改顶层会话那一行**；
   子会话的 leaf 一个都不动——对话历史不回滚是本次拍板的核心）
⑦ 重载内存历史 + 重算 context + 发事件
```

**子会话的 leaf 为什么一个都不动**：子会话的对话是它自己的（AGENTS §2.3），父会话回滚
表达的是「回到我这一轮之前」——它没有权限、也没有理由去改子会话的对话位置。文件之所以
跟着回去，是因为**工作区是父子共享的单一状态**（子会话继承父会话的 `workDir`），
不回去的话「父会话回到第 42 轮」就只是半句话：历史回去了、工作区还停在第 46 轮。
两件事的分界就是：**共享的东西（文件）跟着回，各自的东西（对话）不动**。

**中间崩溃的残留（诚实记录）**：④ 完成、⑥ 之前进程被杀 → 文件已还原、leaf 未动。
用户看到旧时间线 + 新文件；重试 `files:true` 会因 hash 不符报冲突 → 给 `files:false` 的重试入口
（只切 leaf）。这条写进文档与错误文案，不假装原子。

**额外的缓解（低成本，建议做）**：④ 与 ⑥ 之间写一条**意图日志**
（`sessions/<id>/pending-restore.json`：目标 leaf + 已写清单 + 时间戳）。启动时若发现它存在，
说明上次回滚没收尾 → 后端**自动补做 ⑥**（leaf 移过去；文件已经对了），并删日志。
这让「崩溃残留」从「要用户手动重试」变成「下次启动自愈」——代价是几十行代码与一个文件。

### C.6 分支与文件的关系（一条必须讲清的边界）

文件系统是**单份共享状态**，而历史是**多条分支**——两者天生不同构。由此有三条必须讲清的规则：

1. **切分支默认不动文件**（`files` 缺省 false）：用户切到旧分支时，磁盘上仍是最后一次写的结果。
   这是刻意的——文件是用户/构建/外部工具的共同产物，切一下历史就重写工作区太粗暴。
2. **回滚的范围按 `owner_seq >= anchor` 切段，跨父子会话（2026-12 用户拍板）**：
   - **父会话自己的写**：`owner_seq = 自己的 seq`，与「离开路径」的切段一致；
   - **子会话的写**：`owner_seq = 派发它那条父消息的 seq`，所以「父会话第 42 轮派出去、
     在第 42~46 轮之间发生的子会话改动」全部纳入；
   - **别的分支的快照一概不碰**：如果用户先切到旧分支再回滚，那么「当前 leaf 路径」上
     `seq >= anchor` 才是父会话自己的账；子会话的账仍按 owner_seq 切（子会话不随父会话
     分支切换而分裂——它是独立会话，它的 owner_seq 是确定的）。
3. **因此「历史与文件一致」只在「一直单链」或「每次都带 files:true 回滚」时成立**。
   UI 必须如实表达这个边界：树面板在「有外部操作 / 有跳过的快照 / 当前磁盘与路径期望不符」时
   显示 ⚠️，并在回滚确认文案里写明「文件只会还原到 N 个文件，另有 M 处无法还原」。

**子会话范围的一句话口径（写进确认文案）**：
「本次回滚会还原 **X 个父会话文件改动 + Y 个子会话文件改动**（涉及 Z 个子会话）；
子会话的对话历史不会回滚。」——三个数字都能从 plan 里算出来，不猜。
（二次确认弹窗的结构见 §H#1：第一段说还原什么，第二段说**还原不了什么**（bash 处数），
第三段说子会话对话不动。）

**不做的事**：不做「切分支时自动同步文件」的隐式行为（一次点击重写工作区是本仓库明确
避免的那类动作——`workspace_rollback` 也要求干净工作区 + 确认门）。

**回滚之后快照本身怎么处理**：**保留，不删**。理由：

- 被回滚掉的那一段历史仍然在库里（分支），用户可能再切回去——那时那段快照就又是「当前的账」；
- 快照是**不可变记录**，删它等于抹掉「这里曾经改过什么」的证据；
- 真正需要回收的是**空间**，那由 C.7 的保留期/上限处理（与「回滚是否发生」无关）。

一个具体的坑要避开：**不要**在回滚成功后把「被还原的那些快照」标成已消费——
下一次 `files:true` 回滚到同一个切点时，它们必须仍然可用（幂等：再回滚一次是无操作，
因为 `owner_seq >= anchor` 的行此刻与磁盘已一致——`hash == expected` 且 `restore == expected`，
plan 收敛为空操作）。

**子会话的快照在回滚后仍然属于它的父会话**（不会被「回滚」这个动作改变归属）：
用户之后可以切回旧分支、再带 `files:true` 回滚一次，那时这批快照会再参与一遍
（正向幂等）。归属是**写入时定死的事实**，回滚是只读它的操作。

### C.7 清理策略

| 维度 | 默认 | 可配 |
|---|---|---|
| 会话快照保留期 | 30 天（`created_at`） | 是（设置页二期） |
| 单会话快照占用上限 | 256MB | 是 |
| 全库 blob 占用上限 | 2GB | 是 |
| 当前会话最近 20 轮 | **恒不清理** | 否（防止「刚撤回就没了」） |

回收时机：后端启动后 + 每 24h 一次（后台 goroutine，失败只记日志）。
GC 算法：扫 `fileblobs/` 目录 → 对每个 hash 查 `idx_snapshots_blob` 是否有引用 → 无引用且
文件 mtime > 1h（避开正在写的临时态）→ 删除。**孤儿 blob（写成功但插行失败）由此自然回收。**

**元数据行的清理（与 blob 分开算，顺序不能反）**：

1. 先按策略删 `file_snapshots` 行（保留期 / 单会话上限，按 `created_at` 从旧到新）；
2. **再**跑 blob GC（此时被删行引用的 blob 才成为孤儿）。
   顺序反了的话，GC 会把「马上就要被删的行的 blob」当孤儿删掉，而那一行还在——
   下次回滚读不到内容（缺失 blob），直接变成一次失败的还原。

**「最近 20 轮不清理」的口径**：轮 = 真实 user 消息（与 `SessionStats.turns` 同口径），
不是「最近 20 条消息」——一条轮里可能有几十条工具消息。实现上取**owner 路径上**最后 20 个
user 节点的 seq 作为下界（`WHERE owner_seq >= 下界` 的行一律保留——按 **owner_seq** 而不是
`seq`，否则子会话的快照会被按它自己的序号误判）。

**上限触发时的取舍**：单会话/全库超限时**从最旧的快照删起**（时间上最不可能被回滚），
并且**按文件分组整组删**（同一 path 的 before/after 链条要么都在、要么都不在——
留半条链会让回滚算出错误的 expected）。

**跨会话清理（子会话的账归父会话管）**：快照的保留策略按 **`owner_session_id`** 计，
不按 `session_id`——因为「这条快照还能不能被回滚到」只取决于 owner 的轮次跨度：

- 「最近 20 轮不清理」的下界按 **owner 的** user 轮次算（父会话第 N-19 轮起）；
- 单会话上限按 `owner_session_id` 汇总（父会话 + 它的全部子会话的快照算**一份**账）；
- 归档父会话时，**不删**子会话快照（归档是可恢复的，删了就真回不来了）；
  只有「父会话 + 全部子会话都归档」且超过保留期时才清——判据用 `sessions.parent_id` 上溯。

这条的理由与拍板项 12 一致：**子会话的写是父会话历史的一部分**，按子会话单独算保留期
会出现「父会话第 42 轮还在保留期内，但它派出去那批改动已经被清掉」——回滚会静默少还原文件。

---

## D. 右侧轮次面板改造（树形视图）

### D.1 现状（代码事实）

- 组件：`App.tsx` 的 `<aside className="outline-aside">` → `TurnPanel`（`panels/TurnPanel.tsx`）；
- 数据：`useTurnOutline(blocks)` → `turnGroups(blocks)`（**从 blocks 分组**，一条 `kind==="user"`
  开一轮，其后的 dispatch 归入该轮）；`outlineLabel` 做单行截断；
- 跳转：`onJump(uid)` → `handleOutlineJump` 只记目标，Thread 负责扩窗 + `scrollIntoView` + 高亮
  （`data-uid` 精确匹配，**不许按文本找**）；
- 样式：`styles/panels.css` 的 `.turn-*`（左侧导轨线、组头 + 组内子 Agent、`data-active` 高亮条）。

**改造原则：不推翻，叠加。** 现有 `turnGroups` 继续服务「当前路径的线性视图」，
树视图新增一层数据源（`chat.tree`）+ 一层纯函数（`tree-model.ts`），**跳转仍走同一个 `onJump`**。

### D.2 数据流

```
App
 ├─ useSessionTree(source, currentId)        // 新 hook：拉 chat.tree、折叠/过滤状态、setLeaf
 │    ├─ tree: ChatTreeResult | null         // null = 老后端无 chat.tree → 回落线性视图
 │    └─ actions: { jump(seq), setLeaf(seq, files), branchFrom(seq) }
 ├─ useTurnOutline(blocks)                   // 保留：产出 seqToUid 映射 + activeUid
 └─ <SessionTreePanel … />                   // 树面板（tree 为 null 时内部回落 TurnPanel）
```

**seq → uid 映射**：树节点带 `seq`，跳转需要 blocks 的 `uid`（Thread 的 `data-uid`）。
在 `use-turn-outline` 里加一个 `useMemo` 建 `Map<number, number>`（seq → uid）。

**关键事实：只有 `kind==="user"` 的块带 `seq`**（`dispatch` 块没有 seq，见 `shared/blocks.ts`）。
所以映射表**只能收 user 块**——树面板的「单击跳转」也只对 user 节点可用；
对 assistant/tool/checkpoint 节点，单击 = 滚动到**该轮**（它的 user 祖先块），
没有 user 祖先（root 之前的孤儿/会话开始）时该行不可点（做成 `<div>` 而不是 `<Button>`，
与现有「会话开始」行的处理一致）。

### D.3 纯函数层 `frontend/src/components/panels/tree-model.ts`（node:test 可测）

```ts
export interface TreeRow {
  seq: number; parentSeq: number; depth: number;
  kind: "user" | "checkpoint" | "notice" | "assistant" | "tool";
  label: string;            // 单行摘要（复用 outlineLabel，同一份实现）
  turn?: number;
  children: number;         // 直接子节点数（>1 = 分支标记）
  onPath: boolean;          // 当前路径（false = 废弃分支 → 灰化）
  isLeaf: boolean;          // === leaf_seq
  files: number;            // 该轮的写文件数
  external: number;         // 该轮的不可快照写数（⚠️）
  collapsible: boolean;     // children > 0 且有可折叠的后代
  collapsed: boolean;
}

export function flattenTree(
  tree: ChatTreeResult,
  opts: { filter: "all" | "user" | "changed"; collapsed: Set<number> },
): TreeRow[];
```

规则（每条都可单测）：

1. **节点粒度**：`user` / `checkpoint` / `notice` 各自成行；`assistant` / `tool` **折叠进最近的
   前驱 user 节点的统计**（`files` / `external` 累加），不单独成行（`include=full` 时才展开）。
   注意「前驱」是**沿 parent 链**找的，不是「seq 更小的最近一条」——分支之后两者会分叉，
   按 seq 找会把 B 分支的工具调用算到 A 分支那一轮的账上。
2. **depth** = 从 root 到该节点的**成行祖先**数（折叠掉的中间节点不计——否则缩进会跳 3~5 级）。
3. **过滤**：`user` = 只留 user 行；`changed` = 只留 `files>0 || external>0` 的轮。
   **命中节点的祖先必须一起保留**（`visibleSet` 标记，祖先渲染成「路径骨架」样式）——
   否则缩进失去意义、用户看不出这个节点在哪条分支上。
4. **折叠**：`collapsed` 里的节点隐藏其后代（分支点行仍显示 `▸ N` 计数）。
   默认折叠**所有非 on_path 的分支**（长会话里废弃分支不该占满侧栏），on_path 恒展开。
   **默认折叠的判据不能用「off_path 且 children>0」**（那样第一个分叉点就把整条废弃分支
   藏起来，用户根本不知道有分支）：默认折叠的是「off_path 分支的**中段**」——
   分叉点（`children>1` 且 on_path）恒展开，它的每个 off_path 子分支各显示**首节点一行**
   + 「▸ 还有 N 个」，展开才继续。这条是「灰化但不隐藏」（用户要求）的具体实现方式。
5. **顺序**：按 `seq` 升序（= 时间顺序），与现有面板一致（用户的心智模型是时间线，不是树遍历）。

### D.4 视觉结构（CSS，沿用 `.turn-*` 的皮）

```
.stree-panel                       // 容器（替代 .turn-panel 的位置，复用 .outline-aside 外框）
.stree-toolbar                     // 过滤 chips + 「N 个分支」汇总
.stree-list
  .stree-row[data-depth="0..n"]    // 缩进用 --depth 变量：padding-left: calc(6px + var(--depth) * 13px)
                                   // 这里的 depth 是**视觉深度** = min(成行祖先数, 8)
                                   // （tree-model.ts 的 STREE_MAX_VISUAL_DEPTH）：单链会话每轮
                                   // 再深一层，不钳制的话 20 轮就把标签挤出 288px 面板
    ::before                       // 竖连接线（按同一个视觉深度定位到父节点的竖线位置，
                                   // 钳制边界处共列、连贯）
    ::after                        // 横向短枝（└/├）；被钳制的行短枝更紧凑 + 标签前「⋯」记号
                                   // （data-clamped="true"）——不静默压平
    [data-last="true"]             // 最后一个子节点：竖线到这一行为止（└）
    [data-leaf="true"]             // 当前 leaf：左侧 2px 高亮条 + 「你在这里」小标
    [data-off-path="true"]         // 废弃分支：opacity .55 + 文字降级到 --fg-faint
    [data-active="true"]           // 当前跳转高亮（与现状同款：压导轨的高亮条）
  .stree-branch-badge              // 「2 分支」小徽标（children > 1）
  .stree-badge                     // 轮次号（复用 .turn-badge）
  .stree-files                     // 「3 文件」小标（files > 0）；child_files > 0 时显示
                                   // 「3 文件 · 1 子 Agent」（title 列出是哪个子会话改的）
  .stree-external                  // 「⚠ 2」外部操作（external > 0，title 说明不可回滚；
                                   // 含子 Agent 里跑的 bash —— 这一处最容易让人误以为回滚干净了）
  .stree-actions                   // 行尾操作（hover / focus 时**覆盖式**出现——absolute
                                   // 定位不占 flex 槽位，否则一 hover 标签就被挤窄；触摸端常驻）
    button "⟲ 切到此"              // = setLeaf(seq)（该节点留在路径上）
    button "↳ 从此分支"            // = rewind(seq) + 回填输入框（user 节点才给，见 D.5）
```

**宽度**：`.outline-aside` 现在 240px，树缩进后偏挤。改法：树模式下宽度 280px，
并在面板头给一个拖拽把手（`resize` 存 localStorage）。小屏（≤768px）维持现状直接不显示。

### D.5 交互：跳转 vs 切 leaf（**不用双击**）

| 节点状态 | 单击 | 行尾按钮（`user` 节点才给两个；其它节点只给「⟲」） |
|---|---|---|
| **在当前路径上** | 滚动跳转（`onJump(uid)`，扩窗 + 高亮，**现状行为一字不改**） | 「⟲ 切到此」= `setLeaf(seq)`（此时无视觉变化 → 按钮 disabled，title「已在当前路径上」）<br>「↳ 从此分支」= **`rewind(seq)` + 原文回填输入框**（= 旧的「编辑/撤回后重发」链路，见下） |
| **不在当前路径上**（废弃分支） | **切到该分支并跳转**：`setLeaf(seq)` → 等 `historyLoaded` → `onJump(uid)` | 「⟲ 切到此」= `setLeaf(seq)`（路径到该节点为止）<br>「↳ 从此分支」= **`rewind(seq)` + 回填**（先切过去再把它自己移出路径） |

**「切到此」与「从此分支」的差别必须说清**（这是最容易做错的一处）：

- `setLeaf(seq)` 让**该节点留在路径上**（「我从这里接着往下长」）；
- `rewind(seq)` 让**该节点自己离开路径**（「我要把这条改一下重发」）——它内部就是
  `setLeaf(parent(seq))`。所以「从此分支」作用在 user 节点上必须走 rewind，否则用户重发
  会出现两条一模一样的用户消息（一条旧的、一条新的，都是同一个父亲）——那是明显的 bug。

为什么这样分：用户点一个废弃分支上的节点，本意必然是「我要回到那条分支上」——
在当前时间线里根本没有它的位置，纯滚动跳转会是**静默的 no-op**（仓库既有教训：
「点了没反应」比报错更坏）。而双击切 leaf 不可发现、触摸端冲突、键盘不可达——**否决**。
行尾按钮显式、可 Tab、可触摸，且把「只切」与「切并回填」分成两个动作。

**风险与保护**：切 leaf 是**破坏性视觉动作**（时间线整段换掉），且 busy 时不允许
（`ErrBusy`，与 rewind/compact 同款）。所以：
- 有未发送的输入框草稿时，先确认（「当前输入还没发出去，切换后草稿会保留在输入框，继续？」）；
- 切 leaf 后时间线重载，**滚动位置回到新路径末尾**（不是保持旧位置——旧位置在新历史里可能不存在）。

### D.6 过滤模式（用户要求的三档）

| 模式 | 显示什么 | 用在哪 |
|---|---|---|
| **全部**（默认） | 所有成行节点（含废弃分支，灰化） | 找历史、看分支结构 |
| **只看用户消息** | 只留 `user` 行 + 分支骨架（`checkpoint`/`notice` 隐藏） | 长会话快速定位「我说过什么」 |
| **只看有改动** | 只留 `files>0` 或 `external>0` 的轮 + 分支骨架（`files` **含子会话的改动**，见 `TreeNode.Files`） | 验收「这次改了什么、要回滚到哪」 |

三条纪律：

1. **骨架必须保留**：过滤后仍要能看出节点属于哪条分支（祖先以低对比度显示，不可点是正常的）；
2. **过滤不改变 leaf 与路径**：纯展示层筛选，`on_path` 与当前 leaf 的判定不受影响；
3. **过滤状态按会话记**（切走再切回保持），但**不落库**（纯 UI 偏好，与折叠态同款）。
   折叠态与过滤态都放 `useSessionTree` 的局部 state，不进 `UIState`——它们不影响任何数据事实。

### D.7 与现有 outline 的兼容（降级路径必须写死）

| 情形 | 行为 |
|---|---|
| 后端有 `chat.tree`（新后端） | 渲染树；`tree.nodes` 里 `children<=1` 且全在路径上时，视觉与现在的线性轮次列表几乎一致——区别只在缩进：单链会话每轮深一层（depth 逐轮 +1），缩进**按视觉深度 `min(depth, 8)` 钳制**（tree-model.ts 的 `STREE_MAX_VISUAL_DEPTH`），所以 8 轮之后不再右移，最深层标签仍有约 119px 可读宽度（钳制行带「⋯」记号，不静默压平） |
| 后端无 `chat.tree`（老后端 / `CodeInvalidRequest`） | **回落现有 `TurnPanel`**（`tree === null` 分支），不报错、不空屏——AGENTS.md §5 坑 11「老后端 + 新前端不许炸」 |
| 纯内存模式（无持久化） | `chat.tree` 回空树 → 同上回落；`setLeaf` 报错，行尾按钮 disabled |
| 树节点在别的分支上（当前时间线无对应块） | 单击 = 先 `setLeaf` 再跳（D.5）；`seqToUid` 里查不到就等 history 重载后重试一次，仍查不到则只切不跳（并 toast 说明） |
| 非 user 节点（assistant/tool/checkpoint） | 单击 = 跳到该轮的 user 祖先块（D.2 的映射表只有 user 块）；无 user 祖先时不可点 |
| 轮次编号 | 树面板的 `turn` **必须与 `SessionStats.turns` / 现有 `turnGroups` 同口径**（真实 user 消息、`notice` 与 `checkpoint` 不算）——三处编号不一致是本仓库反复踩的「同一屏两个数字矛盾」 |

### D.8 文案口径（「撤回」→「回到这里」）

面板与气泡的现有文案是「撤回这条及其之后的全部对话」。分支化之后这句话**不再准确**
（之后的不删了，还能找回）。建议：

- 气泡动作：`撤回` → **`回到这里`**（title：「这条及其之后会从当前对话里移开，但**不会删除**——
  可以在右侧分支面板里再切回来」）；前端改文案的位置是 `components/thread/blocks/Block.tsx`
  的 `title`（现有「撤回这条及其之后的全部对话，原文回到输入框」）与 `NO_SEQ_HINT` 的相邻行；
  `shared/blocks.ts` 的 `planRewind` 函数名与语义**不动**（它仍然是「时间线截断到锚点之前」，
  只是现在这个截断是**可视层**的，不是数据层的删除）——函数名改动的收益为零，风险是多处引用漂移；
- 面板行操作：`切到此` / `从此分支`；
- 设置里给一个开关（二期）：「切分支时同时还原文件」。

---

## E. 迁移与兼容

### E.1 命名区分（两个 parent 不许混）

| 字段 | 位置 | 语义 | 现状 |
|---|---|---|---|
| `sessions.parent_id` | sessions | **会话级**父子：子 Agent = 独立会话（AGENTS.md §2.3） | 已有，不动 |
| `sessions.child_owner_id` / `child_owner_seq` | sessions | 子会话的**快照归属默认值**：它的写在父会话哪一轮之后（§C.2.1） | 新增（幂等 ALTER，默认 `''`/`0`） |
| `messages.parent_seq` | messages | **消息级**父指针：会话树的一条边 | 新增 |
| `file_snapshots.owner_session_id` / `owner_seq` | file_snapshots | 这笔写记在**谁的**哪一轮之后（回滚切段的唯一依据） | 新增（表本身也是新增） |

**四个「parent/owner」字段的分工必须一眼分清**（这是最容易混的一处）：

- `sessions.parent_id` = **谁是父会话**（静态关系，创建时定死）；
- `messages.parent_seq` = **这条消息接在哪条之后**（会话树的一条边，同一会话内）；
- `sessions.child_owner_*` = **子会话的写默认归到父会话哪一轮**（兜底值）；
- `file_snapshots.owner_*` = **这次写实际归到哪一轮**（权威值，写入时由 ctx 决定）。

代码与文档统一叫法：**「会话树」= messages 的树**；**「子会话」= sessions.parent_id**。
`store` 里两个方法名也要分开：`CreateChild(parentID, …)`（会话级，不变）vs
`LoadPath(sessionID, leafSeq)`（消息级，内部就是 A.3 的 `pathRows`）。
`agent.Persistence` 接口里对应的新增方法是 `SetLeaf` / `LeafSeq`（§E.4）——
`Load` 的签名**不变**（它内部改为走 leaf 路径），所以 `AttachTo`/`EnablePersistence` 零改动。

### E.2 老客户端兼容矩阵

| 场景 | 老客户端行为 | 新后端下是否仍然正确 |
|---|---|---|
| 点撤回 → 本地 `planRewind` 截断 | 截断到锚点之前 | ✅ 新路径恰好是截断后的前缀 |
| 收到 `chat.rewound` → `reduceRewound` | 同上（幂等：锚点已不在） | ✅ 载荷形状未变 |
| 撤回后刷新 → `chat.history` | 拿全量历史 | ✅ 得到 leaf 路径（它以为的「删后历史」） |
| 撤回后继续发消息 | `chat.send` | ✅ 追加到 leaf（= 截断点）→ 单链延续 |
| 依赖 `removed` 数值做展示 | 「删了 N 条」 | ✅ 数值口径保持（离开路径条数） |
| 侧栏消息数 | `session.list` 的 `messages` | ✅ 改成 `leaf_len`（切分支后条数跟着变，符合直觉） |
| 未知事件 `chat.leafChanged` | 忽略 | ✅ 现有 ws 层按方法名映射，未知 = no-op |
| **认知差** | 用户以为历史被删了 | ⚠️ 数据没删但老客户端看不到找回入口——纯增强，不是破坏 |

**协议 Version 不需要递增**：所有变更是「加法 + 同名方法语义收紧」。
唯一需要评估的是 `chat.rewind` 的语义变化——但如上表，**老客户端的可观察行为逐条不变**
（它看到的始终是 leaf 路径），所以 Version 保持 `2`。
（若拍板选「新增 `chat.branch` 而让 `rewind` 保持删除语义」，那就要同时维护两套历史语义——
存储上不可能同时「删了」又「还在」，只能靠 `rewind` 真的 DELETE，那树就永远只有一个分支，
方案自相矛盾。**这是推荐「保留名改语义」的硬理由。**）

### E.3 压缩与树的关系（规则汇总）

1. **压缩改变历史形状，不只是记一个影子集**：`AppendCheckpoint` 必须重接父指针
   （被压段之后那条存活行改挂到检查点上，§A.3）。这一步让「检查点属于哪条分支」
   由 parent 链自己回答——**路径解析里不需要第二套分支判定**。
2. **压缩是分支局部的**（这条是上一条的推论）：从压缩**之前**的位置分叉出去的新分支，
   它的链不经过检查点，看到的是未压缩的原文——这是**正确**的（压缩是那条分支的历史处理）。
3. **切分支不会撤销压缩**：旧分支上的检查点行原样留着，切回去仍然生效。
4. **新分支可以再压缩**：各分支有自己的检查点（每份摘要 1~4KB，可接受）。
5. **`AppendCheckpoint` 的区间校验**：`skip/count` 解析到当前路径 surface，越界拒绝（fail-closed）。
6. **`surfaceCounts` 的旧口径作废**：侧栏条数改读 `sessions.leaf_len`（否则废弃分支的行会被算进去）。
7. **`shadowed_seqs` 保持原样**：它仍然是「哪些原文被这份摘要顶替」的权威，
   回放仍然按它跳过——树模型没有替换它，只是给它加了一条 parent 边。
8. **子会话的压缩与父会话完全独立**（AGENTS §2.3 的既有语义，本次不动）：
   子会话有自己的检查点与影子集合，父会话回滚**不触碰**子会话的对话历史，所以也不碰它的检查点。
   「父会话回滚把子会话改的文件还原了，但子会话的历史里仍写着那些改动」——这是**预期的**：
   历史是当时发生了什么，文件是当前的状态。UI 文案要把这句说清（§H#18）。

### E.4 `Persistence` 接口的加法（agent 层不见 SQL）

```go
// internal/agent/persistence.go 追加两个方法（实现 = store）
// SetLeaf 把叶指针移到 seq（0 = 空路径），返回 (离开路径条数, 新叶 seq)。
// 只动指针，不删任何行——这就是「撤回」在新模型下的全部实现。
// 「回到最新」由调用方传树里最大的 seq，不走特殊值（0 已被「空路径」占用）。
SetLeaf(sessionID string, seq int64) (removed int, leaf int64, err error)
// LeafSeq 读当前叶。0 = 空路径（用户回到第一条消息之前）——**不是「未知」**。
LeafSeq(sessionID string) (int64, error)
// ChildOwner 读子会话的快照归属默认值（它的写在父会话哪一轮之后）。
// 返回 ("", 0) = 老会话/顶层会话（调用方回落「自己就是 owner」）。
ChildOwner(sessionID string) (ownerID string, ownerSeq int64, err error)
// SetChildOwner 落子会话的快照归属（CreateChild 之后立刻写一次；续跑时按本次派发重写）。
SetChildOwner(sessionID, ownerID string, ownerSeq int64) error
```

- **`CreateChild` 的签名要扩**（这是唯一一处「不是纯加法」的接口变更）：
  `CreateChild(parentID, agentID, dispatchID string, ownerID string, ownerSeq int64) (string, error)`
  ——owner 与子会话行**同一条 INSERT** 落库，避免「子会话建好了但归属没写上」的中间态。
  调用点只有 `openChildSession` 一处（`internal/agent/dispatch.go`），改动面可控。

- `Rewind(sessionID, seq)` 从接口里**删掉**（语义被 `SetLeaf` 覆盖）；`Store.Rewind` 一并删除。
  **但删除前先想清楚一件事**：`Store.Rewind` 里那段「收缩影子集合」的循环（引用已删行的项滤掉、
  影子全空的检查点整条删掉）在树模型下**不再需要**——没有行被删，检查点也不会失效。
  这正是分支模型的一个附带收益：那段级联收缩逻辑（现有实现里最绕的一段）可以整段退役。
- **纯内存模式（`st == nil`）降级**：没有存储就没有历史行、也没有 leaf——
  `SetLeaf` 直接返回错误（`"未启用持久化，无法切分支"`），前端据此禁用树面板
  （与 `chat.tree` 空树同一条：**不编一棵假树**）。
- `agent.Session.Rewind/SetLeaf` 的实现要点：busy 拒绝 → 落库（fail-closed）→
  `st.Load(id)` 重载 `s.history` → **重算 context** → 发事件。**不再做「内存截断 + 锚定算术」**：
  重载就是真相，两条路径（内存/库）从根上不再有机会分叉。
- **context 不能走 `restoredUsage`**（这是个必须钉住的坑）：`restoredUsage` 的第一优先级是
  「库里落库的测量」（`ContextUsageOf`），而那份测量属于**切分支之前那条路径**——
  直接用它等于把旧路径的占用贴到新路径上（静默的假数字）。切分支后只能：
  ```
  s.context = estimateContextUsage("", nil, s.history)   // 现有估算函数
  s.context.Window = s.contextWindowFor(agentID)
  s.context.Estimated = true                             // 如实标注「这是估算」
  ```
  并按 `persistContextUsage` 落库（否则重启后又读回旧路径那份）。下一轮 `chat.done` 的真实
  `prompt_tokens` 会把它校正回来——这与「新会话/切会话清零」是同一条纪律：**旧路径的测量对新路径无效**。
  单测：`TestSetLeafRecomputesContextFromPath`（库里故意存一个离谱的旧测量，切分支后不得沿用）。
- **`rewind` 与 `setLeaf` 在这一点上不同**：`rewind` 的新路径是旧路径的**前缀**（只砍尾巴），
  锚定算术（`reanchoredUsage`，现状实现）在数学上仍然成立；而 `setLeaf` 切到**另一条分支**时，
  进入段与离开段毫无关系，锚定算术会算出假数字。
  **建议两者都走「重估 + Estimated=true」**：重估的实现只有一份（少一条分支就少一处漂移），
  而锚定算术省下的那点精度在下一次 `chat.done` 就会被真实 `prompt_tokens` 覆盖——
  用「一份实现」换「一次可能不准的中间态」，划算。

### E.5 其它受影响的旧逻辑（改动面清单，供 F 排期参考）

| 位置 | 现状 | 分支化后的处理 |
|---|---|---|
| `store.Load` / `Latest` | 全表 `surfaceRows` | 换成 `pathRows(rows, leaf_seq, migrated)` |
| `store.Rewind` | `DELETE … seq >= ?` + 收缩影子集合 | **删除**（改成 `SetLeaf`）；影子集合不再需要收缩（没有行被删） |
| `store.AppendCheckpoint` | `surfaceRows(全表)` | `pathRows(leaf)` + 重接父指针（A.3 三件事） |
| `store.surfaceCounts` | 全表 − 影子（`List` / `ChildrenOf` 共用） | 由 `leaf_len` 列取代（两处 SELECT 都加列、删掉那次聚合查询）；**函数保留到批次 1 结束再删**（迁移回填不再用它，但删除前先确认没有第三个调用点） |
| `store.List` 的**可见性过滤** | `if meta.Messages > 0`（空白草稿不进侧栏） | ⚠️ **必须改成「表里有任意行」**（`EXISTS(SELECT 1 FROM messages WHERE session_id=…)` 或加一列 `has_rows`）。原因：撤回第一条消息后 `leaf_len = 0`（空路径是合法状态），而这条会话**仍有废弃分支在库里**——按 `leaf_len > 0` 过滤会让它从侧栏消失，用户再也切不回去（数据没丢但入口没了）。显示条数仍用 `leaf_len`（空路径显示「0 条」，与「已回到起点」一致） |
| `store.LastUserTurn` | 全表计数 + 取最后一条 user 正文（`workspace_sync` 的默认提交信息用） | **必须改按路径**：否则切到旧分支后，提交信息会取到废弃分支里那句用户消息（静默的错数据）。改法 = 先 `pathRows(leaf)` 再在 Go 侧数（调用点只有 3 处，都是低频路径） |
| `agent.Session.Rewind` | 内存截断 + 落库删行 | 改成 `SetLeaf`：重载路径 + 重算 context（**内存截断不再需要**——重载就是真相） |
| `agent.Session` 的 `Send` 前置检查 | 无 | 追加前无需改：`AppendMsg` 沿 leaf 前进 |
| `Session.History/HistoryBefore/TailHistory` | 基于 `s.history` | 不变（`s.history` 已经是路径） |
| `store.Search` | 搜全表 | **第一版不变**（能搜到废弃分支的内容）；二期加「结果标注分支」 |
| `store.ChildrenOf` / `sessions.parent_id` | 会话级父子（子 Agent） | 关系**不动**；但 `CreateChild` 签名扩 owner 两个参数（§E.4），`ChildrenOf` 的 SELECT 顺手带 `child_owner_*`（回滚对账要用） |
| `agent.openChildSession` | `CreateChild(parentID, agentID, dispatchID)` | 多传 `ownerID/ownerSeq`（新建与续跑都写，续跑按本次派发重写 owner_seq——§C.2.1） |
| `agent.runDispatch` / `tools.DispatchCall` | 只带 dispatch 参数 | 加 `DispatchSeq`（父会话那条 dispatch 消息的 seq），供子会话注入 owner；结果回带 `ExternalCount` |
| `store.SessionStatsOf` 的折叠 | 全表按 session_id | **不变**（统计仍按会话各算各的——子会话的 token 不算进父会话的消耗，拍板项 3 只谈「整段日志 vs 路径」，不涉及跨会话） |
| `server/session_ops.go` 的 `history()` | `sess.History()` 等 | 不变（`s.history` 已是路径）；只加 `LeafSeq` 字段 |
| `internal/server/dispatch_sessions.go` 的 resume | `EnablePersistence` → `Latest()` | `Latest()` 内部改走路径，**调用方零改动** |
| `SessionStatsOf` | 折叠整段日志 | **不变**：拍板项 3 定了「仍折叠整段日志」（切分支数字不变）；子会话的消耗仍算它自己那份（不跨会话累加——`chat.done` 各带各的 stats，见 AGENTS §2.4） |

---

## F. 分阶段实施计划（4 批，每批可独立验收）

**依赖关系与降级**（每批都能单独发布，不必等下一批）：

```
批次 1（存储）  ──→ 批次 2（leaf/协议）  ──→ 批次 4（前端树面板）
                              └─→ 批次 3（文件快照）──→ 批次 4 的「N 文件 / ⚠️ N」徽标
```

- 批次 3 若晚于批次 4：`TreeNode.Files` / `External` 恒 0（`omitempty` → 键缺席），
  前端据「键缺席」不渲染徽标（**不显示 0**——本仓库既有纪律：零值 = 未知，不编数字）；
  同时 `files:true` 不可用（回滚只动历史，UI 上「同时还原文件」开关隐藏而不是灰掉——
  藏起来是「这个版本没有这个能力」，灰掉是「你有但用不了」，后者会让人以为坏了）；
- 批次 2 若晚于批次 4：`chat.tree` 不存在 → 面板回落 `TurnPanel`（§D.7）；
- 批次 1 单独发布时**外部零变化**（协议、UI 全不动），是唯一一批可以「先合进去躺一段时间」的。

### 批次 1：数据模型 + 迁移 + 路径解析（**不改任何外部行为**）

- 改动：`store/store.go`（DDL + 迁移）、`store/messages.go`（`parentSeq` 读列、`pathRows`、
  `AppendMsg` 写 parent/leaf、`AppendCheckpoint` 走路径、`Load`/`Latest` 走路径）、
  `store/sessions.go`（`leaf_len`、`SetLeaf` 的 store 层、`List`/`ChildrenOf` 读列、
  **`child_owner_*` 两列 + `CreateChild` 扩 owner 参数 + `SetChildOwner`/`ChildOwner`**）、
  `chatmsg.Message.ParentSeq`（+ `llm/toolargs.go` 的 `sanitizeMessagesForWire` 清它）。
  `sessions.child_owner_*` 虽然服务于批次 3，但它是**幂等 ALTER + 一条 INSERT 参数**，
  放在批次 1 一起做（省一次迁移版本号；且 `CreateChild` 的签名改动只影响一处调用）。
- **不改**：`agent/`、`protocol/`、`server/`、`frontend/` —— 这一批**零外部行为变化**，
  唯一的可观察差异是侧栏条数改为读列（数值必须与今天完全相同，用对照测试钉住）。
- **验收**：
  - `go test ./internal/store` 全绿（现有 messages/checkpoint/rewind 测试**不改一行**：
    批次 1 里 `Store.Rewind` 保持旧语义（真删行）不动，批次 2 才删它——单链会话下
    「leaf = MAX(seq)」，删行后 leaf 恰好仍是 MAX(seq)，两者不冲突）；
  - `TestMigrationChainBackfill`（老库升级后路径逐字节等价）；
  - `TestPathSurfaceMatchesSurfaceRowsOnLinearHistory`（含多检查点、中间段影子）；
  - `TestPathSurfaceSkipsAbandonedCheckpoint` / `TestPathSurfaceLeafOnCheckpoint`；
  - `TestBranchCompactionDoesNotLeak`（A 分支压缩后切回 B 分支，B 的原文全部可见）；
  - `TestPathSeqMonotonic`（`pathRows` 返回值 seq 严格递增——C.5 的切段前提）；
  - `TestLeafLenMatchesPathLen`（append/compact/setLeaf 随机序列下 `leaf_len` 恒等于路径长度）；
  - `TestCheckpointRewiresParent`（压缩后后继行的 `parent_seq` 指向检查点，且路径逐字节不变）；
  - `TestLeafLenMatchesSurfaceCounts`（迁移/写入后 `leaf_len` 与**旧** `surfaceCounts()` 数值相同
    ——在批次 1 里两个实现并存，这条对照测试是删掉旧实现的放行条件）；
  - `TestEmptyPathSessionStaysVisible`（`leaf_len = 0` 但库里有行 → 仍在 `List` 里，
    条数显示 0——空路径是合法状态，不是「会话坏了」）；
  - `TestChildOwnerPersisted`（`CreateChild(…, ownerID, ownerSeq)` 落库、`ChildOwner` 读回；
    续跑时 `SetChildOwner` 重写 owner_seq 而 owner_id 不变）。
- 风险：最低（纯存储层，外部协议与 UI 零变化）。**可以单独合并发布。**

### 批次 1.5（可选，与批次 1 同批）：`readRows` / `readRowsTx` 列对齐

- 改动：把 `readRows` 多出来的 `images` / `attachments` 两列（以及它们的 `rows.Scan` 目标）
  补进 `readRowsTx`（§A.5 的既有漂移）。
- 验收：`go test ./internal/store` 全绿（这条改动不改变任何行为，只是让两个 SELECT 真的逐字一致）。
- 为什么单列出来：它不属于本设计，但**加 `parent_seq` 时两处都要改**——顺手对齐比以后再踩一次便宜。

### 批次 2：leaf/分支运行时 + 协议

- 改动：`agent/rewind.go`（改 `SetLeaf`）、`agent/session.go`（重载路径 + context 重算）、
  `protocol/{chat,methods}.go`、`server/dispatch_chat.go`（`chat.setLeaf` / `chat.tree` /
  `chat.rewind` 新语义）、`server/emit.go`（`chat.leafChanged`）、`server/session_ops.go`（`history` 带 `leaf_seq`）。
- **验收**：
  - `TestRewindRemovedCountUnchanged`（`removed` 数值与旧删除语义逐例相同）；
  - `TestSetLeafCreatesBranch`（切到 A3 再发消息 → A3 有两个子节点，旧分支仍在）；
  - `TestBranchHistoryIsLeafPath`（切回旧分支，`chat.history` = 旧路径）；
  - `TestChatTreeShape`（节点/父子/on_path/children 计数）；
  - `TestRewindCompatOldClient`（按老客户端的时序：rewound 事件 → planRewind 截断 → history 一致）；
  - `TestSetLeafRejectsUnbalancedCut`（切到 tool_calls 与结果之间 → 拒绝）；
  - `TestSetLeafRecomputesContextFromPath`（不沿用旧路径的落库测量）；
  - `TestStatsUnchangedByBranchSwitch`（**拍板项 3**：切分支前后 `SessionStatsOf` 完全一致——
    折叠的是整段日志，与 leaf 无关）；
  - 手工：真机壳里撤回、切分支、再切回。

### 批次 3：文件快照

- 改动：`store/snapshots.go`（新表读写 + blob 存取 + GC + **owner 归属查询**）、
  `store/sessions.go`（`child_owner_*` 两列 + `CreateChild` 签名扩 owner + `SetChildOwner`/`ChildOwner`）、
  `tools/{snapshot,edit,file_write}.go`（sink 接缝 + `WithSnapshotOwner`）、
  `agent/{turn,tools,dispatch}.go`（注入 sink/锚点/owner、`openChildSession` 传 owner、
  dispatch 结果回带子会话的 `ExternalCount`）、`protocol`（`FileRestoreResult` 含 session_id/child_sessions）、
  `server`（回滚编排：一次 owner 查询 + 跨会话合并 plan）、`agent/rewind.go`（`files` 参数）。
- **验收**（★ = 本次拍板新增的跨会话用例，必须有）：
  - `TestSnapshotEarliestBeforeWins`（同一文件 seq 5/10 两次改，回滚到 8 之前 → 内容 = seq5 的 before）；
  - `TestSnapshotCreateThenRestoreDeletesFile`（新建文件回滚 → 删掉）；
  - `TestSnapshotDedupByHash`（两次相同内容只存一份 blob）；
  - `TestRewindFilesFailClosed`（磁盘被手改 → 冲突清单 + leaf 不动 + 文件不动）；
  - `TestRewindFilesConflictKeepsLeaf`（冲突时 `removed=0` + `leaf_seq` 不变 → 前端据此回滚乐观截断）；
  - `TestSnapshotSkipsBinaryAndHuge`（如实报 skipped）；
  - `TestBlobGC`（孤儿 blob 被回收、被引用的不动）；
  - `TestRestoreIsIdempotent`（对同一个切点回滚两次：第二次 `hash == expected && restore == expected`
    → plan 收敛为空操作，不报错、不动文件）；
  - ★ `TestParentRewindRestoresChildFiles`（父会话第 44 轮派子会话改 A，锚点 = 第 43 轮 user 消息
    → A 还原；**子会话 `chat.history` 一条不少、leaf 未变**）；
  - ★ `TestChildResumeUsesDispatchTurn`（边界①：第 42 轮派发改 A、第 45 轮续跑改 B，
    锚点 = 第 45 轮 user 消息 → 只还原 B（owner_seq=45），A（owner_seq=42）不动）；
  - ★ `TestChildCreatedAfterAnchorFullyReverts`（边界②：锚点之后才创建的子会话，
    它新建的文件被删、改过的文件被还原）；
  - ★ `TestParentChildSamePathChain`（父会话与子会话改**同一个文件**：取最早 before / 最新 after，
    冲突基准是最后那个 after）；
  - ★ `TestChildFileConflictFailsClosed`（子会话改的文件被用户手改 → 同样报冲突、整批不执行）；
  - ★ `TestNestedChildOwnerInherits`（多层嵌套：孙会话的写仍归到顶层 owner_seq——
    今天深度恒 1，用直接构造 sessions 行来测）；
  - ★ `TestChildBashCountPropagates`（子会话里跑 `bash` 改文件 → 父会话那一轮 `external++`）；
  - ★ `TestSetLeafFilesCutIsExclusive`（`setLeaf(seq, files:true)` 还原 `owner_seq > seq`：
    第 42 轮自己改的文件**不被还原**——差一个等号就会「刚切过去文件就没了」）；
  - ★ `TestRewindFilesCutIsInclusive`（`rewind(anchor, files:true)` 还原 `owner_seq >= anchor`：
    锚点那条自己的改动也被还原，因为它离开了路径）；
  - 真链路：让 agent 改三个文件（其中一次经子 Agent）→ 撤回带 files → `git diff` 为空。

### 批次 4：前端树面板

- 改动：`shared/types.ts`（`AgentSource.setLeaf/tree`、`RewindOutcome` 扩展）、
  `agent/ws/index.ts` + `agent/demo/index.ts`（实现/降级）、
  `app-hooks/use-session-tree.ts`（新）、`app-hooks/use-turn-outline.ts`（加 `seqToUid`）、
  `components/panels/{SessionTreePanel,tree-model,TreeNodeRow}.tsx/ts`、
  `styles/panels.css`（`.stree-*`）、`App.tsx`（右栏接线）、文案（「撤回」→「回到这里」）。
- **别忘了 `agent/ws/connection.ts` 的 `HEAVY_METHODS`**：`chat.tree`（大会话要扫全表骨架）与
  `chat.setLeaf`（含文件回滚时是磁盘 IO）都该进宽容超时集合（现有集合里已有 `chat.rewind`，
  它的实现改成 setLeaf 后负载只会更重，10s 快判会误伤）。
- **验收**：
  - `frontend/tests/tree-model.test.mjs`（缩进深度、折叠、过滤保留祖先、分支标记、off_path 灰化、
    `child_files` 徽标文案）；
  - `frontend/tests/rewind-confirm.test.mjs`（**拍板项 6**：二次确认三段文案的三个数字
    来自 plan；`bash` 处数 > 0 时第二段必须出现）；
  - 手工：单链会话（树视图与旧列表视觉接近）、三分支会话、废弃分支灰化不隐藏、过滤模式、
    单击跳转 vs 切分支、切分支后时间线/上下文指示器/侧栏条数同步；
  - 手工（**跨会话回滚**）：父会话派子 Agent 改两个文件 + 新建一个 → 点「回到这里」并开
    「同时还原文件」→ 二次确认里看到「含 N 个子 Agent 的改动」→ 确认后文件回到目标状态、
    子会话的历史与 leaf 完全不变（点开派发卡进子会话视图核对）；
  - 老后端（无 `chat.tree`）→ 面板回落现有 `TurnPanel`（不报错、不空屏）。

---

## G. 需要用户拍板的清单

### G.0 已拍板（2026-12，本文按这四条写）

| # | 取舍点 | **拍板结果** |
|---|---|---|
| 2 | `chat.rewind` 兼容策略 | **保留名字、语义改为「移 leaf」**（不新增 `chat.branch`）。老客户端的可观察行为不变，`removed` 口径保持「离开路径的条数」（§B.1） |
| 3 | 会话统计口径（`SessionStats`） | **仍折叠整段日志**（含废弃分支）：切分支时数字不变——「没删就不该变」，与现有 DSH 口径一致。二期可加并列的 `PathStats` |
| 6 | `bash` 改文件不可回滚的呈现 | **UI 明确标 ⚠️ + 回滚前二次确认**：确认文案列出「N 处改动无法还原」，不静默尽力回滚、不假装回滚干净（§C.1、§H#1） |
| 12 | 子会话的文件改动是否纳入父会话回滚 | **纳入**（原推荐「不纳入」是错的，已按用户拍板修正）：父会话回滚时**递归还原父子全部文件改动**；子会话对话历史不动（§C.2.1、§C.5、§C.5.1） |

下面 15 条是其余细节，推荐值可直接采用（若要改，按表内选项说明调整）。

| # | 取舍点 | 选项 | 我的推荐 | 影响面 |
|---|---|---|---|---|
| 1 | 消息级父指针命名 | `parent_seq` / `parent_id` | **`parent_seq`**（与 `seq` 同域、与 `sessions.parent_id` 一眼分开） | 存储 + 协议键名 |
| 2 | `chat.rewind` 兼容策略 | ① 保留名、语义改分支 ② 新增 `chat.branch`、`rewind` 保持删除 | **①（已拍板）**（② 会导致存储上「删了」与「还在」自相矛盾；且 `removed` 数值口径可保持兼容） | 协议 + 老客户端 |
| 3 | **会话统计口径**（`SessionStats`） | ① 仍折叠整段日志（含废弃分支，切分支数字不变） ② 只折叠当前路径（切分支数字变小） | **①（已拍板）**（「没删就不该变」与现有 DSH 口径一致；② 会引入「同一个会话数字忽大忽小」的新困惑）。二期可加 `PathStats` 并列显示 | 输入框统计胶囊 |
| 4 | blob 存储 | ① 磁盘文件（内容寻址） ② SQLite 表 | **①**（与 attachments 先例一致；不放大「全表读含正文」的既有痛点） | 存储布局 + 备份脚本 |
| 5 | 快照覆盖范围 | ① 只 `edit`/`write_file` ② 加 `workspace_publish` | **①**（v1），`publish` 二期 | 回滚完整度 |
| 6 | `bash` 改文件不可回滚的呈现 | ① UI 明确标 ⚠️ + 回滚前二次确认 ② 静默尽力回滚 | **①（已拍板）**（宁可承认边界，也不让用户以为回滚干净了）。二次确认文案必须带具体数字：`将还原 X 个文件，另有 N 处改动（bash/子 Agent 的 bash）无法还原` | UI 文案 + 回滚流程 |
| 7 | 快照上限与保留 | 单文件 1MB / 单会话 256MB / 全库 2GB / 30 天 | 同左（可配，二期设置页） | 磁盘占用 |
| 8 | 冲突处理 | ① 整批拒绝（fail-closed） ② 部分还原 + 冲突清单 | **①**（部分还原会造成「一半旧一半新」的更难排查状态） | 回滚流程 |
| 9 | 树面板手势 | ① 单击跳转（当前路径）/ 切分支并跳转（废弃分支）+ 行尾按钮 ② 双击切 leaf | **①**（可发现、键盘可达、触摸可用） | 前端交互 |
| 10 | 树节点粒度 | ① 以「轮」为节点（assistant/tool 折叠） ② 每条消息一个节点 | **①**（compact 载荷小 10 倍；轮才是用户的思考单位） | 前端 + 载荷 |
| 11 | `chat.tree` 载荷 | ① 默认 compact + `max_nodes=2000` ② 默认全量 | **①** | 大会话性能 |
| 12 | 子会话的文件改动是否纳入父会话回滚 | ① 不纳入，仅提示 ② 纳入（跨 session_id 聚合） | ~~①~~ **已拍板 ②**（用户 2026-12 改判：工作区是父子共享的单一状态，只回父会话的文件等于「历史回去了、工作区没回去」。归属靠 `owner_session_id`/`owner_seq` 摊平，不靠回滚时递归，见 §C.2.1） | 回滚语义 |
| 13 | 侧栏「消息数」的口径 | ① 当前路径条数（`leaf_len`） ② 全库行数 | **①**（用户看到的历史就是路径；但**可见性**判据必须用「有任意行」——见 §E.5，否则空路径的会话会从侧栏消失） | 侧栏 |
| 14 | 压缩能否跨分支复用 | ① 不能（分支局部，影子集合必须全在路径上） ② 能（摘要挂到公共祖先） | **①**（② 需要重写摘要语义，且「哪条分支的摘要」无从判定） | 压缩实现 |
| 15 | 「撤回」文案 | ① 改成「回到这里」 ② 保留「撤回」但加说明 | **①**（「撤回」在分支模型下是误导） | 全站文案 |
| 16 | 回滚崩溃自愈（意图日志） | ① 做（启动时补做 leaf 移动） ② 不做（只给重试入口） | **①**（几十行代码换掉一整类「文件对了、历史没切」的诡异态） | 回滚流程 |
| 17 | `chat.setLeaf` 是否返回 `messages` | ① 返回（省一次往返） ② 恒不返回（客户端自己拉） | **②**（一个方法只做一件事；`chat.history` 已有分页/统计/树的全套参数，重载走它才不会漏字段） | 协议 |
| 18 | `files:true` 冲突时前端如何回滚乐观截断 | ① 不乐观（等应答） ② 应答 `removed=0` → 重拉历史对齐（复用现有失败路径） | **②**（手感统一；①会让带文件与不带文件两种撤回"卡"得不一样） | 前端时序 |
| 19 | 快照的二进制判定复用 `isBinary` | ① 复用（导出成包内共用） ② 另写一份 | **①**（同一份启发式不写第二遍） | 代码组织 |

---

## H. 风险与边界

| # | 风险 | 后果 | 缓解 |
|---|---|---|---|
| 1 | **`bash` 改文件不可回滚**（含子 Agent 里的 bash） | 回滚后文件仍是 bash 改过的样子 | **已拍板**：UI 标 ⚠️ 外部操作数 + 回滚前**二次确认**，文案列出「有 N 处改动无法还原」+ 建议项目用 git 兜底。子会话的 bash 计数经 dispatch 结果上抛到父会话那一轮（§C.1）。<br>**二次确认的三段结构**（固定文案骨架，前端只填数字）：<br>①「将把对话回到第 N 轮之前，并还原 **X** 个文件（含 **Y** 个子 Agent 的改动，涉及 Z 个子会话）」<br>②「另有 **M** 处改动**无法还原**（bash / MCP / 自定义工具）——这些文件会保持现状」<br>③「子会话的对话历史**不会**回滚，它们仍可继续」 |
| 2 | 用户手改文件（编辑器/构建） | 回滚覆盖用户改动 = 数据丢失 | `after_hash` 冲突检测，**一律不覆盖**，整批拒绝 + 冲突清单 |
| 3 | blob 与 DB 不一致 | 孤儿 blob（浪费磁盘）/ 缺失 blob（回滚失败） | 孤儿由 GC 回收；缺失时 fail-closed（`FileSkip` 如实报，不假装成功） |
| 4 | 大文件/二进制不抓 | 回滚不到这些文件 | 写入时就记 `skipped`，回滚结果里如实列出 |
| 5 | 老客户端认知差（「撤回」不再删除） | 用户以为数据没了 | `removed` 数值兼容 + 新客户端文案改「回到这里」+ 树面板给找回入口 |
| 6 | 大会话 `chat.tree` 载荷 | 慢链路卡顿 | compact 投影（不读正文）+ `max_nodes` + `truncated` 明示 |
| 7 | 分支多份摘要 | 存储略增 | 每份 1~4KB，压缩本来就不频繁，可接受 |
| 8 | 统计口径随分支增长（拍板项 3） | 用户困惑「没干活怎么涨了」 | **已拍板 ①（折叠整段日志）**：在统计弹层的口径说明里写清「含全部分支的历史」。<br>⚠️ 连带的口径变化必须一起改：`docs/session-stats.md` 与 `SessionStats` 的注释现在写着「撤回真删了行，数字跟着变小」——**撤回不再删行之后这句话不成立**（`chat.rewound` 仍会重算 stats，但重算结果与之前相同）。实现时把那条注释与 UI 说明一起改成「统计折叠整段日志，切分支/回到这里都不改变它」 |
| 9 | 切 leaf 与子会话并发 | 子会话正在写文件时父会话切分支 | 父会话 busy（dispatch 在跑）= 天然被 `ErrBusy` 拒绝（`runDispatch` 在 `runTools` 阶段二同步等结果，父会话整个 dispatch 期间是 busy）。**子会话的写已纳入回滚范围**（拍板项 12）：它写的时候父会话必然 busy，所以不存在「回滚与子会话写并发」的窗口 |
| 10 | 回滚过程崩溃 | 文件已还原、leaf 未动 | 顺序固定（文件→DB）+ 意图日志（C.5，启动时自动补做 ⑥）；仍失败则给 `files:false` 重试入口，文档明示非原子 |
| 11 | `readRows` 全表读在超大库上的成本 | 切分支/回放变慢 | 与今天同量级（多一次 map 构建）；批次 1 的等价性测试里带一个 53k 行的基准 |
| 12 | 迁移回填的 `parent_seq` 写错 | 老会话打不开或历史错乱 | 整体一个事务 + `PRAGMA user_version` 幂等（A.2），失败则 `Open` 报错而不是半迁移；`TestMigrationChainBackfill` 逐字节比对；`pathRows` 对环/断链钳住不抛异常（畸形数据不炸加载路径） |
| 13 | **快照的锚点 seq 与实际写顺序错位** | 回滚到错误的文件状态 | 锚点取「携带 tool_call 的 assistant 消息 seq」——同一轮里多个工具调用共享一个锚点，按 `owner_seq` 切段时它们整批进/整批出（**不会出现半轮**）；子会话的整轮写同样共享「父会话那条 dispatch 消息」的 owner_seq（也不会半轮）。单测：一轮内先 edit A 再 edit B，回滚该轮 → 两个文件都还原 |
| 14 | **同一文件被 bash 与 edit 交替改** | 冲突检测会拒（bash 那次没记 after_hash） | 这正是「bash 改动无法还原」的具体表现：回滚报冲突并列出该文件，用户可选择 `files:false` 只切历史，或手动处理。**不做启发式猜测**（猜「大概是 bash 改的」然后覆盖 = 数据丢失） |
| 15 | **空路径会话从侧栏消失** | 用户撤回到起点后再也找不到那条会话（数据在库里，入口没了） | 可见性判据用「表里有任意行」而不是 `leaf_len > 0`（§E.5 的 `List` 行）；单测 `TestEmptyPathSessionStaysVisible` |
| 16 | **跨会话回滚误伤「别的账」** | 子会话在第 45 轮被续跑时改的文件，被回滚到第 43 轮时一起还原（多还原） | 归属在**每次派发**时重算（§C.2.1 的续跑行），并用 ctx 注入作权威；单测 `TestChildResumeUsesDispatchTurn` 钉住 |
| 17 | **子会话 workDir 与父会话不一致时快照串根** | 相对路径指向不同的根，回滚写错文件 | 今天 `openChildSession` 恒继承父会话 workDir；设计把它写成不变式（§C.5），将来若放开独立工作区必须按 workDir 分组 |
| 18 | **用户以为「回滚也把子会话对话清掉了」** | 看到子 Agent 的历史还在，以为回滚失败 | 确认文案与结果面板都明说「子会话的对话不会回滚」（§C.5.1 边界②、§H#1 的三段式文案） |

---

## I. 附：一次完整流程（端到端）

```
用户在第 7 轮的某条消息上点「回到这里」（files 开关打开）
  ├─ 前端：本地乐观截断时间线（planRewind，现状行为不变；若后端回 removed=0
  │        （文件冲突、leaf 未动），前端重拉历史把截断回滚——见 §B.3 的时序坑）
  ├─ WS：chat.rewind {session_id, seq: <第7轮 user 消息>, files: true}
  ├─ 后端 agent.Rewind：
  │    ① busy? → ErrBusy（不猜）
  │    ② 配对平衡校验（现状保留）
  │    ②' 前端已拿到确认弹窗的三段文案（§H#1）；用户点了「确认回滚」才走到这里
  │    ③ 文件回滚 plan（**跨父子会话**，拍板项 12）：
  │         rows = SELECT … WHERE owner_session_id = 顶层id AND owner_seq >= anchor
  │         （一次索引查询拿到父会话 + 全部子会话在这一段的写；不递归 CTE）
  │         → 按 path 分组（跨会话混在一起）取 owner_seq 最早 before / 最新 after
  │         → 逐文件 hash 校验 → 有冲突则整批拒绝（leaf 不动、文件不动）
  │         → 无冲突则原子写/删，失败逆序回退
  │         → 结果按 session_id 分组回带（父会话改动 / 各子 Agent 改动）
  │    ④ store.SetLeaf(parent(anchor))：只改**顶层会话**的 leaf_seq/leaf_len（一条不删）
  │         —— 子会话的 leaf 一个都不动（对话历史不回滚）
  │    ⑤ st.Load(id) 重载内存历史（= pathRows(newLeaf)）
  │    ⑥ context 整份重估（Estimated=true，等下一轮真实 prompt_tokens 校正）
  ├─ 事件：chat.rewound（老客户端兼容）+ chat.leafChanged（新）+ session.changed("rewound")
  └─ 前端：树面板重拉 chat.tree（旧分支变灰、leaf 标记移到新位置）、
           时间线**不再重拉**（leafChanged 判重跳过——乐观截断已是终态，见 §B.3）、
           输入框回填被撤回的那条原文；rewound 广播的 context/stats 按新路径校正指示器
用户想回到旧分支：点树面板里灰掉的节点 → chat.setLeaf(旧 seq) → 时间线切回、文件不动
```

**跨会话回滚的一个具体例子**（这是拍板项 12 的验收场景）：

```
父会话（用户点「回到这里」的锚点 = 第 43 轮那条 user 消息）：
  seq 40  user「重构 auth」
  seq 41  assistant（tool_calls: edit internal/auth.go）
  seq 42  tool（结果）
  seq 43  user「再补个测试」              ← 锚点 anchor = 43
  seq 44  assistant（tool_calls: agent_dispatch → 子会话 S，dispatch_id=D）
  seq 45  tool（子会话结论）
  seq 46  assistant（tool_calls: edit internal/auth_test.go）
  leaf = 46

子会话 S（parent_id=父, dispatch_id=D, child_owner_seq=44）：
  改 internal/auth.go（第二次）、新建 internal/auth_helper.go

回滚：anchor = 43 → leaf = parent(43) = 42（seq 43 及其之后离开路径，但**一条不删**）
  快照查询 owner_session_id=父 AND owner_seq >= 43
    → 子会话那两条（owner_seq=44）✅ 纳入
    → 父会话 seq 46 那条（owner_seq=46）✅ 纳入
    → 父会话 seq 41 那条（owner_seq=41）❌ 不纳入（在锚点之前，用户没要撤它）
  按 path 合并（跨父子会话同一份链）：
    internal/auth.go        最早 before = 子会话那条的 before
                            （= 父会话 seq41 改完之后的内容）
                            → 还原成「子 Agent 第一次改之前」的样子 ✅
    internal/auth_helper.go 新建（before=''）→ 删掉 ✅
    internal/auth_test.go   父会话 seq46 的 before → 还原 ✅
  子会话 S 的对话：leaf 仍是它自己的，`chat.history` 一条不少，可继续续跑 ✅
  确认文案：将还原 3 个文件（含 1 个子 Agent 的 2 处改动，涉及 1 个子会话）；
            子会话的对话历史不会回滚
```

---

## J. 明确**不做**的事（避免范围蔓延）

| 不做 | 理由 |
|---|---|
| 分支的合并 / cherry-pick / rebase（把两条分支的历史缝起来） | 那是版本控制系统的活；会话树只负责「哪条历史是当前」，缝合会产生「同一条消息有两个父亲」这种模型外的东西 |
| 跨会话（父子会话之间）的**对话树** | 子会话是独立会话（`sessions.parent_id`），它的树是它自己的；不做跨会话聚合。**注意区分**：文件快照**是**跨会话聚合的（拍板项 12），对话历史**不是**——这两件事别混 |
| 自动为每条消息建文件快照 | 成本与收益都不成立（一条会话几百条消息、绝大多数不写文件）；只在写工具执行前抓 |
| `bash` 写文件的推断与补偿 | 不可靠（无法枚举副作用）；明确列为边界，UI 如实提示 |
| 树节点的拖拽重排 / 手工编辑历史 | 历史是事实记录，不提供改写入口（除了 leaf 指针） |
| 分支命名与备注 | 第一版靠轮次号 + 摘要识别；等有真实需求再加（避免又一张表） |

---

## K. 与既有纪律的一致性检查

- **单一事实源**：路径 = `leaf_seq` 回溯（唯一），检查点生效判定复用 `surfaceRows`（唯一），
  轮次分组复用 `turnGroups`/`outlineLabel`（唯一），seq→uid 映射单点构造；
- **未知就显示中性态**：`context` 未知时整键缺席、`stats` 零值不渲染、`skipped` 如实报；
  `leaf_seq=0` 是**空路径**（合法状态，UI 显示「这段对话已回到起点」而不是「加载失败」）；
- **fail-closed**：`AppendCheckpoint` 越界拒绝、文件回滚冲突整批拒绝、落库失败不动内存；
- **两条路径（live / replay）必须一致**：`chat.leafChanged` 与 `chat.history` 带同一份
  `leaf_seq`/`context`/`stats`；树的 `on_path` 由后端算，前端不自己推；
- **畸形数据不炸加载路径**：`pathRows` 对环/断链钳住，`parent_seq` 缺失回落 0，
  `chat.tree` 对孤儿/环标 `orphan` 而不是抛错；
- **判定只有一份**：路径回溯（`reachSet`）、配对平衡（`AnalyzeToolPairing`）、轮次编号、
  单行摘要（`outlineLabel`）、二进制嗅探（`isBinary`）都只有一处实现，迁移/回放/树/快照共用；
- **归属在写入时摊平，不在回滚时反推**（§C.2.1）：快照行带 `owner_session_id`/`owner_seq`，
  回滚只是一条索引查询；不靠 `parent_id` 递归 + `dispatch_id` join（那条路既慢又区分不了续跑）；
- **不新增依赖**：全部用现有 `atomicfile` / `crypto/sha256` / 标准库；
- **改存储先想「两条路径」**：`readRows` / `readRowsTx` 逐字一致、live 与 replay 同源、
  内存与库不各算一遍——本文的每一处改动都按这条检查过（§A.5、§E.4）。
