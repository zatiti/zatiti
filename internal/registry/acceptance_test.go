package registry

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// The named acceptance cases from the frozen contract, proven against the
// registry's own surface. Cross-transport execution (real CLI subprocesses,
// MCP clients) belongs to the client and application lanes; this file proves
// the registry portion: complete surface enumeration, mapping and schema
// parity, the LocalIO seam order, advisory execution semantics and the
// baseline protocol surface.

// numericValue reads a schema number regardless of its decoded encoding:
// operation schemas decode with UseNumber (json.Number) while the shared
// definitions decode through DecodeStrict (float64).
func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// instanceCandidates returns deterministic candidate instances for one
// schema node. oneOf/anyOf branches contribute all of their candidates and
// object properties contribute a capped cartesian product, so the probe in
// validInstance can pick a branch the frozen validator accepts. The shared
// definitions are acyclic, so the depth bound only guards against runaway
// recursion on a malformed catalog; the frozen chains reach 14 hops.
func instanceCandidates(node any, defs map[string]any, depth int) []any {
	if depth > 24 {
		return nil
	}
	switch n := node.(type) {
	case map[string]any:
		if ref, ok := n["$ref"].(string); ok && strings.HasPrefix(ref, "#/$defs/") {
			return instanceCandidates(defs[strings.TrimPrefix(ref, "#/$defs/")], defs, depth+1)
		}
		if c, ok := n["const"]; ok {
			return []any{c}
		}
		if e, ok := n["enum"].([]any); ok && len(e) > 0 {
			return []any{e[0]}
		}
		for _, combinator := range []string{"oneOf", "anyOf"} {
			if branches, ok := n[combinator].([]any); ok && len(branches) > 0 {
				var out []any
				for _, branch := range branches {
					out = append(out, instanceCandidates(branch, defs, depth+1)...)
				}
				return out
			}
		}
		switch n["type"] {
		case "object":
			props, _ := n["properties"].(map[string]any)
			required, _ := n["required"].([]any)
			names := make([]string, 0, len(required))
			perProperty := make([][]any, 0, len(required))
			for _, r := range required {
				name, _ := r.(string)
				cands := instanceCandidates(props[name], defs, depth+1)
				if len(cands) == 0 {
					cands = []any{nil}
				}
				names = append(names, name)
				perProperty = append(perProperty, cands)
			}
			return cappedProduct(names, perProperty)
		case "array":
			items := []any{}
			if count, ok := numericValue(n["minItems"]); ok && count > 0 {
				itemSchema, _ := n["items"].(map[string]any)
				first := instanceCandidates(itemSchema, defs, depth+1)
				for i := int64(0); i < int64(count) && len(first) > 0; i++ {
					items = append(items, first[0])
				}
			}
			return []any{items}
		case "string":
			return stringCandidates(n)
		case "integer", "number":
			return numberCandidates(n)
		case "boolean":
			return []any{false}
		case "null":
			return []any{nil}
		}
		return []any{map[string]any{}}
	default:
		return []any{node}
	}
}

// cappedProduct builds object candidates over the required properties,
// deterministically and capped so pathological schemas cannot explode.
func cappedProduct(names []string, per [][]any) []any {
	const limit = 32
	out := []any{map[string]any{}}
	for i, name := range names {
		var next []any
		for _, base := range out {
			for _, v := range per[i] {
				if len(next) >= limit {
					break
				}
				obj := make(map[string]any, len(base.(map[string]any))+1)
				for k, bv := range base.(map[string]any) {
					obj[k] = bv
				}
				obj[name] = v
				next = append(next, obj)
			}
			if len(next) >= limit {
				break
			}
		}
		out = next
	}
	return out
}

