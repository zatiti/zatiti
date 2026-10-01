// page.test.mjs -- T8.2 page command executor over the CDP fake:
// hit-test pass and fail, focus verification (pass and mismatch), the
// always-detach rule (success AND failure paths), the named-key-only
// rule, trusted fill and sensitive refusal on the trusted paths.
// node:test, no npm dependencies.

import test from "node:test";
import assert from "node:assert/strict";
import { createPageExecutor, NAMED_KEYS, PageCommandError } from "../lib/page.js";
import { SensitiveFieldError } from "../lib/sensitive.js";
import { FakeCDP, FakeDocument, makeElement, FakeText } from "./support/cdp.mjs";

const PAGE = "https://example.test/page";

// Minimal stand-in for the vendored adapter.js source: in the unit tier
// the adapter is data evaluated into the isolated world; the fake
// recognizes the FerroAdapter marker and scripts the reply. (The real
// file is hashed and exercised by vendor.test.mjs; the headless tier
// runs the vendored bytes.)
const ADAPTER_SOURCE = "/* vendored adapter stand-in */ globalThis.FerroAdapter = { perform: async () => ({}) };";

function makeExecutor(cdp, extra = {}) {
  return createPageExecutor({ cdp, adapterSource: ADAPTER_SOURCE, ...extra });
}

function encodeTarget(payload) {
  return "ferro-target:" + Buffer.from(JSON.stringify(payload), "utf8").toString("base64url");
}

function makeDoc() {
  const doc = new FakeDocument();
  const link = makeElement("a", { id: "login", "aria-label": "Log in", href: "/session/new" }, doc.body);
  const input = makeElement(
    "input",
    { id: "email", type: "email", "aria-label": "Email address", name: "email", autocomplete: "email" },
    doc.body,
  );
  return { doc, link, input };
}

function codeOf(error) {
  return error instanceof PageCommandError ? error.code : `not-a-PageCommandError: ${error}`;
}

test("snapshot runs the vendored adapter op in the isolated world and detaches", async () => {
  const { doc } = makeDoc();
  const cdp = new FakeCDP({ doc });
  const executor = makeExecutor(cdp);
  const reply = await executor.perform({ op: "snapshot", maxElements: 50 });
  assert.ok(reply.snapshot, "snapshot reply carries the adapter's snapshot");
  assert.equal(cdp.detaches, 1, "the debugger detached exactly once");
  assert.equal(cdp.attached, false);
  // The vendored adapter source was shipped into the isolated world as data.
  const adapterCall = cdp.calls.find(
    (call) => call.method === "Runtime.evaluate" && call.params.expression.includes("FerroAdapter"),
  );
  assert.ok(adapterCall, "the adapter op was evaluated in the isolated world");
});

test("click passes when the debugger's hit-test resolves inside the target", async () => {
  const { doc, link } = makeDoc();
  const cdp = new FakeCDP({ doc, hitElement: link });
  const executor = makeExecutor(cdp);
  const action = {
    op: "click",
    selector: encodeTarget({ selector: "#login", tag: "a", name: "Log in" }),
  };
  await assert.doesNotReject(() => executor.perform(action));
  const pressed = cdp.inputDispatches.filter((d) => d.params.type === "mousePressed");
  const released = cdp.inputDispatches.filter((d) => d.params.type === "mouseReleased");
  assert.equal(pressed.length, 1, "exactly one trusted click");
  assert.equal(released.length, 1);
  assert.equal(cdp.detaches, 1);
});

test("click fails closed with stale_target when the hit lands on an overlay", async () => {
  const { doc } = makeDoc();
  const overlay = makeElement("div", { id: "overlay" }, doc.body);
  const cdp = new FakeCDP({ doc, hitElement: overlay });
  const executor = makeExecutor(cdp);
  const action = {
    op: "click",
    selector: encodeTarget({ selector: "#login", tag: "a", name: "Log in" }),
  };
  await assert.rejects(() => executor.perform(action), (error) => codeOf(error) === "stale_target");
  assert.equal(cdp.inputDispatches.length, 0, "no input was dispatched after a hit-test mismatch");
  assert.equal(cdp.detaches, 1, "the debugger detached even on the failure path");
});

test("click fails closed when the hit-test finds nothing or cannot resolve", async () => {
  const { doc } = makeDoc();
  const none = new FakeCDP({ doc, hitElement: null });
  await assert.rejects(
    () => makeExecutor(none).perform({ op: "click", selector: "#login" }),
    (error) => codeOf(error) === "stale_target",
  );
  assert.equal(none.detaches, 1);

  const unresolvable = new FakeCDP({ doc, hitElement: doc.body, resolveFails: true });
  await assert.rejects(
    () => makeExecutor(unresolvable).perform({ op: "click", selector: "#login" }),
    (error) => codeOf(error) === "stale_target",
  );
  assert.equal(unresolvable.detaches, 1);
});

