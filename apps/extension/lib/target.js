// target.js -- T8.2 ferro-target selector decode and execution-identity
// comparison.
//
// A governed page action carries a `ferro-target:` selector: the base64url
// JSON payload of the reviewed snapshot element (selector plus identity
// fields). This module decodes that payload and compares it against the
// identity the live page reports at execution time, including the FULL href
// and the form action (ADR 005: "href and form_action URL-normalized and
// compared exactly" -- the vendored placeholder adapter's mustFind compares
// only the href path, so the extension's governed path supersedes it here).
//
// Fail closed: any decode problem or identity difference is stale_target
// (or invalid_target for a malformed payload), never a CSS-only fallback.
// Pure functions only, so the unit tier needs no DOM.

export const FERRO_TARGET_PREFIX = "ferro-target:";

export class TargetError extends Error {
  constructor(code, message) {
    super(message);
    this.name = "TargetError";
    this.code = code;
  }
}

// Decode a `ferro-target:` selector into { selector, expected }, where
// `expected` is the reviewed identity payload (selector stripped). A plain
// CSS selector decodes to { selector, expected: null } -- execution still
// requires uniqueness, but no identity fields are compared. Mirrors the
// vendored adapter's base64url decoding exactly (ferro-target payload is
// standard base64 spelled with the URL-safe alphabet).
export function decodeTarget(raw) {
  if (typeof raw !== "string" || raw.length === 0) {
    throw new TargetError("invalid_target", "target is empty");
  }
  if (!raw.startsWith(FERRO_TARGET_PREFIX)) {
    return { selector: raw, expected: null };
  }
  let payload = raw.slice(FERRO_TARGET_PREFIX.length).replace(/-/g, "+").replace(/_/g, "/");
  payload += "=".repeat((4 - (payload.length % 4)) % 4);
  let expected;
  try {
    const bytes = Uint8Array.from(atob(payload), (char) => char.charCodeAt(0));
    expected = JSON.parse(new TextDecoder().decode(bytes));
  } catch (error) {
    throw new TargetError("invalid_target", `invalid ferro-target payload: ${error.message}`);
  }
  if (expected === null || typeof expected !== "object" || Array.isArray(expected)) {
    throw new TargetError("invalid_target", "ferro-target payload is not an object");
  }
  if (typeof expected.selector !== "string" || expected.selector.length === 0) {
    // TargetSelector errors on an empty Selector and never falls back to CSS.
    throw new TargetError("invalid_target", "ferro-target payload has no selector");
  }
  const { selector, ...identity } = expected;
  return { selector, expected: identity };
}

// Name and text normalize to NFC with whitespace collapsed (ADR 005
// identity normalization).
export function normalizeText(value) {
  return String(value ?? "")
    .normalize("NFC")
    .replace(/\s+/g, " ")
    .trim();
}

// href and form_action normalize to absolute URLs and compare exactly
// (new URL().href applies the standard URL normalization). Relative
// expectations resolve against the page URL the execution probe observed.
// A value that cannot normalize keeps its raw form, which can only fail
// the exact comparison -- never silently pass.
export function normalizeUrl(value, base) {
  if (value == null || value === "") return "";
  try {
    return new URL(value, base || undefined).href;
  } catch {
    return String(value);
  }
}

// Compare the reviewed expected identity against the identity the isolated
// world observed. Only fields present in `expected` are compared, so an
// older snapshot payload (href-as-pathname, no form_action) still pins
// everything it carried. Returns false on any difference.
export function identityMatches(expected, observed, base) {
  if (!expected) return true;
  if (!observed || typeof observed !== "object") return false;
  if (expected.tag != null && observed.tag !== String(expected.tag).toLowerCase()) return false;
  if ((expected.role ?? "") !== (observed.role ?? "")) return false;
  if (normalizeText(expected.name) !== normalizeText(observed.name)) return false;
  if (normalizeText(expected.text) !== normalizeText(observed.text)) return false;
  if (expected.href != null && normalizeUrl(expected.href, base) !== (observed.href ?? "")) {
    return false;
  }
  if (expected.form_action != null && normalizeUrl(expected.form_action, base) !== (observed.formAction ?? "")) {
    return false;
  }
  if (expected.input_type != null && (observed.inputType ?? "") !== expected.input_type) {
    return false;
  }
  if (expected.autocomplete != null && (observed.autocomplete ?? "") !== expected.autocomplete) {
    return false;
  }
  return true;
}
