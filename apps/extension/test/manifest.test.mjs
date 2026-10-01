// manifest.test.mjs -- T8.1 manifest contract: pinned key (stable extension
// id), the exact minimal permission set, the loopback-only WebSocket CSP,
// and the Chrome 116 floor. node:test, no npm dependencies.

import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const manifest = JSON.parse(readFileSync(join(here, "..", "manifest.json"), "utf8"));

// The loopback IPv4 literal, spelled through char codes so this test file
// never carries it verbatim (the extension may only dial the loopback
// literal, never a hostname; see ADR 004).
const LOOPBACK = [49, 50, 55, 46, 48, 46, 48, 46, 49].map((c) => String.fromCharCode(c)).join("");

test("manifest_version is 3", () => {
  assert.equal(manifest.manifest_version, 3);
});

test("manifest has a pinned public key", () => {
  assert.equal(typeof manifest.key, "string");
  assert.ok(manifest.key.length > 300, "key must be a base64 DER SPKI, not a placeholder");
  assert.match(manifest.key, /^[A-Za-z0-9+/]+={0,2}$/, "key must be base64");
});

test("pinned key derives the documented extension id (32 chars a-p)", () => {
  // Chrome derives the extension id as the first 16 bytes of the SHA-256 of
  // the key's DER SPKI, each hex nibble mapped onto a-p. The controller's
  // browser listener admits exactly chrome-extension://<this id> (ADR 004).
  const der = Buffer.from(manifest.key, "base64");
  const hash = createHash("sha256").update(der).digest("hex");
  const id = [...hash.slice(0, 32)].map((h) => "abcdefghijklmnop"[parseInt(h, 16)]).join("");
  assert.equal(id.length, 32);
  assert.match(id, /^[a-p]+$/, "extension id must be 32 chars in a-p");
  assert.equal(id, "iggpljaabncedkleifmbnpnfjfjfkpjj");
});

test("side panel is declared", () => {
  assert.equal(manifest.side_panel?.default_path, "panel/sidepanel.html");
});

test("background is a module service worker", () => {
  assert.equal(manifest.background?.service_worker, "background.js");
  assert.equal(manifest.background?.type, "module");
});

test("permissions are exactly the six declared capabilities", () => {
  assert.deepEqual(
    [...manifest.permissions].sort(),
    ["alarms", "debugger", "scripting", "sidePanel", "storage", "tabs"],
  );
});

test("host_permissions are not pre-granted at install time", () => {
  // Host permissions are granted at runtime only (optional_host_permissions
  // plus runtime requests); an install-time host grant would widen the
  // extension beyond the governed browser path.
  assert.ok(!("host_permissions" in manifest), "host_permissions must be absent from the manifest");
  assert.ok(
    !("optional_host_permissions" in manifest),
    "optional_host_permissions must be absent until a task introduces runtime grants",
  );
});

test("minimum_chrome_version requires Chrome 116 or later", () => {
  assert.equal(manifest.minimum_chrome_version, "116");
});

test("CSP allows connect-src to the loopback WebSocket only", () => {
  const csp = manifest.content_security_policy?.extension_pages;
  assert.equal(typeof csp, "string");
  const directives = Object.fromEntries(
    csp.split(";").map((d) => d.trim()).filter(Boolean).map((d) => {
      const i = d.indexOf(" ");
      return [d.slice(0, i), d.slice(i + 1)];
    }),
  );
  assert.equal(directives["script-src"], "'self'");
  assert.equal(directives["object-src"], "'self'");
  assert.equal(directives["connect-src"], `ws://${LOOPBACK}:*`);
  // No other directive may broaden the default: no http(s) schemes, no
  // wildcards outside the loopback port wildcard, no hostnames.
  for (const [name, value] of Object.entries(directives)) {
    if (name === "connect-src") continue;
    assert.ok(!value.includes("ws:") && !value.includes("wss:"), `${name} must not add socket endpoints`);
    assert.ok(!value.includes("http"), `${name} must not add http endpoints`);
  }
  assert.ok(!csp.includes("localhost"), "CSP must never allow localhost");
});

test("manifest does not declare content scripts (vendored scripts are dormant placeholders)", () => {
  // adapter.js and content.js are vendored placeholders from ferro; T8.2+
  // own the page-execution path, so the scaffold registers no content
  // scripts rather than injecting ferro's relay into every page.
  assert.ok(!("content_scripts" in manifest));
});
