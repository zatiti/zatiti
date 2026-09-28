// page.js -- T8.2 page command executor: governed page reads plus
// hit-test / focus-verified trusted input over the Chrome debugger
// protocol.
//
// Reads and adapter-backed mutations (snapshot, text, extract, navigate,
// scroll, select) run the VENDORED adapter.js inside a CDP-created
// isolated world (FerroAdapter.perform) -- the vendored scripts are
// loaded as data, never modified.
//
// Trusted input (click, fill, key) is CDP-mediated and verified first:
//   - click: the element under the pointer is found by the debugger
//     itself (DOM.getNodeForLocation -> DOM.resolveNode) and must be the
//     reviewed target or inside it (target.contains(hit)), in an isolated
//     world, before Input.dispatchMouseEvent; any mismatch fails closed
//     with stale_target (plan.md R22).
//   - key: the target is focused and document.activeElement is verified
//     (element identity + focused flag) before Input.dispatchKeyEvent;
//     otherwise focus_mismatch. Named keys only -- free text is refused.
//   - fill: after the same focus verification, trusted Input.insertText
//     into the verified focused field.
//
// The debugger ALWAYS detaches: every session ends in a finally block.
//
// The executor is dependency-injected over the CDP surface so the unit
// tier drives it with a fake and no Chrome:
//   cdp = { attach(), send(method, params) -> result, detach() }
//   tabs (navigate only) = { update(url) -> tab, waitForComplete(tabId, deadlineMs) }

import {
  decodeTarget,
  identityMatches,
  TargetError,
} from "./target.js";
import {
  refuseSensitiveExtract,
  refuseSensitiveTarget,
  SensitiveFieldError,
} from "./sensitive.js";

export class PageCommandError extends Error {
  constructor(code, message) {
    super(message);
    this.name = "PageCommandError";
    this.code = code;
  }
}

// Named keys only (plan.md DF8: "key accepts named keys only"). Map is
// key name -> { code, windowsVirtualKeyCode, keyDownText }.
export const NAMED_KEYS = {
  Enter: { code: "Enter", windowsVirtualKeyCode: 13, keyDownText: "\r" },
  Tab: { code: "Tab", windowsVirtualKeyCode: 9, keyDownText: "\t" },
  Escape: { code: "Escape", windowsVirtualKeyCode: 27 },
  Backspace: { code: "Backspace", windowsVirtualKeyCode: 8 },
  Delete: { code: "Delete", windowsVirtualKeyCode: 46 },
  ArrowUp: { code: "ArrowUp", windowsVirtualKeyCode: 38 },
  ArrowDown: { code: "ArrowDown", windowsVirtualKeyCode: 40 },
  ArrowLeft: { code: "ArrowLeft", windowsVirtualKeyCode: 37 },
  ArrowRight: { code: "ArrowRight", windowsVirtualKeyCode: 39 },
  Home: { code: "Home", windowsVirtualKeyCode: 36 },
  End: { code: "End", windowsVirtualKeyCode: 35 },
  PageUp: { code: "PageUp", windowsVirtualKeyCode: 33 },
  PageDown: { code: "PageDown", windowsVirtualKeyCode: 34 },
  Space: { code: "Space", windowsVirtualKeyCode: 32, keyDownText: " " },
};

// ---------------------------------------------------------------------
// Isolated-world probe sources. Each is a self-contained function
// stringified into a Runtime.evaluate expression: no closures, so the
// real isolated world and the shape recorded by the CDP fake agree.
// factsOf mirrors the vendored adapter's snapshot identity extraction
// (aria-label/placeholder/label-for name, href as ABSOLUTE url) plus the
// execution-only identity fields (form action, input type, autocomplete).
// ---------------------------------------------------------------------

