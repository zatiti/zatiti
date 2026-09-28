// vendor.test.mjs -- the vendored ferro scripts must hash-match
// vendor/ferro/MANIFEST.json (T8.5 re-vendors v0.2.0 and refreshes the
// hashes). node:test, no npm dependencies.

import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const vendorDir = join(here, "..", "vendor", "ferro");
const vendoredManifest = JSON.parse(readFileSync(join(vendorDir, "MANIFEST.json"), "utf8"));

function sha256(file) {
  return createHash("sha256").update(readFileSync(join(vendorDir, file))).digest("hex");
}

test("MANIFEST records the ferro ref", () => {
  // Placeholder ref for T8.1; T8.5 replaces it with the pinned v0.2.0 module.
  assert.equal(vendoredManifest.ferro_ref, "59178db");
});

test("every MANIFEST entry exists on disk with a plausible sha256", () => {
  for (const [file, entry] of Object.entries(vendoredManifest.files)) {
    assert.match(entry.sha256, /^[0-9a-f]{64}$/, `${file}: sha256 must be 64 hex chars`);
    assert.equal(sha256(file), entry.sha256, `${file}: sha256 mismatch against MANIFEST`);
  }
});

test("MANIFEST entry sizes match the files on disk", () => {
  for (const [file, entry] of Object.entries(vendoredManifest.files)) {
    const bytes = readFileSync(join(vendorDir, file)).length;
    assert.equal(bytes, entry.bytes, `${file}: size mismatch against MANIFEST`);
  }
});

test("vendored scripts are non-empty JavaScript", () => {
  for (const file of ["adapter.js", "content.js"]) {
    const src = readFileSync(join(vendorDir, file), "utf8");
    assert.ok(src.length > 100, `${file} must not be a stub`);
    assert.ok(src.includes("extension/"), `${file} should retain its ferro header`);
  }
});

test("vendored scripts carry no local filesystem paths", () => {
  // Repo hygiene: no personal paths in committed vendor files (T8.5's acc
  // enforces the same rule over the whole vendor tree).
  for (const file of ["adapter.js", "content.js"]) {
    const src = readFileSync(join(vendorDir, file), "utf8");
    assert.ok(!src.includes("/Users/"), `${file} must not contain /Users/`);
    assert.ok(!src.includes("~/"), `${file} must not contain ~/`);
  }
});