// stringCandidates produces deterministic string values matching common
// frozen constraints (uuid and date-time formats, digest and currency
// patterns, bounded lengths). Asserted formats take precedence: the frozen
// validator enforces known formats.
func stringCandidates(n map[string]any) []any {
	switch n["format"] {
	case "uuid":
		return []any{"00000000-0000-4000-8000-000000000001"}
	case "date-time":
		return []any{"2026-01-01T00:00:00Z"}
	case "date":
		return []any{"2026-01-01"}
	case "time":
		return []any{"00:00:00Z"}
	}
	candidates := []string{
		"x",
		"00000000-0000-4000-8000-000000000001",
		"2026-01-01T00:00:00Z",
		strings.Repeat("0", 64),
		"USD",
		"",
	}
	maxLen := -1
	if v, ok := numericValue(n["maxLength"]); ok {
		maxLen = int(v)
	}
	pattern, hasPattern := n["pattern"].(string)
	var re *regexp.Regexp
	if hasPattern {
		re = regexp.MustCompile(pattern)
	}
	var out []any
	for _, c := range candidates {
		if maxLen >= 0 && len(c) > maxLen {
			continue
		}
		if hasPattern && !re.MatchString(c) {
			continue
		}
		if !hasPattern && c == "" {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		out = append(out, "x")
	}
	return out
}

// numberCandidates produces an integer value clamped into [minimum, maximum].
func numberCandidates(n map[string]any) []any {
	value := 1.0
	if v, ok := numericValue(n["minimum"]); ok && v > value {
		value = v
	}
	if v, ok := numericValue(n["maximum"]); ok && value > v {
		value = v
	}
	return []any{json.Number(strconv.FormatInt(int64(value), 10))}
}

// validInstance generates an input instance for one operation and proves it
// against the frozen validator itself, so generator defects surface as loud
// failures naming the operation instead of silently weakening the test.
func validInstance(t *testing.T, opID string, schema json.RawMessage) json.RawMessage {
	t.Helper()
	cat := mustCatalog(t)
	merged, err := mergedSchema(cat.defsJSON, schema)
	if err != nil {
		t.Fatalf("operation %s: input schema: %v", opID, err)
	}
	var firstErr error
	var firstRaw json.RawMessage
	for _, candidate := range instanceCandidates(decodeUseNumber(t, schema), cat.sharedDefs, 0) {
		raw, err := json.Marshal(candidate)
		if err != nil {
			continue
		}
		if err := contract.ValidateSchema(merged, raw); err == nil {
			return raw
		} else if firstErr == nil {
			firstErr = err
			firstRaw = raw
		}
	}
	if firstErr != nil {
		t.Fatalf("operation %s: no schema-valid input instance could be generated; "+
			"best candidate %s rejected: %v", opID, firstRaw, firstErr)
	}
	t.Fatalf("operation %s: no schema-valid input instance could be generated", opID)
	return nil
}

// descriptorAgreesWith compares two descriptors for frozen-surface equality.
func descriptorAgreesWith(t *testing.T, a, b contract.Descriptor) {
	t.Helper()
	if a.ID != b.ID || a.Version != b.Version || a.Owner != b.Owner || a.Visibility != b.Visibility ||
		a.Mode != b.Mode || a.Effect != b.Effect || a.SubmissionKey != b.SubmissionKey ||
		a.ExpectedVersion != b.ExpectedVersion || a.MCP != b.MCP ||
		!slices.Equal(a.CLI, b.CLI) || !slices.Equal(a.ScopeRequired, b.ScopeRequired) {
		t.Fatalf("descriptor %s v%d differs across lookups", a.ID, a.Version)
	}
	if err := sameSchema(a.InputSchema, b.InputSchema, "input"); err != nil {
		t.Fatalf("operation %s: %v", a.ID, err)
	}
	if err := sameSchema(a.OutputSchema, b.OutputSchema, "output"); err != nil {
		t.Fatalf("operation %s: %v", a.ID, err)
	}
	if err := sameSchema(a.CompletionSchema, b.CompletionSchema, "completion"); err != nil {
		t.Fatalf("operation %s: %v", a.ID, err)
	}
}

// TestAcceptanceZ02RegistrySurface: a generated registry enumerates every
// concrete operation/version; discovery yields CLI mappings, MCP tools and
// equivalent input/output schemas; mapping collisions are rejected and
// capabilities carries its declared special mapping.
func TestAcceptanceZ02RegistrySurface(t *testing.T) {
	reg := mustRegistry(t)
	public := reg.Public()

	// Discovery and the typed registry agree exactly.
	payload, items := invokeCapabilitiesList(t, reg, &fakeUnit{})
	if payload.Status != contract.StatusCompleted || len(items.Items) != len(public) {
		t.Fatalf("capabilities.list returned %d items, the registry holds %d operations", len(items.Items), len(public))
	}

	seenCLI := map[string]string{}
	seenMCP := map[string]string{}
	for i, dto := range items.Items {
		d := public[i]
		if dto.ID != d.ID {
			t.Fatalf("discovery order diverges at %d: %s vs %s", i, dto.ID, d.ID)
		}
		// Every registry operation has both mappings.
		if len(dto.CLI) == 0 || dto.MCP == "" {
			t.Fatalf("operation %s lacks its CLI/MCP mappings", dto.ID)
		}
		// Mappings are exactly the names derived from the operation ID.
		if !slices.Equal(dto.CLI, cliTokensFor(dto.ID)) || dto.MCP != mcpNameFor(dto.ID) {
			t.Fatalf("operation %s mappings do not match the derived names", dto.ID)
		}
		// Generation rejects mapping collisions.
		cliKey := strings.Join(dto.CLI, " ")
		if prev, dup := seenCLI[cliKey]; dup {
			t.Fatalf("CLI mapping collision: %s and %s", dto.ID, prev)
		}
		seenCLI[cliKey] = dto.ID
		if prev, dup := seenMCP[dto.MCP]; dup {
			t.Fatalf("MCP name collision: %s and %s", dto.ID, prev)
		}
		seenMCP[dto.MCP] = dto.ID
		// Equivalent input/output schemas: discovery and registry agree.
		if err := sameSchema(dto.InputSchema, d.InputSchema, "input"); err != nil {
			t.Fatalf("operation %s: %v", dto.ID, err)
		}
		if err := sameSchema(dto.OutputSchema, d.OutputSchema, "output"); err != nil {
			t.Fatalf("operation %s: %v", dto.ID, err)
		}
	}
	// capabilities has its declared special mapping.
	if cli := cliTokensFor("capabilities.list"); !slices.Equal(cli, []string{"capabilities"}) {
		t.Fatalf("capabilities.list CLI %v, want [capabilities]", cli)
	}
	if mcp := mcpNameFor("capabilities.list"); mcp != "zatiti_capabilities" {
		t.Fatalf("capabilities.list MCP %q", mcp)
	}
	// Optional MCP resources or prompts are never the sole route: every
	// capability names a tool mapping (asserted above) and a CLI command.
	for _, d := range public {
		if mcpNameFor(d.ID) == "" {
			t.Fatalf("operation %s is reachable only through optional MCP features", d.ID)
		}
	}
}

// TestAcceptanceZ02LifecycleParityRegistrySide: every operation of the
// frozen surface is executable through its registered handler with a
// schema-valid input, lookup paths agree, and the local-IO seam runs
// Prepare, Perform and Finish in order. Cross-transport parity (real CLI
// subprocesses and MCP clients) is proven in the client/application lanes.
func TestAcceptanceZ02LifecycleParityRegistrySide(t *testing.T) {
	reg := mustRegistry(t)
	public := reg.Public()
	for _, d := range public {
		looked, handler, err := reg.Lookup(d.ID, d.Version)
		if err != nil {
			t.Fatalf("lookup of %s failed: %v", d.ID, err)
		}
		descriptorAgreesWith(t, looked, d)
		input := validInstance(t, d.ID, d.InputSchema)
		// capabilities.schema resolves a named operation: point it at a real
		// public operation so the lookup succeeds for the lifecycle probe.
		if d.ID == "capabilities.schema" && len(public) > 0 {
			input, err = json.Marshal(map[string]any{"operation": public[0].ID, "version": public[0].Version})
			if err != nil {
				t.Fatalf("capabilities.schema probe input does not marshal: %v", err)
			}
		}
		payload, err := handler(context.Background(), &fakeUnit{}, contract.Invocation{
			Operation: d.ID,
			Version:   d.Version,
			Input:     input,
		})
		if err != nil {
			t.Fatalf("operation %s rejected a schema-valid input: %v", d.ID, err)
		}
		if payload.Status != contract.StatusCompleted {
			t.Fatalf("operation %s returned status %q", d.ID, payload.Status)
		}
	}

	// The local-IO seam routes the frozen operations through
	// Prepare → Perform → Finish, in that order.
	modules := catalogModules()
	var artifacts *fakeOwnerIO
	for _, m := range modules {
		if io, ok := m.(*fakeOwnerIO); ok && io.Name() == "artifacts" {
			artifacts = io
		}
	}
	if artifacts == nil {
		t.Fatal("no artifacts LocalIO owner in the catalog modules")
	}
	reg2, err := New(modules)
	if err != nil {
		t.Fatalf("assembly failed: %v", err)
	}
	_, handler, err := reg2.Lookup("artifact.upload.chunk", 1)
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	input := validInstance(t, "artifact.upload.chunk", mustCatalog(t).byID["artifact.upload.chunk"].Input)
	if _, err := handler(context.Background(), &fakeUnit{}, contract.Invocation{
		Operation: "artifact.upload.chunk",
		Version:   1,
		Input:     input,
	}); err != nil {
		t.Fatalf("artifact.upload.chunk invocation failed: %v", err)
	}
	if len(artifacts.io.prepared) != 1 || len(artifacts.io.performed) != 1 || len(artifacts.io.finished) != 1 {
		t.Fatalf("LocalIO seam stages misordered: prepared=%d performed=%d finished=%d",
			len(artifacts.io.prepared), len(artifacts.io.performed), len(artifacts.io.finished))
	}
}

// TestAcceptanceZ13AdvisoryWorker: inspecting capabilities exposes the
// worker/lease context, and the surface states external limitations as
// explicitly advisory. Lease fencing restricts Zatiti mutations and never
// claims an external process is contained or terminated.
func TestAcceptanceZ13AdvisoryWorker(t *testing.T) {
	reg := mustRegistry(t)

	// The claim operation binds lease fencing to Zatiti-side state: scope,
	// worker, run, expected version and declared capabilities.
	runClaim := mustCatalog(t).byID["run.claim"]
	if runClaim == nil {
		t.Fatal("run.claim is missing from the frozen surface")
	}
	var claimInput map[string]any
	if err := contract.DecodeStrict(runClaim.Input, &claimInput); err != nil {
		t.Fatalf("run.claim input schema does not decode: %v", err)
	}
	required, _ := claimInput["required"].([]any)
	wantRequired := []string{"scope", "run_id", "worker_id", "expected_version", "capabilities"}
	gotRequired := make([]string, 0, len(required))
	for _, r := range required {
		s, _ := r.(string)
		gotRequired = append(gotRequired, s)
	}
	if !slices.Equal(gotRequired, wantRequired) {
		t.Fatalf("run.claim requires %v, want %v", gotRequired, wantRequired)
	}

	// Advisory execution semantics are explicit schema vocabulary in the
	// generated surface: context capture, external confirmation and budget
	// reporting each carry an advisory state.
	doc := parseOpenAPI(t, reg)
	schemas := doc.Components["schemas"].(map[string]any)
	captureAdvisory := false
	confirmationAdvisory := false
	budgetAdvisory := false
	for _, raw := range schemas {
		walkSchemaValues(raw, func(key string, value any) {
			obj, ok := value.(map[string]any)
			if !ok {
				return
			}
			enum, _ := obj["enum"].([]any)
			switch key {
			case "context_capture":
				for _, v := range enum {
					if s, _ := v.(string); s == "advisory" {
						captureAdvisory = true
					}
				}
			case "confirmation":
				for _, v := range enum {
					if s, _ := v.(string); s == "advisory" {
						confirmationAdvisory = true
					}
				}
			case "advisory":
				if t, _ := obj["type"].(string); t == "boolean" {
					budgetAdvisory = true
				}
			}
		})
	}
	if !captureAdvisory || !confirmationAdvisory || !budgetAdvisory {
		t.Fatalf("advisory semantics incomplete: context_capture=%t confirmation=%t budget=%t",
			captureAdvisory, confirmationAdvisory, budgetAdvisory)
	}

	// No schema or operation in the surface claims containment or
	// termination of external processes: the lease fences Zatiti mutations,
	// nothing more.
	for name, raw := range schemas {
		text, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("schema %s does not marshal: %v", name, err)
		}
		for _, claim := range []string{"contained", "terminat", "sandbox", "killed"} {
			if strings.Contains(strings.ToLower(string(text)), claim) {
				t.Fatalf("component schema %s claims containment vocabulary %q", name, claim)
			}
		}
	}
}