test("click verifies snapshot identity before dispatching", async () => {
  const { doc, link } = makeDoc();
  const cdp = new FakeCDP({ doc, hitElement: link });
  const executor = makeExecutor(cdp);
  // Reviewed name no longer matches the live element: stale ref.
  const action = {
    op: "click",
    selector: encodeTarget({ selector: "#login", tag: "a", name: "Sign up now" }),
  };
  await assert.rejects(() => executor.perform(action), (error) => codeOf(error) === "stale_target");
  assert.equal(cdp.inputDispatches.length, 0);
  assert.equal(cdp.detaches, 1);
});

test("click accepts a hit on a descendant of the reviewed target (target.contains(hit))", async () => {
  const { doc, link } = makeDoc();
  const span = makeElement("span", { id: "login-label" }, link);
  const cdp = new FakeCDP({ doc, hitElement: span });
  const executor = makeExecutor(cdp);
  await assert.doesNotReject(() =>
    executor.perform({
      op: "click",
      selector: encodeTarget({ selector: "#login", tag: "a", name: "Log in" }),
    }),
  );
  assert.equal(cdp.inputDispatches.length, 2, "mousePressed + mouseReleased");
});

test("key dispatches only after focus verification, named keys only", async () => {
  const { doc, input } = makeDoc();
  const cdp = new FakeCDP({ doc });
  const executor = makeExecutor(cdp);
  await executor.perform({ op: "key", text: "Enter", selector: "#email" });
  const keys = cdp.inputDispatches.filter((d) => d.type === "key");
  assert.equal(keys.length, 2, "keyDown + keyUp");
  assert.equal(keys[0].params.type, "keyDown");
  assert.equal(keys[0].params.key, "Enter");
  assert.equal(keys[0].params.windowsVirtualKeyCode, 13);
  assert.equal(cdp.detaches, 1);
  assert.ok(input.focused, "the target was focused");
});

test("key fails with focus_mismatch when focus was stolen before dispatch", async () => {
  const { doc, input } = makeDoc();
  const cdp = new FakeCDP({ doc });
  // Simulate a focus steal between focus and verification: the fake's
  // activeElement is moved off the target by an interloper.
  const interloper = makeElement("input", { id: "trap" }, doc.body);
  cdp.evaluateHandler = (expression) => {
    if (expression.includes("__zatitiFocus")) {
      const selector = JSON.parse(expression.slice(expression.lastIndexOf("(") + 1, -1).trim());
      const [el] = doc.querySelectorAll(selector);
      doc.focus(el);
      doc.focus(interloper); // the page stole focus right back
      return {
        ok: true,
        url: PAGE,
        focused: doc.activeElement === el,
        activeFacts: { tag: "input", name: "" },
        facts: { tag: "input", name: "Email address" },
      };
    }
    return undefined;
  };
  const executor = makeExecutor(cdp);
  await assert.rejects(
    () => executor.perform({ op: "key", text: "Enter", selector: "#email" }),
    (error) => codeOf(error) === "focus_mismatch",
  );
  assert.equal(cdp.inputDispatches.length, 0, "no key event was dispatched");
  assert.equal(cdp.detaches, 1);
  assert.ok(input.focused || true);
});

test("key fails with focus_mismatch when the active element is not the target", async () => {
  const { doc } = makeDoc();
  const trap = makeElement("input", { id: "trap" }, doc.body);
  const cdp = new FakeCDP({ doc });
  // Focus verification must run against the real activeElement identity:
  // the fake reports focused=true but the active element's facts differ
  // from the reviewed target.
  cdp.evaluateHandler = (expression) => {
    if (expression.includes("__zatitiFocus")) {
      const selector = JSON.parse(expression.slice(expression.lastIndexOf("(") + 1, -1).trim());
      const [el] = doc.querySelectorAll(selector);
      doc.focus(trap);
      return {
        ok: true,
        url: PAGE,
        focused: doc.activeElement === el, // false: the trap holds focus
        activeFacts: { tag: "input", id: "trap" },
        facts: { tag: "input", id: "email" },
      };
    }
    return undefined;
  };
  const executor = makeExecutor(cdp);
  await assert.rejects(
    () => executor.perform({ op: "key", text: "Tab", selector: "#email" }),
    (error) => codeOf(error) === "focus_mismatch",
  );
  assert.equal(cdp.inputDispatches.length, 0);
  assert.equal(cdp.detaches, 1);
});

