// sensitive.js -- T8.2 sensitive-field refusal (SC16).
//
// Refusal covers password, one-time-code, current-password, new-password
// and cc-* fields, for fill, select, key AND extract -- reads are refused
// too (plan.md review fix #3: "sensitive fields refused on reads too").
// Both the adapter (Go side) and this extension enforce it; this module is
// the extension's gate, applied before any input dispatch or value read.
//
// A field is sensitive when ANY of its reported attributes matches:
//   - type="password" (or an inputType of password);
//   - autocomplete containing "current-password", "new-password" or
//     "one-time-code" (the section- prefixed forms included);
//   - an autocomplete or name carrying the cc-* credit-card vocabulary
//     (cc-number, cc-exp, cc-csc, ...);
//   - a name/id/aria-label that names a password or one-time code field.
//
// Pure predicates only -- the caller (page.js / commands.js) feeds it the
// field facts its isolated-world probe gathered. Snapshots never include
// input values, so nothing here ever sees one.

export class SensitiveFieldError extends Error {
  constructor(message) {
    super(message);
    this.name = "SensitiveFieldError";
    this.code = "sensitive_field_refused";
  }
}

const SENSITIVE_AUTOCOMPLETE = [
  "current-password",
  "new-password",
  "one-time-code",
];

// cc-* autocomplete tokens and their common name/id spellings (the bare
// "cardnumber"/"cardnumber"-style spellings included, since sites write
// credit-card fields without the cc- prefix).
const CC_PATTERN = /(^|[^a-z0-9-])cc-(number|exp|exp-month|exp-year|csc|csv|cvv|cvc|name|given-name|family-name|additional-name|postal-code|country|type)([^a-z0-9-]|$)/i;
const CC_NAME_PATTERN = /(^|[^a-z0-9])(cc[-_ ]?)?(number|exp|expmonth|expyear|csc|csv|cvv|cvc|cardnumber|cardexp|cardcsc|card[-_ ]?name|securitycode|security[-_ ]?code)([^a-z0-9]|$)/i;

const PASSWORD_NAME_PATTERN = /password|passwd|pwd|passcode/i;
const OTP_NAME_PATTERN = /one[-_ ]?time[-_ ]?code|otp|verification[-_ ]?code|confirm[^a-z0-9]*code|sms[-_ ]?code|authenticator[-_ ]?code/i;

function containsToken(value, token) {
  // Autocomplete tokens are space-separated and may be section-prefixed
  // ("shipping new-password"); match on the token suffix.
  return String(value ?? "")
    .toLowerCase()
    .split(/\s+/)
    .some((part) => part === token || part.endsWith(`-${token}`));
}

// Decide sensitivity from the field facts an isolated-world probe reported:
// { inputType, autocomplete, name, id, ariaLabel, ... }. Every attribute is
// optional; a field with no identifying facts is not sensitive.
export function isSensitiveField(field) {
  if (!field || typeof field !== "object") return false;
  const type = String(field.inputType ?? field.type ?? "").toLowerCase();
  if (type === "password") return true;
  const autocomplete = String(field.autocomplete ?? "").toLowerCase();
  if (SENSITIVE_AUTOCOMPLETE.some((token) => containsToken(autocomplete, token))) return true;
  if (CC_PATTERN.test(autocomplete)) return true;
  const identifiers = [field.name, field.id, field.ariaLabel, field.placeholder]
    .map((value) => String(value ?? ""));
  if (identifiers.some((value) => CC_NAME_PATTERN.test(value))) return true;
  if (identifiers.some((value) => PASSWORD_NAME_PATTERN.test(value))) return true;
  if (identifiers.some((value) => OTP_NAME_PATTERN.test(value))) return true;
  return false;
}

// Guard for a fill/select/key command: throw SensitiveFieldError when the
// target (or any field the command would touch) is sensitive.
export function refuseSensitiveTarget(field) {
  if (isSensitiveField(field)) {
    throw new SensitiveFieldError("sensitive field refused: password, one-time-code or credit-card field");
  }
}

// Guard for extract: refuse when ANY requested field selector resolves to a
// sensitive element. `fields` maps result-name -> selector (the adapter's
// extract contract); `probes` maps the same selectors (or the same names) to
// the field facts observed for them. A field with no probe is allowed --
// the probe is where password typing is discovered -- but a probe that says
// sensitive fails the whole extract.
export function refuseSensitiveExtract(fields, probes) {
  if (!fields || typeof fields !== "object") return;
  const byKey = probes ?? {};
  for (const [name, selector] of Object.entries(fields)) {
    const probe = byKey[selector] ?? byKey[name];
    if (probe && isSensitiveField(probe)) {
      throw new SensitiveFieldError(`sensitive field refused for extract of ${JSON.stringify(name)}`);
    }
  }
}
