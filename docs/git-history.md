# 提交与推送：本地提交、公开按版本聚合

> 2026-10-19 用户拍板；**2026-10 修订：轮末自动检查点提交已移除**（用户拍板先砍掉
> 自动提交，回滚方案后期重新设计）。本文是**提交格式**与**推送聚合机制**的唯一契约。
> 涉及代码：`scripts/push-release.mjs`（聚合推送 + 一次性清理）、
> `internal/project/commit.go`（`CommitAll`，workspace_sync 的提交原语）、
> `internal/server/workspace.go`（workspace_sync / workspace_rollback）。
>
> **自动提交已移除**：`internal/server/autocommit.go`（轮末触发）与
> `internal/project/checkpoint.go`（检查点信息规则）已删除；提交只发生在用户或模型
> 显式要求时（`workspace_sync`），改动否则一直留在工作区。

## 1. 为什么这样分

**现在**：一轮对话结束不再自动提交——改动留在工作区，直到用户说「提交/推送」
（`workspace_sync`，`git add -A` + commit，可选 push）。

**历史**：2026-10-19 起每轮结束会自动落一个检查点提交（一轮一个），作为本地回滚
粒度。这些历史提交**保留在 master 里不改写**，所以下面的格式与聚合机制照旧适用：
公开仓库不能是几百条「第 N 轮：…」，历史因此按版本聚合发布。

所以两条线分工：

| 线 | 内容 | 去向 |
|---|---|---|
| 本地 `master` | 完整历史（存量逐轮检查点 + 手工/workspace_sync 提交） | **永不直接推送**；回滚靠已提交的位置 |
| 发布线（本地 `release` / 远端 `master`） | 每次发版**一个**快照提交 `release: <version>` | 公开仓库看到的唯一历史 |

发布线是**独立的一条线**：每个发布提交的树 = 当时 master 的树，父提交 = 上一个发布
提交。因此本地 master 一根毫毛都不动（公开历史干净），推送是普通快进（不需要 force）。

## 2. 历史检查点提交格式（只对存量历史生效）

> 新提交不再由系统生成这个格式：`workspace_sync` 的默认信息是「最近一条用户消息首行」
> （可用 `message` 参数显式指定）。下面描述的是 master 里已有的历史检查点，脚本仍需
> 识别它们。

```
feat(server): 子会话确认门代理

[checkpoint] 第 137 轮

用户原话：
> 子会话的确认请求要能交给父会话裁决

改动：3 个文件（+120/-8）
- internal/server/dispatch.go (+96/-4)
- internal/server/autocommit.go (+24/-4)
- internal/project/checkpoint.go (+0/-0)
```

规则（原 `project.CheckpointMessage`，已随自动提交一并删除；仅存量历史保留这种形态）：

- **type**：用户意图关键词优先（`fix`/`docs`/`test`/`refactor`/`perf`/`chore`），
  都不命中时看改动文件（只动 `*.md`/`docs/` → `docs`，只动测试 → `test`），否则 `feat`；
- **scope**：改动文件最多的顶层目录（`internal`/`frontend`/`shell`/`android`/`site`/…），
  全在仓库根时省略括号；
- **摘要**：用户消息首行，去掉「然后/帮我/请…」等开头与行尾标点，截到 50 字符；
- **正文**：`[checkpoint] 第 N 轮` + 用户原话引用（限 6 行 / 500 字符）+ 改动统计
  （文件数、总增删、前 8 个文件逐条）。

`[checkpoint]` 标记是聚合推送的**识别依据**（`classifyCommit`）：老提交没有它，脚本
用「标题以 `第 N 轮` 开头」兼容识别，所以**历史不需要重写**。

## 3. 常规发版

```bash
node scripts/push-release.mjs            # 版本号取 shell/package.json
node scripts/push-release.mjs 0.2.19     # 显式版本
node scripts/push-release.mjs --dry-run  # 只打印将生成的提交（不动 ref、不推）
node scripts/push-release.mjs --no-push  # 更新本地 release，不推
```

流程：工作区必须干净 → 读远端发布线末端（`origin/master`）→ 从发布提交正文的
`master-commit:` 尾注（缺失时按树相等在 master 历史里回溯）定位「上次发布对应的
master 提交」→ 收集它到 HEAD 的提交 → 生成一个发布提交（树 = HEAD 的树）→ 更新本地
`release` → `git push origin release:master`。

- **幂等**：树与上次发布一致时直接退出（「没有新内容」），不产生空转的发布提交；
- **快进**：发布提交的父就是上一个发布提交，普通 push 即可；远端历史不是发布线
  （还是旧检查点历史）时脚本拒绝并提示先跑 `--bootstrap`；
- 发布说明按 type 分组（`feat` 在前），同一 scope+摘要的重复检查点只列一次，
  手工提交单列一节（上限 15 条）。

## 4. 一次性清理存量历史（`--bootstrap`，破坏性）

```bash
node scripts/push-release.mjs --bootstrap --dry-run   # 先看会生成什么
node scripts/push-release.mjs --bootstrap
```

做三件事：

1. **备份**（强制、无开关）：`temp/git-backup-<时间戳>.bundle`（`git bundle --all`，
   含所有分支，`git bundle verify` 可校验）；加 `--push-backup` 才额外把 master 推成
   远端 `backup/pre-cleanup-<时间戳>` 分支（**默认不推**——远端是公开仓库，备份分支会
   让检查点历史继续公开可查）；
2. **重写**：按时间顺序取历史所有 `release:` 提交，各建一个快照提交（保留原提交
   日期），末端接当前 master 的树作为本次版本——公开历史 = 初始提交 + 各发布版本；
3. **force push** 到远端 `master`；**本地 master 不动**。

回滚远端：把本地 master 推成临时分支再 force 到 master
（`git push origin <本地master>:refs/heads/backup/x` + `git push --force origin backup/x:master`）；
恢复本地全量：`git clone temp/git-backup-<时间戳>.bundle <目录>`。

## 5. 边界

- 发布线**不是** master 的祖先（快照线），所以不要试图 `git merge release` 或
  `git rebase` 它；它只用来给公开仓库提供版本历史；
- **回滚粒度**：轮末自动检查点已移除，所以 `workspace_rollback` 的 `last-turn` 退化为
  「上一个提交」——只有经 `workspace_sync` 或用户手工提交落过的位置能回滚，未提交的
  轮次改动不在回滚范围内（回滚方案后期重新设计）；
- `workspace_sync` 工具里的「直接推送当前分支」是**给用户自己的项目**用的（不是本仓库
  的发版通道）——本仓库的推送已被工具层闸门拦下：仓库根同时有 `scripts/push-release.mjs`
  与 `docs/git-history.md` 时（`project.IsSelfRepo`），`push=true` 一律拒绝并返回发版指引，
  本地提交照常（`push=false` 不受影响）。发版一律走 `scripts/push-release.mjs`；绕过闸门
  手工 `git push origin master` 仍会把历史检查点推出去——提示词里有配套的硬纪律（不得直接
  执行 git push）；
- 脚本自身测试：`node --test scripts/push-release.test.mjs`（分类、说明组装、尾注解析、
  树相同判定都是纯函数）。