test("key refuses free text: named keys only", async () => {
  const { doc } = makeDoc();
  const cdp = new FakeCDP({ doc });
  const executor = makeExecutor(cdp);
  await assert.rejects(
    () => executor.perform({ op: "key", text: "hello world", selector: "#email" }),
    (error) => codeOf(error) === "named_key_refused",
  );
  await assert.rejects(
    () => executor.perform({ op: "key", text: "", selector: "#email" }),
    (error) => codeOf(error) === "named_key_refused",
  );
  await assert.rejects(
    () => executor.perform({ op: "key", selector: "#email" }),
    (error) => codeOf(error) === "named_key_refused",
  );
  assert.equal(cdp.inputDispatches.length, 0, "nothing was dispatched for free text");
  assert.equal(cdp.detaches, 3, "each refusal still detached");
});

test("every NAMED_KEYS entry dispatches", async () => {
  const { doc } = makeDoc();
  for (const name of Object.keys(NAMED_KEYS)) {
    const cdp = new FakeCDP({ doc });
    await makeExecutor(cdp).perform({ op: "key", text: name, selector: "#email" });
    const keys = cdp.inputDispatches.filter((d) => d.type === "key");
    assert.equal(keys.length, 2, name);
    assert.equal(keys[0].params.code, NAMED_KEYS[name].code, name);
  }
});

test("fill inserts trusted text into the verified focused field", async () => {
  const { doc } = makeDoc();
  const cdp = new FakeCDP({ doc });
  const executor = makeExecutor(cdp);
  await executor.perform({
    op: "fill",
    selector: encodeTarget({
      selector: "#email",
      tag: "input",
      name: "Email address",
      input_type: "email",
    }),
    text: "ada@example.test",
  });
  const inserts = cdp.inputDispatches.filter((d) => d.type === "insertText");
  assert.equal(inserts.length, 1);
  assert.equal(inserts[0].params.text, "ada@example.test");
  assert.equal(cdp.detaches, 1);
});

test("fill refuses sensitive fields before any insert", async () => {
  const { doc } = makeDoc();
  const password = makeElement(
    "input",
    { id: "pw", type: "password", name: "password", autocomplete: "current-password" },
    doc.body,
  );
  const cdp = new FakeCDP({ doc });
  const executor = makeExecutor(cdp);
  await assert.rejects(
    () => executor.perform({ op: "fill", selector: "#pw", text: "secret" }),
    (error) => error instanceof SensitiveFieldError && error.code === "sensitive_field_refused",
  );
  assert.equal(cdp.inputDispatches.length, 0, "no trusted insert reached a sensitive field");
  assert.equal(cdp.detaches, 1);
  assert.ok(password);
});

test("fill fails with focus_mismatch when focus cannot be held", async () => {
  const { doc } = makeDoc();
  const cdp = new FakeCDP({ doc });
  cdp.evaluateHandler = (expression) => {
    if (expression.includes("__zatitiFocus")) {
      const selector = JSON.parse(expression.slice(expression.lastIndexOf("(") + 1, -1).trim());
      const [el] = doc.querySelectorAll(selector);
      doc.focus(el);
      return { ok: true, url: PAGE, focused: false, activeFacts: null, facts: null };
    }
    return undefined;
  };
  await assert.rejects(
    () => makeExecutor(cdp).perform({ op: "fill", selector: "#email", text: "x" }),
    (error) => codeOf(error) === "focus_mismatch",
  );
  assert.equal(cdp.inputDispatches.length, 0);
});

test("extract refuses when any requested field is sensitive", async () => {
  const { doc } = makeDoc();
  makeElement(
    "input",
    { id: "pw", type: "password", name: "password" },
    doc.body,
  );
  const cdp = new FakeCDP({ doc });
  const executor = makeExecutor(cdp);
  await assert.rejects(
    () =>
      executor.perform({
        op: "extract",
        fields: { email: "#email", secret: "#pw" },
      }),
    (error) => error instanceof SensitiveFieldError && error.code === "sensitive_field_refused",
  );
  // The adapter never ran: no FerroAdapter expression reached the world.
  assert.equal(
    cdp.calls.some((call) => call.params?.expression?.includes("FerroAdapter")),
    false,
    "extract of a sensitive field never reaches the vendored adapter",
  );
  assert.equal(cdp.detaches, 1);
});

test("extract passes for ordinary fields", async () => {
  const { doc } = makeDoc();
  const cdp = new FakeCDP({ doc });
  const executor = makeExecutor(cdp);
  const reply = await executor.perform({ op: "extract", fields: { email: "#email" } });
  assert.deepEqual(reply.result, { email: "value-for-email" });
  assert.equal(cdp.detaches, 1);
});