const PROBE_SOURCE = `
function __zatitiProbe(selector) {
  const wellFormed = (value) => {
    const text = String(value);
    return typeof text.toWellFormed === 'function' ? text.toWellFormed() : text;
  };
  const truncate = (value, ellipsis) => {
    let text = wellFormed(value);
    if (text.length > 80) text = text.slice(0, 80) + (ellipsis ? '…' : '');
    return wellFormed(text);
  };
  const factsOf = (el) => {
    if (!el || el.nodeType !== 1) return null;
    let name = el.getAttribute('aria-label') || el.getAttribute('placeholder') || '';
    if (!name && ['INPUT', 'SELECT', 'TEXTAREA'].includes(el.tagName) && el.id) {
      const label = document.querySelector('label[for="' + CSS.escape(el.id) + '"]');
      if (label) name = label.innerText.trim();
    }
    const heading = /^H[1-4]$/.test(el.tagName);
    const text = truncate(heading ? el.innerText : (el.innerText || '').trim(), true);
    const form = el.closest && el.closest('form');
    return {
      tag: el.tagName.toLowerCase(),
      role: el.getAttribute('role') || '',
      name: truncate(name),
      text: text,
      href: el.tagName === 'A' && el.getAttribute('href')
        ? new URL(el.getAttribute('href'), location.href).href : '',
      formAction: form && form.getAttribute('action')
        ? new URL(form.getAttribute('action'), location.href).href : '',
      inputType: el.tagName === 'INPUT' ? String(el.type || '') : '',
      autocomplete: el.getAttribute('autocomplete') || '',
      inputName: el.getAttribute('name') || '',
      inputId: el.id || '',
      ariaLabel: el.getAttribute('aria-label') || '',
      placeholder: el.getAttribute('placeholder') || '',
    };
  };
  let els;
  try {
    els = document.querySelectorAll(selector);
  } catch (error) {
    return { ok: false, code: 'invalid_selector', error: String(error && error.message) };
  }
  if (els.length === 0) return { ok: false, code: 'no_match' };
  if (els.length > 1) return { ok: false, code: 'ambiguous' };
  const el = els[0];
  el.scrollIntoView({ block: 'center', behavior: 'instant' });
  const rect = el.getBoundingClientRect();
  return {
    ok: true,
    url: location.href,
    facts: factsOf(el),
    rect: { x: rect.left, y: rect.top, width: rect.width, height: rect.height },
  };
}`;

const FOCUS_SOURCE = `
function __zatitiFocus(selector) {
  const wellFormed = (value) => {
    const text = String(value);
    return typeof text.toWellFormed === 'function' ? text.toWellFormed() : text;
  };
  const truncate = (value, ellipsis) => {
    let text = wellFormed(value);
    if (text.length > 80) text = text.slice(0, 80) + (ellipsis ? '…' : '');
    return wellFormed(text);
  };
  const factsOf = (el) => {
    if (!el || el.nodeType !== 1) return null;
    let name = el.getAttribute('aria-label') || el.getAttribute('placeholder') || '';
    if (!name && ['INPUT', 'SELECT', 'TEXTAREA'].includes(el.tagName) && el.id) {
      const label = document.querySelector('label[for="' + CSS.escape(el.id) + '"]');
      if (label) name = label.innerText.trim();
    }
    const heading = /^H[1-4]$/.test(el.tagName);
    const text = truncate(heading ? el.innerText : (el.innerText || '').trim(), true);
    const form = el.closest && el.closest('form');
    return {
      tag: el.tagName.toLowerCase(),
      role: el.getAttribute('role') || '',
      name: truncate(name),
      text: text,
      href: el.tagName === 'A' && el.getAttribute('href')
        ? new URL(el.getAttribute('href'), location.href).href : '',
      formAction: form && form.getAttribute('action')
        ? new URL(form.getAttribute('action'), location.href).href : '',
      inputType: el.tagName === 'INPUT' ? String(el.type || '') : '',
      autocomplete: el.getAttribute('autocomplete') || '',
      inputName: el.getAttribute('name') || '',
      inputId: el.id || '',
      ariaLabel: el.getAttribute('aria-label') || '',
      placeholder: el.getAttribute('placeholder') || '',
    };
  };
  let els;
  try {
    els = document.querySelectorAll(selector);
  } catch (error) {
    return { ok: false, code: 'invalid_selector', error: String(error && error.message) };
  }
  if (els.length === 0) return { ok: false, code: 'no_match' };
  if (els.length > 1) return { ok: false, code: 'ambiguous' };
  const el = els[0];
  el.scrollIntoView({ block: 'center', behavior: 'instant' });
  el.focus();
  const active = document.activeElement;
  return {
    ok: true,
    url: location.href,
    focused: active === el,
    activeFacts: factsOf(active),
    facts: factsOf(el),
  };
}`;

// Runs against the DOM.resolveNode remote object of the hit-test node,
// in the same isolated world, and asks whether the reviewed target
// contains the hit (target.contains(hit), per plan.md SC17).
const CONTAINS_SOURCE = `
function __zatitiContains(selector) {
  const hit = this.nodeType === 3 ? this.parentElement : this;
  let target = null;
  try {
    target = document.querySelector(selector);
  } catch (_) {
    return false;
  }
  if (!target) return false;
  return target === hit || target.contains(hit);
}`;

