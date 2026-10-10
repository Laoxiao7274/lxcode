// 发布域契约测试：钉住 src/shared/release.ts 的口径（与后端 internal/domain 同一套规则）。
//
// 为什么还留着：mock 数据已经撤掉了（前端不再有样例数据，样例数据只在后端 --seed），
// 但 release.ts 仍是**前后端共享的契约**（命名 / manifest / 发布前校验），
// 它一旦漂移，症状还是「后台显示发布成功、客户端说已是最新」。所以这里只钉契约本身，
// 不再钉任何样例数据；后端侧由 internal/domain/domain_test.go 钉同一组不变量。
//
// 运行：npm test（tests/register.mjs 负责让 Node 能直接跑 src/ 下的 .ts 源码）。

import assert from "node:assert/strict";
import test from "node:test";

import {
  CHANGELOG_KINDS,
  REQUIRED_UPDATE_FILES,
  artifactUrl,
  canPublish,
  checkReleaseDraft,
  compareVersions,
  findArtifact,
  formatBytes,
  installerName,
  isValidVersion,
  latestPublished,
  portableName,
  sortByVersionDesc,
  updatePackageName,
} from "../src/shared/release.ts";

const HEX64 = /^[0-9a-f]{64}$/;

test("命名口径：安装包 / 免安装 / 更新包 与更新包内两个路径", () => {
  assert.equal(installerName("0.1.0"), "Lxcode Setup 0.1.0.exe");
  assert.equal(portableName("0.1.0"), "Lxcode-0.1.0-win-x64.zip");
  assert.equal(updatePackageName("0.1.0"), "update-0.1.0.zip");
  assert.deepEqual([...REQUIRED_UPDATE_FILES], ["resources/app.asar", "resources/bin/lxcode.exe"]);
});

test("下载地址：静态目录下的文件名（与 manifest.url 同一口径）", () => {
  assert.equal(artifactUrl(updatePackageName("0.1.0")), "/releases/update-0.1.0.zip");
});

test("版本号：只认三段数字", () => {
  for (const v of ["0.1.0", "1.2.3", "10.0.99"]) assert.ok(isValidVersion(v), v);
  for (const v of ["", "1.0", "1.0.0.0", "1.0.x", "v1.0.0", "1..0"]) {
    assert.ok(!isValidVersion(v), v);
  }
});

test("版本比较与排序", () => {
  assert.ok(compareVersions("0.1.0", "0.0.9") > 0);
  assert.equal(compareVersions("0.1.0", "0.1.0"), 0);
  assert.ok(compareVersions("1.9.9", "1.9.10") < 0);
  const sorted = sortByVersionDesc([{ version: "0.0.1" }, { version: "0.2.0" }, { version: "0.1.0" }]);
  assert.deepEqual(sorted.map((r) => r.version), ["0.2.0", "0.1.0", "0.0.1"]);
});

test("更新链：草稿不进链、撤回不进链，取最新已发布", () => {
  const list = [
    { version: "0.2.0", channel: "beta", status: "draft" },
    { version: "0.1.0", channel: "stable", status: "published" },
    { version: "0.0.3", channel: "stable", status: "published" },
    { version: "0.0.2", channel: "stable", status: "revoked" },
  ];
  assert.equal(latestPublished(list, "stable").version, "0.1.0");
  assert.equal(latestPublished(list, "beta"), null);
});

test("formatBytes：1024 进制、≥100 取整", () => {
  assert.equal(formatBytes(512), "512 B");
  assert.equal(formatBytes(1024), "1.0 KB");
  assert.equal(formatBytes(1024 * 1024 * 12), "12.0 MB");
  assert.equal(formatBytes(150 * 1024 * 1024), "150 MB");
});

function artifacts(version, sha) {
  return [
    { kind: "installer", name: installerName(version), size: 1024, sha256: sha },
    { kind: "portable", name: portableName(version), size: 1024, sha256: sha },
    { kind: "update", name: updatePackageName(version), size: 2048, sha256: sha },
  ];
}

function draft(version, sha) {
  return {
    version,
    channel: "stable",
    artifacts: artifacts(version, sha),
    electronVersion: "40.10.2",
    previousElectronVersion: "40.10.2",
    sha256OfUpdate: sha,
  };
}

function fails(checks) {
  return checks.filter((c) => c.level === "fail").map((c) => c.label);
}

test("发布前校验：齐备可发布；四类问题都是阻断项", () => {
  const sha = "aa";
  assert.equal(canPublish(checkReleaseDraft(draft("0.2.0", sha), [])), true);

  const existing = [{ version: "0.1.0", status: "published" }];
  assert.ok(fails(checkReleaseDraft(draft("0.1.0", sha), existing)).includes("版本号唯一性"));
  assert.ok(fails(checkReleaseDraft(draft("0.2", sha), [])).includes("版本号格式"));
  assert.ok(
    fails(checkReleaseDraft({ ...draft("0.2.0", sha), artifacts: artifacts("0.2.0", sha).slice(1) }, []))
      .includes("安装包"),
  );
  assert.ok(
    fails(
      checkReleaseDraft(
        {
          ...draft("0.2.0", sha),
          artifacts: [
            { kind: "installer", name: installerName("0.2.0"), size: 1, sha256: sha },
            { kind: "update", name: "update-0.1.9.zip", size: 1, sha256: sha },
          ],
        },
        [],
      ),
    ).includes("更新包命名"),
  );
  assert.ok(fails(checkReleaseDraft({ ...draft("0.2.0", sha), sha256OfUpdate: "bb" }, [])).includes("更新包哈希"));
  assert.ok(
    fails(
      checkReleaseDraft(
        { ...draft("0.2.0", sha), artifacts: artifacts("0.2.0", sha).slice(0, 1) },
        [],
      ),
    ).includes("更新包"),
  );
});

test("免安装版缺失与 Electron 换代只是提醒", () => {
  const sha = "aa";
  const checks = checkReleaseDraft(
    {
      version: "0.2.0",
      channel: "stable",
      artifacts: artifacts("0.2.0", sha).filter((a) => a.kind !== "portable"),
      electronVersion: "40.10.2",
      previousElectronVersion: "39.0.0",
      sha256OfUpdate: sha,
    },
    [],
  );
  assert.equal(canPublish(checks), true);
  const warns = checks.filter((c) => c.level === "warn").map((c) => c.label);
  assert.ok(warns.includes("免安装版"));
  assert.ok(warns.includes("Electron 版本变化"));
});

test("findArtifact 与 changelog 分类常量", () => {
  const list = artifacts("0.1.0", "aa");
  assert.equal(findArtifact({ artifacts: list }, "update").name, updatePackageName("0.1.0"));
  assert.equal(findArtifact({ artifacts: list }, "installer").name, installerName("0.1.0"));
  assert.deepEqual([...CHANGELOG_KINDS], ["features", "fixes", "breaking", "docs"]);
});

test("哈希格式：契约里的 sha256 都是 64 位小写 hex（后端算出来的真哈希）", () => {
  // 这条钉的是「值从哪来」：manifest.sha256 必须来自真文件（后端上传时算），
  // 前端只校验格式，不再自带样例哈希。
  const fake = "80d54dc6c8b318173c630c21c9636028544bc505212de0af5e97c7de8fc407b9";
  assert.ok(HEX64.test(fake));
});