test("select refuses sensitive fields and otherwise runs the adapter op", async () => {
  const { doc } = makeDoc();
  makeElement("select", { id: "country", name: "country" }, doc.body);
  makeElement("input", { id: "pw", type: "password", name: "password" }, doc.body);
  const cdp = new FakeCDP({ doc });
  const executor = makeExecutor(cdp);
  // A sensitive select target is refused before the adapter runs.
  await assert.rejects(
    () => executor.perform({ op: "select", selector: "#pw", value: "x" }),
    (error) => error instanceof SensitiveFieldError && error.code === "sensitive_field_refused",
  );
  assert.equal(
    cdp.calls.some((call) => call.params?.expression?.includes("FerroAdapter")),
    false,
    "a sensitive select never reaches the vendored adapter",
  );
  // An ordinary select target runs the vendored select op.
  const reply = await executor.perform({ op: "select", selector: "#country", value: "ke" });
  assert.equal(reply.selected, "ke");
  assert.equal(cdp.detaches, 2);
});

test("navigate uses the tabs dependency and detaches", async () => {
  const { doc } = makeDoc();
  const cdp = new FakeCDP({ doc });
  const tabs = {
    updates: [],
    async update(url) {
      this.updates.push(url);
      return { id: 7, url };
    },
  };
  const reply = await makeExecutor(cdp, { tabs }).perform({ op: "navigate", to: PAGE });
  assert.equal(reply.url, PAGE);
  assert.deepEqual(tabs.updates, [PAGE]);
  assert.equal(cdp.detaches, 1);
});

test("the debugger ALWAYS detaches -- including when the command throws", async () => {
  const { doc } = makeDoc();
  const exploding = new FakeCDP({ doc });
  exploding.send = async function (method, params) {
    if (method === "Runtime.evaluate") throw new Error("connection dropped");
    return FakeCDP.prototype.send.call(this, method, params);
  };
  await assert.rejects(
    () => makeExecutor(exploding).perform({ op: "snapshot" }),
    /connection dropped/,
  );
  assert.equal(exploding.detaches, 1, "detach ran on the exception path");

  // And when detach itself fails, the error of the command still surfaces
  // (detach errors are swallowed, not masked).
  const doubleTrouble = new FakeCDP({ doc });
  doubleTrouble.send = async function (method, params) {
    if (method === "Runtime.evaluate") throw new Error("boom");
    if (method === "Debugger.detach") throw new Error("detach broke");
    return FakeCDP.prototype.send.call(this, method, params);
  };
  await assert.rejects(
    () => makeExecutor(doubleTrouble).perform({ op: "snapshot" }),
    /boom/,
  );
  assert.equal(doubleTrouble.detaches, 1);
});

test("an expired action is refused before any debugger attach", async () => {
  const { doc } = makeDoc();
  const cdp = new FakeCDP({ doc });
  await assert.rejects(
    () =>
      makeExecutor(cdp, { now: () => 5000 }).perform({
        op: "snapshot",
        deadlineMs: 4000,
      }),
    (error) => codeOf(error) === "expired",
  );
  assert.equal(cdp.calls.length, 0, "nothing ran after the deadline");
});

test("unknown ops and missing adapter sources fail closed", async () => {
  const { doc } = makeDoc();
  const cdp = new FakeCDP({ doc });
  await assert.rejects(
    () => makeExecutor(cdp).perform({ op: "hover", selector: "#email" }),
    (error) => codeOf(error) === "unsupported",
  );
  await assert.rejects(
    () =>
      createPageExecutor({ cdp, adapterSource: null }).perform({
        op: "snapshot",
      }),
    (error) => codeOf(error) === "adapter_missing",
  );
  // The unknown op is refused before any attach; the snapshot case attaches
  // once and detaches in its finally. Exactly one debugger session ran.
  assert.equal(cdp.detaches, 1);
});

test("stale targets on text reads fail closed", async () => {
  const { doc } = makeDoc();
  const cdp = new FakeCDP({ doc });
  const executor = makeExecutor(cdp);
  await assert.rejects(
    () =>
      executor.perform({
        op: "text",
        selector: encodeTarget({ selector: "#gone", tag: "p" }),
      }),
    (error) => codeOf(error) === "stale_target",
  );
  assert.equal(cdp.detaches, 1);
});

test("a text hit on a text node resolves through its parent element", async () => {
  const { doc, link } = makeDoc();
  const text = new FakeText("Log in", link);
  const cdp = new FakeCDP({ doc, hitElement: text });
  await assert.doesNotReject(() =>
    makeExecutor(cdp).perform({
      op: "click",
      selector: encodeTarget({ selector: "#login", tag: "a", name: "Log in" }),
    }),
  );
  assert.ok(text);
});
