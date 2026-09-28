// sensitive.test.mjs -- T8.2 sensitive-field refusal matrix (SC16):
// password, one-time-code, current-password, new-password and cc-* fields
// are refused for fill, select, key AND extract. node:test, no npm
// dependencies.

import test from "node:test";
import assert from "node:assert/strict";
import {
  isSensitiveField,
  refuseSensitiveExtract,
  refuseSensitiveTarget,
  SensitiveFieldError,
} from "../lib/sensitive.js";

const field = (overrides) => ({
  tag: "input",
  inputType: "text",
  autocomplete: "",
  name: "",
  id: "",
  ariaLabel: "",
  placeholder: "",
  ...overrides,
});

test("type=password is refused regardless of other attributes", () => {
  assert.equal(isSensitiveField(field({ inputType: "password" })), true);
  assert.equal(isSensitiveField(field({ inputType: "password", autocomplete: "off" })), true);
});

test("autocomplete=current-password and new-password are refused, including sectioned forms", () => {
  for (const autocomplete of [
    "current-password",
    "new-password",
    "shipping current-password",
    "billing new-password",
    "section-login current-password",
  ]) {
    assert.equal(isSensitiveField(field({ autocomplete })), true, autocomplete);
  }
});

test("autocomplete=one-time-code is refused, including sectioned forms", () => {
  for (const autocomplete of ["one-time-code", "shipping one-time-code"]) {
    assert.equal(isSensitiveField(field({ autocomplete })), true, autocomplete);
  }
});

test("cc-* autocomplete tokens are refused", () => {
  for (const autocomplete of [
    "cc-number",
    "cc-exp",
    "cc-exp-month",
    "cc-csc",
    "cc-name",
    "billing cc-number",
  ]) {
    assert.equal(isSensitiveField(field({ autocomplete })), true, autocomplete);
  }
});

test("password-ish names, ids and labels are refused", () => {
  assert.equal(isSensitiveField(field({ name: "password" })), true);
  assert.equal(isSensitiveField(field({ id: "user-passwd" })), true);
  assert.equal(isSensitiveField(field({ ariaLabel: "Enter your password" })), true);
  assert.equal(isSensitiveField(field({ placeholder: "Password" })), true);
});

test("one-time-code-ish names and ids are refused", () => {
  assert.equal(isSensitiveField(field({ name: "otp" })), true);
  assert.equal(isSensitiveField(field({ id: "sms_code" })), true);
  assert.equal(isSensitiveField(field({ ariaLabel: "One-time code" })), true);
  assert.equal(isSensitiveField(field({ name: "verification_code" })), true);
});

test("credit-card-ish names and ids are refused", () => {
  assert.equal(isSensitiveField(field({ name: "cardnumber" })), true);
  assert.equal(isSensitiveField(field({ id: "cc-csc" })), true);
  assert.equal(isSensitiveField(field({ name: "cc_cvv" })), true);
});

test("ordinary fields are allowed", () => {
  assert.equal(isSensitiveField(field()), false);
  assert.equal(isSensitiveField(field({ name: "email", autocomplete: "email" })), false);
  assert.equal(isSensitiveField(field({ inputType: "search", ariaLabel: "Search the site" })), false);
  assert.equal(isSensitiveField(field({ name: "coupon", placeholder: "Promo code" })), false);
  // 'username' must not trip the password vocabulary; neither must a
  // comment field that merely contains the letters c-c.
  assert.equal(isSensitiveField(field({ name: "username" })), false);
  assert.equal(isSensitiveField(field({ name: "accompaniment" })), false);
});

test("non-object and empty facts are not sensitive", () => {
  assert.equal(isSensitiveField(null), false);
  assert.equal(isSensitiveField({}), false);
});

test("refuseSensitiveTarget throws SensitiveFieldError with the governed code", () => {
  assert.throws(() => refuseSensitiveTarget(field({ inputType: "password" })), (error) => {
    return error instanceof SensitiveFieldError && error.code === "sensitive_field_refused";
  });
  // And does not throw for an ordinary field.
  refuseSensitiveTarget(field({ name: "email" }));
});

test("refuseSensitiveExtract refuses when a requested field is sensitive", () => {
  const fields = { email: "#email", secret: "#pw" };
  const probes = {
    "#email": field({ name: "email" }),
    "#pw": field({ inputType: "password" }),
  };
  assert.throws(
    () => refuseSensitiveExtract(fields, probes),
    (error) => error instanceof SensitiveFieldError && error.code === "sensitive_field_refused",
  );
});

test("refuseSensitiveExtract keys probes by selector or by result name", () => {
  const fields = { otp_code: "#code" };
  assert.throws(
    () => refuseSensitiveExtract(fields, { "#code": field({ name: "otp" }) }),
    SensitiveFieldError,
  );
  assert.throws(
    () => refuseSensitiveExtract(fields, { otp_code: field({ name: "otp" }) }),
    SensitiveFieldError,
  );
});

test("refuseSensitiveExtract allows ordinary fields and tolerates missing probes", () => {
  const fields = { email: "#email", unknown: "#gone" };
  refuseSensitiveExtract(fields, { "#email": field({ name: "email" }) });
  // No probes at all: nothing observed as sensitive, nothing refused here
  // (the unresolvable field is the adapter's per-field failure to report).
  refuseSensitiveExtract(fields, {});
  refuseSensitiveExtract(null, {});
});

// The op-level gate: the executor applies the same predicate for fill,
// select and key. These cases pin the matrix at the sensitive module's
// contract level so every op shares one refusal.
test("the fill/select/key refusal matrix over one predicate", () => {
  const sensitive = [
    field({ inputType: "password" }),
    field({ autocomplete: "current-password" }),
    field({ autocomplete: "new-password" }),
    field({ autocomplete: "one-time-code" }),
    field({ autocomplete: "cc-number" }),
    field({ name: "passwd" }),
    field({ id: "otp" }),
  ];
  const allowed = [
    field(),
    field({ inputType: "email", autocomplete: "email" }),
    field({ tag: "select", name: "country" }),
  ];
  for (const f of sensitive) {
    assert.throws(() => refuseSensitiveTarget(f), SensitiveFieldError, JSON.stringify(f));
  }
  for (const f of allowed) {
    refuseSensitiveTarget(f);
  }
});