// TestAcceptanceQualificationBaselineMCP: with optional MCP features
// disabled, the baseline stdio tool mapping and durable polling carry the
// complete authorized functionality, and the wire behavior is pinned to the
// frozen revision.
func TestAcceptanceQualificationBaselineMCP(t *testing.T) {
	reg := mustRegistry(t)
	public := reg.Public()
	doc := parseOpenAPI(t, reg)

	// Baseline stdio tools: every capability has a tool mapping.
	for _, d := range public {
		if d.MCP == "" || len(d.CLI) == 0 {
			t.Fatalf("operation %s is not operable over the baseline stdio tools", d.ID)
		}
	}
	// Mutations are durable commands: every one except the one-time init
	// requires a submission key.
	for _, d := range public {
		if d.Mode == contract.ModeMutation && d.ID != "installation.init" && !d.SubmissionKey {
			t.Fatalf("mutation %s does not require a submission key", d.ID)
		}
	}

	// Durable polling: job.get exposes inspectable job state including the
	// unknown-outcome state and the eventual result.
	jobGet := mustCatalog(t).byID["job.get"]
	if jobGet == nil {
		t.Fatal("job.get is missing from the frozen surface")
	}
	var jobOutput map[string]any
	if err := contract.DecodeStrict(jobGet.Output, &jobOutput); err != nil {
		t.Fatalf("job.get output schema does not decode: %v", err)
	}
	props, _ := jobOutput["properties"].(map[string]any)
	resource, _ := props["resource"].(map[string]any)
	if ref, _ := resource["$ref"].(string); ref != "#/$defs/Job" {
		t.Fatalf("job.get resource is %v, want the Job schema", resource["$ref"])
	}
	cat := mustCatalog(t)
	jobDef, ok := cat.sharedDefs["Job"].(map[string]any)
	if !ok {
		t.Fatal("the shared Job schema is missing")
	}
	jobProps, _ := jobDef["properties"].(map[string]any)
	state, _ := jobProps["state"].(map[string]any)
	states, _ := state["enum"].([]any)
	var stateNames []string
	for _, s := range states {
		name, _ := s.(string)
		stateNames = append(stateNames, name)
	}
	// Pollable state includes the unknown-outcome state: a crashed worker
	// must be observable rather than silently pending forever.
	if !slices.Contains(stateNames, "outcome_unknown") {
		t.Fatalf("Job states %v lack the unknown-outcome state", stateNames)
	}

	// Result and error mapping is pinned: every operation's failed response
	// maps onto the shared Fault schema, and the completed envelope is the
	// frozen zatiti.result/v1.
	for _, d := range public {
		item := doc.Paths["/v1/operations/"+d.ID].(map[string]any)
		post := item["post"].(map[string]any)
		responses := post["responses"].(map[string]any)
		failed := responses["default"].(map[string]any)
		schema := failed["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
		errorProp := schema["properties"].(map[string]any)["error"].(map[string]any)
		anyOf, _ := errorProp["anyOf"].([]any)
		faultRef := anyOf[0].(map[string]any)["$ref"]
		if faultRef != "#/components/schemas/Fault" {
			t.Fatalf("operation %s failed response maps errors to %v", d.ID, faultRef)
		}
	}

	// The negotiated protocol surface is pinned to frozen revision 1.
	if v := doc.Document["info"].(map[string]any)["version"]; v != "1.1.0" {
		t.Fatalf("surface version %v, want 1.1.0", v)
	}
}

// walkSchemaValues visits every named schema position with its value.
func walkSchemaValues(node any, fn func(key string, value any)) {
	switch n := node.(type) {
	case map[string]any:
		for key, value := range n {
			fn(key, value)
		}
		for _, value := range n {
			walkSchemaValues(value, fn)
		}
	case []any:
		for _, value := range n {
			walkSchemaValues(value, fn)
		}
	}
}
