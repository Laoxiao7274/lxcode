// cert-allow.test.mjs —— certificate-error 自签放行判定的单测。
//
// 钉住的根因（2026-11 用户「公网入口一直正在重连」）：Electron 40 实测
// certificate-error 事件的 error 值带 `net::` 前缀，白名单存裸名——
// `Set.has()` 恒 false，放行条件从未命中。剥前缀后判定必须命中，
// 同时安全边界（回环不放行、非自签错误不放行、非法 URL 不放行）原样保住。
import test from "node:test";
import assert from "node:assert/strict";
import { isSelfSignedCertError, shouldAllowSelfSigned } from "../src/cert-allow.ts";

const TUNNEL = "https://frp-era.com:19211";

test("net:: 前缀的自签错误 + wss 非回环 → 放行（根因回归：事件值实测带前缀）", () => {
  assert.equal(
    shouldAllowSelfSigned("wss://frp-era.com:19211/rpc?token=x", "net::ERR_CERT_AUTHORITY_INVALID"),
    true,
  );
});

test("裸 ERR_CERT_COMMON_NAME_INVALID + https 非回环 → 放行", () => {
  assert.equal(
    shouldAllowSelfSigned(`${TUNNEL}/attachments/a.png`, "ERR_CERT_COMMON_NAME_INVALID"),
    true,
  );
});

test("自签错误 + 回环地址 → 不放行（本机后端永远是 ws:// 明文）", () => {
  assert.equal(shouldAllowSelfSigned("wss://127.0.0.1:7789/rpc", "net::ERR_CERT_AUTHORITY_INVALID"), false);
  assert.equal(shouldAllowSelfSigned("https://localhost:7789/x", "net::ERR_CERT_AUTHORITY_INVALID"), false);
  assert.equal(shouldAllowSelfSigned("https://[::1]:7789/x", "net::ERR_CERT_AUTHORITY_INVALID"), false);
});

test("过期证书（ERR_CERT_DATE_INVALID，不在白名单）→ 不放行", () => {
  assert.equal(shouldAllowSelfSigned("wss://frp-era.com:19211/rpc", "net::ERR_CERT_DATE_INVALID"), false);
});

test("非 wss/https 协议（app:// 主帧、ws:// 明文）→ 不放行", () => {
  assert.equal(shouldAllowSelfSigned("app://bundle/index.html", "net::ERR_CERT_AUTHORITY_INVALID"), false);
  assert.equal(shouldAllowSelfSigned("ws://192.168.1.105:7789/rpc", "net::ERR_CERT_AUTHORITY_INVALID"), false);
});

test("非法 URL → 不放行（不抛异常）", () => {
  assert.equal(shouldAllowSelfSigned("::not a url::", "net::ERR_CERT_AUTHORITY_INVALID"), false);
});

test("isSelfSignedCertError：前缀/裸名都命中，其余错误不命中", () => {
  assert.equal(isSelfSignedCertError("net::ERR_CERT_AUTHORITY_INVALID"), true);
  assert.equal(isSelfSignedCertError("ERR_CERT_COMMON_NAME_INVALID"), true);
  assert.equal(isSelfSignedCertError("net::ERR_CERT_WEAK_KEY"), false);
});