const probeExpression = (selector) => `(${PROBE_SOURCE})(${JSON.stringify(selector)})`;
const focusExpression = (selector) => `(${FOCUS_SOURCE})(${JSON.stringify(selector)})`;
const containsExpression = (selector) =>
  `(${CONTAINS_SOURCE}).call(this, ${JSON.stringify(selector)})`;

// ---------------------------------------------------------------------
// Sessions: one debugger attach per command, detached in a finally.
// ---------------------------------------------------------------------

class DebuggerSession {
  constructor(cdp, now = Date.now) {
    this.cdp = cdp;
    this.now = now;
    this.worldId = null;
  }

  async begin() {
    await this.cdp.attach();
    try {
      await this.cdp.send("Page.enable", {});
      const tree = await this.cdp.send("Page.getFrameTree", {});
      const frameId = tree?.frameTree?.frame?.id;
      if (!frameId) throw new PageCommandError("no_frame", "debugger reported no frame");
      const world = await this.cdp.send("Page.createIsolatedWorld", { frameId });
      this.worldId = world?.executionContextId;
      if (this.worldId == null) {
        throw new PageCommandError("no_world", "could not create the isolated world");
      }
    } catch (error) {
      await this.cdp.detach().catch(() => {});
      throw error;
    }
    return this;
  }

  // Evaluate a self-contained expression in the isolated world; returns
  // the value (returnByValue) or throws on a page-side exception.
  async evaluate(expression) {
    const reply = await this.cdp.send("Runtime.evaluate", {
      expression,
      contextId: this.worldId,
      returnByValue: true,
      awaitPromise: true,
    });
    if (reply?.exceptionDetails) {
      const detail = reply.exceptionDetails.exception?.description
        ?? reply.exceptionDetails.text
        ?? "isolated-world evaluation failed";
      throw new PageCommandError("execution_failed", detail);
    }
    return reply?.result?.value;
  }

  // Resolve a debugger-found node into the isolated world and run a
  // function on it (Runtime.callFunctionOn on the resolved remote object).
  async callOnNode(objectId, functionDeclaration, args = []) {
    const reply = await this.cdp.send("Runtime.callFunctionOn", {
      objectId,
      functionDeclaration,
      arguments: args,
      returnByValue: true,
    });
    if (reply?.exceptionDetails) {
      const detail = reply.exceptionDetails.exception?.description
        ?? reply.exceptionDetails.text
        ?? "callFunctionOn failed";
      throw new PageCommandError("execution_failed", detail);
    }
    return reply?.result?.value;
  }

  async end() {
    await this.cdp.detach().catch(() => {});
  }
}

// ---------------------------------------------------------------------
// Executor.
// ---------------------------------------------------------------------

