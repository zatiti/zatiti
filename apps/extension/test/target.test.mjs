// target.test.mjs -- T8.2 ferro-target decode and execution-identity
// comparison: full href and form-action identity (ADR 005), NFC
// name/text normalization, fail-closed decode errors. node:test, no npm
// dependencies.

import test from "node:test";
import assert from "node:assert/strict";
import {
  decodeTarget,
  identityMatches,
  normalizeText,
  normalizeUrl,
  TargetError,
} from "../lib/target.js";

const PAGE = "https://example.test/page";

function encodeTarget(payload) {
  const json = JSON.stringify(payload);
  const bytes = Buffer.from(json, "utf8");
  return "ferro-target:" + bytes.toString("base64url");
}

test("a plain CSS selector decodes with no expected identity", () => {
  const decoded = decodeTarget("#submit-btn");
  assert.equal(decoded.selector, "#submit-btn");
  assert.equal(decoded.expected, null);
});

test("a ferro-target selector decodes to the selector and identity payload", () => {
  const raw = encodeTarget({
    selector: "#login",
    tag: "a",
    role: "",
    name: "Log in",
    text: "Log in",
    href: "https://example.test/session/new",
  });
  const decoded = decodeTarget(raw);
  assert.equal(decoded.selector, "#login");
  assert.equal(decoded.expected.tag, "a");
  assert.equal(decoded.expected.name, "Log in");
});

test("the vendored adapter's spelling (base64 with - and _) round-trips", () => {
  // The vendored mustFind replaces - with + and _ with / then pads;
  // decodeTarget must agree, including for payloads whose base64url
  // spelling needs padding.
  const payload = { selector: "form input", tag: "input", name: "Email address" };
  const json = JSON.stringify(payload);
  // Force a length that needs '=' padding: 13 bytes -> base64url without
  // padding, decodeTarget re-pads.
  const b64url = Buffer.from(json, "utf8").toString("base64url");
  const decoded = decodeTarget(`ferro-target:${b64url}`);
  assert.equal(decoded.selector, "form input");
  assert.equal(decoded.expected.tag, "input");
});

test("an empty or non-string target is invalid_target", () => {
  assert.throws(() => decodeTarget(""), (error) => error instanceof TargetError && error.code === "invalid_target");
  assert.throws(() => decodeTarget(null), (error) => error.code === "invalid_target");
});

test("a ferro-target payload with a broken base64 or non-object JSON is invalid_target", () => {
  assert.throws(() => decodeTarget("ferro-target:!!!not-base64!!!"), (error) => error.code === "invalid_target");
  const notObject = Buffer.from("[1,2]").toString("base64url");
  assert.throws(() => decodeTarget(`ferro-target:${notObject}`), (error) => error.code === "invalid_target");
});

test("a ferro-target payload without a selector is invalid_target (never a CSS fallback)", () => {
  const noSelector = Buffer.from(JSON.stringify({ tag: "button" })).toString("base64url");
  assert.throws(() => decodeTarget(`ferro-target:${noSelector}`), (error) => error.code === "invalid_target");
});

test("identity comparison passes on equal facts", () => {
  const expected = {
    tag: "a",
    role: "",
    name: "Log in",
    text: "Log in",
    href: "https://example.test/session/new",
  };
  const observed = {
    tag: "a",
    role: "",
    name: "Log in",
    text: "Log in",
    href: "https://example.test/session/new",
    formAction: "",
  };
  assert.equal(identityMatches(expected, observed, PAGE), true);
});

test("identity comparison fails when the full href differs (not just the path)", () => {
  // Same path, different host: the placeholder adapter compares only the
  // pathname and would pass this; the governed path must fail it.
  const expected = { tag: "a", name: "Log in", href: "https://example.test/session/new" };
  const observed = { tag: "a", name: "Log in", href: "https://evil.test/session/new" };
  assert.equal(identityMatches(expected, observed, PAGE), false);
});

test("identity comparison accepts a relative expected href resolved against the page", () => {
  const expected = { tag: "a", href: "/session/new" };
  const observed = { tag: "a", href: "https://example.test/session/new" };
  assert.equal(identityMatches(expected, observed, PAGE), true);
});

test("identity comparison fails when the form action differs", () => {
  const expected = {
    tag: "input",
    name: "Email address",
    form_action: "https://example.test/login",
  };
  const observed = {
    tag: "input",
    name: "Email address",
    formAction: "https://evil.test/login",
  };
  assert.equal(identityMatches(expected, observed, PAGE), false);
  const same = { ...observed, formAction: "https://example.test/login" };
  assert.equal(identityMatches(expected, same, PAGE), true);
});

test("identity comparison accepts a relative form action resolved against the page", () => {
  const expected = { tag: "input", form_action: "/login" };
  const observed = { tag: "input", formAction: "https://example.test/login" };
  assert.equal(identityMatches(expected, observed, PAGE), true);
});

test("identity comparison is exact for tags and roles", () => {
  const expected = { tag: "button", role: "button" };
  assert.equal(identityMatches(expected, { tag: "button", role: "button" }, PAGE), true);
  assert.equal(identityMatches(expected, { tag: "a", role: "button" }, PAGE), false);
  assert.equal(identityMatches(expected, { tag: "button", role: "tab" }, PAGE), false);
});

test("name and text normalize to NFC with whitespace collapsed", () => {
  assert.equal(normalizeText("  Log  in  "), "Log in");
  // NFC: the combining form and the precomposed form are equal.
  assert.equal(normalizeText("éclair"), normalizeText("éclair"));
  const expected = { tag: "button", name: "éclair  now" };
  assert.equal(identityMatches(expected, { tag: "button", name: "éclair now" }, PAGE), true);
});

test("input type and autocomplete identity compare when reviewed", () => {
  const expected = { tag: "input", input_type: "email", autocomplete: "email" };
  assert.equal(
    identityMatches(expected, { tag: "input", inputType: "email", autocomplete: "email" }, PAGE),
    true,
  );
  assert.equal(
    identityMatches(expected, { tag: "input", inputType: "text", autocomplete: "email" }, PAGE),
    false,
  );
});

test("fields absent from the expected payload are not compared", () => {
  // An older snapshot payload (href as a bare pathname, no form_action)
  // still pins everything it carried.
  const expected = { tag: "a", href: "/session/new" };
  const observed = {
    tag: "a",
    href: "https://example.test/session/new",
    formAction: "https://anything.test/x",
  };
  assert.equal(identityMatches(expected, observed, PAGE), true);
});

test("identity comparison fails against a missing or malformed observation", () => {
  assert.equal(identityMatches({ tag: "button" }, null, PAGE), false);
  assert.equal(identityMatches({ tag: "button" }, "button", PAGE), false);
});

test("normalizeUrl keeps an unparseable value raw so the exact comparison fails", () => {
  assert.equal(normalizeUrl("https://example.test/a", PAGE), "https://example.test/a");
  assert.equal(normalizeUrl("/b", PAGE), "https://example.test/b");
  assert.equal(normalizeUrl("", PAGE), "");
  // Without a base an unparseable href cannot resolve, so it stays raw --
  // which can only fail the exact comparison against a normalized URL.
  assert.equal(normalizeUrl("ht tp://bad"), "ht tp://bad");
});
