// test/support/cdp.mjs -- CDP fake for the unit tier (T8.2). No real
// Chrome: it implements just the debugger surface page.js uses
// (attach/detach, Page.*, Runtime.evaluate/callFunctionOn with a real
// in-memory DOM, DOM.getDocument/getNodeForLocation/resolveNode,
// Input.*) against a scripted fixture, and records every call so tests
// can assert ordering and the always-detach rule.
//
// The fake DOM is a tiny hand-rolled tree; `contains` uses the same
// Node.contains semantics as the page.

export class FakeElement {
  constructor(tag, attrs = {}, parent = null) {
    this.tagName = String(tag).toUpperCase();
    this.nodeType = 1;
    this.attrs = { ...attrs };
    this.parent = parent;
    this.children = [];
    this.focused = false;
    if (parent) parent.children.push(this);
  }
  get id() {
    return this.attrs.id ?? "";
  }
  getAttribute(name) {
    return this.attrs[name] ?? "";
  }
  contains(other) {
    for (let node = other; node; node = node.parent) {
      if (node === this) return true;
    }
    return false;
  }
}

export class FakeText extends FakeElement {
  constructor(text, parent) {
    super("#text", {}, parent);
    this.tagName = "#text";
    this.nodeType = 3;
    this.text = text;
  }
}

export function makeElement(tag, attrs, parent) {
  return new FakeElement(tag, attrs, parent);
}

export class FakeDocument {
  constructor() {
    this.root = new FakeElement("html", {}, null);
    this.body = new FakeElement("body", {}, this.root);
    this.activeElement = this.body;
  }
  querySelectorAll(selector) {
    // Supports the simple shapes the fixtures use: tag, #id, and
    // tag#id. Anything else is treated as an invalid selector.
    const m = /^([a-z]+)?(?:#([A-Za-z0-9_-]+))?$/.exec(selector);
    if (!m || (!m[1] && !m[2])) throw new Error(`invalid selector: ${selector}`);
    const found = [];
    const walk = (node) => {
      for (const child of node.children) {
        const tagOk = !m[1] || child.tagName.toLowerCase() === m[1];
        const idOk = !m[2] || child.id === m[2];
        if (tagOk && idOk) found.push(child);
        walk(child);
      }
    };
    walk(this.body);
    return found;
  }
  focus(el) {
    if (el) {
      this.activeElement?.blur?.();
      this.activeElement = el;
      el.focused = true;
    }
  }
}

let nextNodeId = 1;

export class FakeCDP {
  constructor({ doc, hitElement = null, hitError = null, resolveFails = false } = {}) {
    this.doc = doc;
    this.attached = false;
    this.calls = []; // [{method, params}]
    this.detaches = 0;
    this.inputDispatches = []; // [{type, params}]
    // Hit-test scripting: which FakeElement the debugger reports at the
    // probed point (null -> no element), or a forced error.
    this.hitElement = hitElement;
    this.hitError = hitError;
    this.resolveFails = resolveFails;
    this.nodeIds = new Map(); // FakeElement -> nodeId
    this.objectIds = new Map(); // objectId -> FakeElement
    this.nextObjectId = 1;
    this.evaluateResults = new Map(); // marker -> value (tests may script probes)
    this.evaluateHandler = null; // (expression, cdp) => value, optional override
    this.callFunctionOnHandler = null;
  }

  nodeIdFor(el) {
    if (!this.nodeIds.has(el)) this.nodeIds.set(el, nextNodeId++);
    return this.nodeIds.get(el);
  }

  record(method, params) {
    this.calls.push({ method, params });
  }

  async attach() {
    this.record("Debugger.attach", {});
    if (this.attached) throw new Error("fake: already attached");
    this.attached = true;
  }

  async detach() {
    this.record("Debugger.detach", {});
    this.detaches += 1;
    this.attached = false;
  }

  async send(method, params = {}) {
    this.record(method, params);
    switch (method) {
      case "Page.enable":
        return {};
      case "Page.getFrameTree":
        return { frameTree: { frame: { id: "frame-1" } } };
      case "Page.createIsolatedWorld":
        return { executionContextId: 42 };
      case "Runtime.evaluate":
        // Real CDP returns an envelope {result: {value}}; page.js unwraps
        // reply.result.value, so the fake must too.
        return { result: { value: await this.evaluate(params.expression) } };
      case "Runtime.callFunctionOn":
        return { result: { value: await this.callFunctionOn(params) } };
      case "DOM.getDocument":
        return { root: { nodeId: this.nodeIdFor(this.doc.root) } };
      case "DOM.getNodeForLocation": {
        if (this.hitError) throw new Error(this.hitError);
        const el = this.hitElement;
        if (!el) return {};
        return { nodeId: this.nodeIdFor(el) };
      }
      case "DOM.resolveNode": {
        if (this.resolveFails) return {};
        const nodeId = params.nodeId;
        for (const [el, id] of this.nodeIds) {
          if (id === nodeId) {
            const objectId = `obj-${this.nextObjectId++}`;
            this.objectIds.set(objectId, el);
            return { object: { objectId, className: el.tagName } };
          }
        }
        return {};
      }
      case "Input.dispatchMouseEvent":
        this.inputDispatches.push({ type: "mouse", params });
        return {};
      case "Input.dispatchKeyEvent":
        this.inputDispatches.push({ type: "key", params });
        return {};
      case "Input.insertText":
        this.inputDispatches.push({ type: "insertText", params });
        return {};
      default:
        throw new Error(`fake: unexpected CDP method ${method}`);
    }
  }