export function createPageExecutor({ cdp, tabs, now = Date.now, adapterSource } = {}) {
  if (!cdp) throw new TypeError("createPageExecutor requires a cdp client");

  const loadAdapterSource = async () => {
    if (adapterSource) return adapterSource;
    const url = (globalThis.chrome?.runtime?.getURL ?? (() => ""))("vendor/ferro/adapter.js");
    if (!url) throw new PageCommandError("adapter_missing", "vendored adapter.js is unavailable");
    const response = await fetch(url);
    if (!response.ok) throw new PageCommandError("adapter_missing", `adapter.js fetch failed: ${response.status}`);
    return response.text();
  };

  // Run a vendored-adapter op in the isolated world. The adapter source is
  // evaluated as data ahead of the call, so FerroAdapter.perform is
  // available; the vendored file is never modified.
  async function runAdapter(session, action) {
    const source = await loadAdapterSource();
    const expression = `${source}
;globalThis.FerroAdapter.perform(${JSON.stringify(action)})`;
    const reply = await session.evaluate(expression);
    if (reply && reply.error) throw new PageCommandError("execution_failed", reply.error);
    return reply ?? {};
  }

  // Decode the ferro-target selector, probe the live element and verify
  // identity. Throws stale_target / invalid_target fail-closed.
  async function resolveTarget(session, action) {
    const { selector, expected } = decodeTarget(action.selector);
    const probe = await session.evaluate(probeExpression(selector));
    if (!probe || probe.ok !== true) {
      const code = probe?.code ?? "no_match";
      if (code === "invalid_selector") {
        throw new PageCommandError("invalid_target", `invalid selector: ${probe?.error ?? ""}`);
      }
      throw new PageCommandError("stale_target", `target no longer matches (probe: ${code})`);
    }
    if (expected && !identityMatches(expected, probe.facts, probe.url)) {
      throw new PageCommandError("stale_target", "target no longer matches the latest snapshot");
    }
    return { selector, expected, probe };
  }

  // Focus the target and verify document.activeElement. Returns the
  // probe result; throws stale_target / focus_mismatch.
  async function focusAndVerify(session, action) {
    const { selector, expected } = decodeTarget(action.selector);
    const probe = await session.evaluate(focusExpression(selector));
    if (!probe || probe.ok !== true) {
      const code = probe?.code ?? "no_match";
      if (code === "invalid_selector") {
        throw new PageCommandError("invalid_target", `invalid selector: ${probe?.error ?? ""}`);
      }
      throw new PageCommandError("stale_target", `target no longer matches (probe: ${code})`);
    }
    if (!probe.focused) {
      throw new PageCommandError("focus_mismatch", "focus was stolen before the input event");
    }
    if (expected && !identityMatches(expected, probe.activeFacts, probe.url)) {
      throw new PageCommandError("focus_mismatch", "the focused element is not the reviewed target");
    }
    return { selector, probe };
  }

  async function trustedClick(session, action) {
    const { selector, probe } = await resolveTarget(session, action);
    const x = Math.round(probe.rect.x + probe.rect.width / 2);
    const y = Math.round(probe.rect.y + probe.rect.height / 2);

    // CDP hit-test: the debugger, not page script, decides what sits at
    // the point. The hit must be the target or inside it.
    const doc = await session.cdp.send("DOM.getDocument", { depth: 0 });
    const rootNodeId = doc?.root?.nodeId;
    if (!rootNodeId) throw new PageCommandError("hit_test_failed", "DOM.getDocument returned no root");
    const hit = await session.cdp.send("DOM.getNodeForLocation", { x, y });
    if (!hit?.nodeId) {
      throw new PageCommandError("stale_target", "hit-test found no element at the target point");
    }
    const resolved = await session.cdp.send("DOM.resolveNode", {
      nodeId: hit.nodeId,
      executionContextId: session.worldId,
    });
    if (!resolved?.object?.objectId) {
      throw new PageCommandError("stale_target", "hit-test node could not be resolved");
    }
    const contained = await session.callOnNode(
      resolved.object.objectId,
      `(${CONTAINS_SOURCE}).call(this, ${JSON.stringify(selector)})`,
    );
    if (contained !== true) {
      throw new PageCommandError(
        "stale_target",
        "hit-test mismatch: the element under the pointer is not the reviewed target",
      );
    }

    await session.cdp.send("Input.dispatchMouseEvent", {
      type: "mousePressed",
      x,
      y,
      button: "left",
      buttons: 1,
      clickCount: 1,
    });
    await session.cdp.send("Input.dispatchMouseEvent", {
      type: "mouseReleased",
      x,
      y,
      button: "left",
      buttons: 0,
      clickCount: 1,
    });
    return {};
  }

  async function trustedKey(session, action) {
    const keyName = action.text;
    const key = NAMED_KEYS[keyName];
    if (!key) {
      throw new PageCommandError(
        "named_key_refused",
        `key must be a named key, got ${JSON.stringify(keyName ?? null)}`,
      );
    }
    await focusAndVerify(session, action);
    const base = {
      key: keyName,
      code: key.code,
      windowsVirtualKeyCode: key.windowsVirtualKeyCode,
      nativeVirtualKeyCode: key.windowsVirtualKeyCode,
    };
    await session.cdp.send("Input.dispatchKeyEvent", {
      type: "keyDown",
      ...base,
      ...(key.keyDownText != null ? { text: key.keyDownText } : {}),
    });
    await session.cdp.send("Input.dispatchKeyEvent", { type: "keyUp", ...base });
    return {};
  }

  async function trustedFill(session, action) {
    const { selector, expected } = decodeTarget(action.selector);
    // Focus first so the sensitive gate and the insert see the same field.
    const probe = await session.evaluate(focusExpression(selector));
    if (!probe || probe.ok !== true) {
      const code = probe?.code ?? "no_match";
      if (code === "invalid_selector") {
        throw new PageCommandError("invalid_target", `invalid selector: ${probe?.error ?? ""}`);
      }
      throw new PageCommandError("stale_target", `target no longer matches (probe: ${code})`);
    }
    refuseSensitiveTarget(probe.facts);
    if (!probe.focused) {
      throw new PageCommandError("focus_mismatch", "focus was stolen before the input event");
    }
    if (expected && !identityMatches(expected, probe.activeFacts, probe.url)) {
      throw new PageCommandError("focus_mismatch", "the focused element is not the reviewed target");
    }
    await session.cdp.send("Input.insertText", { text: String(action.text ?? "") });
    return {};
  }

  async function selectOption(session, action) {
    const { probe } = await resolveTarget(session, action);
    refuseSensitiveTarget(probe.facts);
    return runAdapter(session, { op: "select", selector: action.selector, value: action.value });
  }

  async function extractFields(session, action) {
    const fields = action.fields ?? {};
    // Refuse sensitive reads before any value leaves the page: probe each
    // requested selector for field facts, then gate the whole extract.
    if (fields && Object.keys(fields).length > 0) {
      const probes = {};
      for (const [name, selector] of Object.entries(fields)) {
        let facts = null;
        try {
          const result = await session.evaluate(probeExpression(decodeTarget(selector).selector));
          facts = result?.facts ?? null;
        } catch {
          // An unresolvable field is the adapter's failure to report, not a
          // sensitive hit; the adapter reports it as a per-field failure.
        }
        probes[selector] = facts;
      }
      refuseSensitiveExtract(fields, probes);
    }
    return runAdapter(session, { op: "extract", fields });
  }

  async function navigate(session, action, deps) {
    if (!deps.tabs?.update) {
      throw new PageCommandError("unsupported", "navigate requires the tabs dependency");
    }
    const url = action.to ?? action.url;
    if (typeof url !== "string" || url.length === 0) {
      throw new PageCommandError("invalid_target", "navigate requires a url");
    }
    const tab = await deps.tabs.update(url);
    if (deps.tabs.waitForComplete) {
      await deps.tabs.waitForComplete(tab?.id, action.deadlineMs, now);
    }
    return { url: tab?.url ?? url };
  }

  async function perform(action) {
    if (!action || typeof action.op !== "string") {
      throw new PageCommandError("invalid_target", "action requires an op");
    }
    if (action.deadlineMs != null && now() >= action.deadlineMs) {
      throw new PageCommandError("expired", "action expired before execution");
    }

    switch (action.op) {
      case "snapshot": {
        const session = await new DebuggerSession(cdp, now).begin();
        try {
          return await runAdapter(session, {
            op: "snapshot",
            maxElements: action.maxElements,
            origin: action.origin,
            deadlineMs: action.deadlineMs,
          });
        } finally {
          await session.end();
        }
      }
      case "text": {
        const session = await new DebuggerSession(cdp, now).begin();
        try {
          const { probe } = await resolveTarget(session, action);
          const text = await session.evaluate(
            `(${PROBE_SOURCE})(${JSON.stringify(action.selector)})`,
          );
          return { text: text?.facts?.text ?? probe?.facts?.text ?? "" };
        } finally {
          await session.end();
        }
      }
      case "extract": {
        const session = await new DebuggerSession(cdp, now).begin();
        try {
          const reply = await extractFields(session, action);
          return { result: reply?.result ?? reply };
        } finally {
          await session.end();
        }
      }
      case "navigate": {
        const session = await new DebuggerSession(cdp, now).begin();
        try {
          return await navigate(session, action, { tabs });
        } finally {
          await session.end();
        }
      }
      case "scroll": {
        const session = await new DebuggerSession(cdp, now).begin();
        try {
          return await runAdapter(session, { op: "scroll", to: action.to });
        } finally {
          await session.end();
        }
      }
      case "select": {
        const session = await new DebuggerSession(cdp, now).begin();
        try {
          return await selectOption(session, action);
        } finally {
          await session.end();
        }
      }
      case "click": {
        const session = await new DebuggerSession(cdp, now).begin();
        try {
          return await trustedClick(session, action);
        } finally {
          await session.end();
        }
      }
      case "key": {
        const session = await new DebuggerSession(cdp, now).begin();
        try {
          return await trustedKey(session, action);
        } finally {
          await session.end();
        }
      }
      case "fill": {
        const session = await new DebuggerSession(cdp, now).begin();
        try {
          return await trustedFill(session, action);
        } finally {
          await session.end();
        }
      }
      default:
        throw new PageCommandError("unsupported", `unknown action op ${JSON.stringify(action.op)}`);
    }
  }

  return { perform };
}
