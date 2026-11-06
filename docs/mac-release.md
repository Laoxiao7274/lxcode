# mac 发版指南（site-backend）

mac 产物（`node scripts/build.mjs --mac` 在 mac 上产出）如何通过 site-backend
的 admin API 上传并对外托管。Windows 链路见 `_remote_release.sh`，本文件只讲 mac
侧的差异与操作顺序。

## 产物与契约

`scripts/build.mjs --mac`（macOS 上）产出，位于 `shell/release/`：

| 产物 | 说明 |
|---|---|
| `Lxcode-<version>-arm64.dmg` / `Lxcode-<version>-x64.dmg` | 安装包（electron-builder 默认命名；arm64/x64 各跑一轮 `--mac` / `--mac --x64`） |
| `update-mac-<version>.zip` | mac 更新包（zip 内 `resources/app.asar` + `resources/bin/lxcode`，`.app/Contents` 相对路径） |
| `manifest-mac.json` | mac 更新清单（本地校验用；对外清单由 site-backend 生成，字段同构） |

客户端契约（`shell/src/update-platform.ts`）：mac 客户端拉与 `manifest.json`
同目录的 `manifest-mac.json`，字段 `version/url/sha256/size/files` 与 Windows
完全同构；`url` 为相对文件名，按 manifest 所在目录解析（服务端固定输出
`releases/<file>` 产物路由）。

## 上传 API 字段

`POST /api/admin/releases` 与 `POST /api/admin/releases/check`（multipart），
认证同 Windows（`Authorization: Bearer $LXCODE_SITE_TOKEN` 或会话 cookie）。
mac 新增三个文件字段 + 一个元数据字段：

- `macInstallerArm64` → `Lxcode-<version>-arm64.dmg`（可选）
- `macInstallerX64` → `Lxcode-<version>-x64.dmg`（可选）
- `macUpdate` → `update-mac-<version>.zip`（发 mac 必填）
- `manifestMacSha256` → manifest-mac 的 `sha256` 声明值；**缺省时自动回落
  上传的 mac 更新包真哈希**（等价于「按实际产物出清单」），填了才做一致性核对

Windows 字段（`installer` / `update` / `manifestSha256` / portable 等）语义不变。

## check 校验清单（mac 段）

`manifestMacSha256` 非空（或带了 `macUpdate`）才启用 mac 校验，不阻断纯
Windows 发版：

- **mac 更新包缺失**（fail）· **命名不符**（必须叫 `update-mac-<version>.zip`，
  fail）· **哈希不符**（`manifestMacSha256` ≠ zip 真哈希，fail）
- **mac 安装包缺失**（warn，不阻断——dmg 只影响首次下载，不影响增量更新链）
- **dmg 命名不符**（warn）
- 上传（非 check）额外做**内容级复核**：mac 更新包 zip 内缺
  `resources/app.asar` 或 `resources/bin/lxcode` → 400（哈希核对对不了 zip 内
  缺路径，manifest-mac.files 会是坏的）
- publish 时对已落盘的 zip 再复核一次（409 拦下）

## 操作顺序（check → upload → publish → 验证）

```bash
# 0. 在 mac 上产出（每个架构一轮；先 --mac 默认 arm64，再 --mac --x64）
node scripts/build.mjs --mac && node scripts/build.mjs --mac --x64

# 1. dry-run（不落盘不入库；macFilesSource 应为 zip）
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -F "version=0.2.0" -F "channel=stable" -F "electronVersion=40.10.2" \
  -F "macInstallerArm64=@shell/release/Lxcode-0.2.0-arm64.dmg" \
  -F "macInstallerX64=@shell/release/Lxcode-0.2.0-x64.dmg" \
  -F "macUpdate=@shell/release/update-mac-0.2.0.zip" \
  https://<site>/api/admin/releases/check

# 2. 上传（draft）；同一 URL，去掉 /check
#    注意：若该版本已发过 Windows 产物，重传时必须把 Windows 文件也带上——
#    upload 是整体替换该版本的产物集合与清单
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -F "version=0.2.0" -F "channel=stable" -F "electronVersion=40.10.2" \
  -F "macInstallerArm64=@shell/release/Lxcode-0.2.0-arm64.dmg" \
  -F "macInstallerX64=@shell/release/Lxcode-0.2.0-x64.dmg" \
  -F "macUpdate=@shell/release/update-mac-0.2.0.zip" \
  https://<site>/api/admin/releases

# 3. publish（草稿 → 已发布；mac 清单齐备性在 publish 前再校验一次）
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"status":"publish"}' https://<site>/api/admin/releases/0.2.0/status

# 4. 验证：manifest-mac.json 契约 + 真文件哈希
curl -sS https://<site>/manifest-mac.json
#   → version/url/sha256/size/files；url 形如 releases/update-mac-0.2.0.zip
curl -sS -o /tmp/v.zip https://<site>/releases/update-mac-0.2.0.zip
shasum -a 256 /tmp/v.zip   # 应等于 manifest-mac.json 的 sha256
# Windows 契约回归确认（应与 mac 无关、不变）：
curl -sS https://<site>/manifest.json
```

一键脚本：仓库根的 `_remote_release_mac.sh`（与 `_remote_release.sh` 同模式，
经 SSH 在远程执行，token 从 `lxcode-site.service` 进程 environ 取）。

## 产物路由与状态

- 所有 mac 产物走 `GET /releases/<file>`；只有已发布版本的产物可取
  （草稿/撤回 404，与 Windows 一致）
- `GET /manifest-mac.json`：从最新已发布 stable 的 mac 更新包生成；**该版本
  没有 mac 产物时返回 404**（不发坏格式的 manifest——mac 客户端会把坏清单
  理解成「更新源指向了 Windows 的 manifest」，见 update-platform.ts 报错文案）
- dmg 的 Content-Type 为 `application/x-apple-diskimage`

## 数据形态

mac 清单随 release 落库（releases 表 `manifest_mac_*` 四列，可空；老库自动
ALTER 补列，老数据四列为 NULL = 没有 mac 清单，读侧按 404 处理）。产物 kind：
`mac-installer-arm64` / `mac-installer-x64` / `mac-update`（artifacts 表按
(version, kind) 存放，双架构 dmg 天然不冲突）。