  // Runtime.evaluate: the probes in page.js are self-contained function
  // expressions; recognize them by their declared function name and
  // evaluate the same semantics against the fake DOM.
  async evaluate(expression) {
    if (this.evaluateHandler) return this.evaluateHandler(expression, this);
    if (expression.includes("__zatitiProbe")) {
      return this.probe(this.probeSelector(expression));
    }
    if (expression.includes("__zatitiFocus")) {
      return this.focusProbe(this.probeSelector(expression));
    }
    if (expression.includes("FerroAdapter")) {
      return this.adapterPerform(expression);
    }
    throw new Error(`fake: unhandled evaluate ${expression.slice(0, 80)}`);
  }

  // The probe expressions end with `})(JSON.stringify(selector))`, so the
  // argument is the last balanced JSON string before the final `))`.
  probeSelector(expression) {
    const start = expression.lastIndexOf("(");
    const arg = expression.slice(start + 1, expression.lastIndexOf(")")).trim();
    return JSON.parse(arg);
  }

  probe(selector) {
    let els;
    try {
      els = this.doc.querySelectorAll(selector);
    } catch (error) {
      return { ok: false, code: "invalid_selector", error: String(error.message) };
    }
    if (els.length === 0) return { ok: false, code: "no_match" };
    if (els.length > 1) return { ok: false, code: "ambiguous" };
    const el = els[0];
    return {
      ok: true,
      url: "https://example.test/page",
      facts: factsOf(el, "https://example.test/page"),
      rect: { x: 10, y: 20, width: 100, height: 30 },
    };
  }

  focusProbe(selector) {
    const base = this.probe(selector);
    if (!base.ok) return base;
    const els = this.doc.querySelectorAll(selector);
    const el = els[0];
    this.doc.focus(el);
    const active = this.doc.activeElement;
    return {
      ...base,
      focused: active === el,
      activeFacts: factsOf(active, "https://example.test/page"),
      facts: factsOf(el, "https://example.test/page"),
    };
  }

  adapterPerform(expression) {
    const opMatch = /"op":"([a-z_]+)"/.exec(expression);
    if (!opMatch) return { error: "fake: no op in adapter expression" };
    const op = opMatch[1];
    if (op === "snapshot") {
      return { snapshot: { url: "https://example.test/page", title: "Example", elements: [] } };
    }
    if (op === "extract") {
      const fieldsMatch = /"fields":(\{[^}]*\})/.exec(expression);
      const fields = fieldsMatch ? JSON.parse(fieldsMatch[1]) : null;
      if (fields) {
        const out = {};
        for (const key of Object.keys(fields)) out[key] = `value-for-${key}`;
        return { result: out };
      }
      return { result: "raw page text" };
    }
    if (op === "select") {
      const valueMatch = /"value":"([^"]*)"/.exec(expression);
      return { selected: valueMatch ? valueMatch[1] : "" };
    }
    if (op === "scroll") {
      const toMatch = /"to":"([^"]*)"/.exec(expression);
      return { scrolledTo: toMatch ? toMatch[1] : "" };
    }
    return {};
  }

  callFunctionOn({ objectId, functionDeclaration }) {
    const el = this.objectIds.get(objectId);
    if (!el) throw new Error("fake: unknown objectId");
    const isContains = functionDeclaration.includes("__zatitiContains");
    if (!isContains) throw new Error("fake: unhandled callFunctionOn");
    // The expression ends with `.call(this, JSON.stringify(selector))`;
    // the argument is the last JSON string in the declaration.
    const selectorMatch = /"((?:[^"\\]|\\.)*)"\s*\)\s*$/.exec(functionDeclaration);
    const selector = selectorMatch ? JSON.parse(`"${selectorMatch[1]}"`) : null;
    if (!selector) throw new Error("fake: callFunctionOn without a selector");
    // __zatitiContains semantics: text nodes fall to their parent; the
    // hit must equal the target or be contained by it.
    const hit = el.nodeType === 3 ? el.parent : el;
    let target = null;
    try {
      target = this.doc.querySelectorAll(selector)[0] ?? null;
    } catch {
      return false;
    }
    return !!target && (target === hit || target.contains(hit));
  }
}

// factsOf mirrors the probe's fact extraction closely enough for the
// fixtures: name from aria-label/placeholder, absolute href, form action
// from the closest form, and the sensitive-relevant fields.
export function factsOf(el, pageUrl) {
  if (!el || el.nodeType !== 1) return null;
  const form = (() => {
    for (let node = el.parent; node; node = node.parent) {
      if (node.tagName === "FORM") return node;
    }
    return null;
  })();
  const href = el.tagName === "A" && el.getAttribute("href")
    ? new URL(el.getAttribute("href"), pageUrl).href
    : "";
  const formAction = form && form.getAttribute("action")
    ? new URL(form.getAttribute("action"), pageUrl).href
    : "";
  return {
    tag: el.tagName.toLowerCase(),
    role: el.getAttribute("role") || "",
    name: el.getAttribute("aria-label") || el.getAttribute("placeholder") || "",
    text: el.attrs["__text"] ?? "",
    href,
    formAction,
    inputType: el.tagName === "INPUT" ? String(el.attrs.type ?? "") : "",
    autocomplete: el.getAttribute("autocomplete") || "",
    inputName: el.getAttribute("name") || "",
    inputId: el.id || "",
    ariaLabel: el.getAttribute("aria-label") || "",
    placeholder: el.getAttribute("placeholder") || "",
  };
}
