package serenity

// The pin this adapter build was written against. PROTOCOL.md records the
// inspection; every value here must agree with it. A profile naming any other
// source is unqualified and is refused.
const (
	pinnedModule   = "github.com/sirerun/serenity"
	pinnedCommit   = "f5a5154e1c4d808e10b495fca3bd50d842f0aa92"
	pinnedDescribe = "v0.1.1-240-gf5a5154"

	// pinnedProtocolRevision names the served memory protocol and its only
	// transports' protocol: MEMORY_VERBS v1 over MCP 2025-11-25.
	pinnedProtocolRevision = "memory_verbs/1+mcp/2025-11-25"

	// adapterVersion identifies this adapter build in capability evidence.
	adapterVersion = "zatiti-serenity-adapter/1"
)

// Action kinds. These are the only values the frozen
// zatiti.serenity.action/v1 oneOf schema accepts.
const (
	kindRecall         = "recall"
	kindRemember       = "remember"
	kindInspect        = "inspect"
	kindPromote        = "promote"
	kindRetract        = "retract"
	kindExportRevision = "export_revision"
)

// Named upstream gaps. Each is a row of PROTOCOL.md's capability table; the
// names are stable so a refusal can be matched to the requirement it blocks.
const (
	gapSingleRequestCallPath  = "single_request_call_path"
	gapUUIDClaimIdentity      = "uuid_claim_identity"
	gapClaimConfidenceFresh   = "claim_confidence_and_freshness"
	gapFetchClaimByID         = "fetch_claim_by_id"
	gapPromotionLineage       = "promotion_lineage"
	gapCommandIdentity        = "command_identity"
	gapCommandStatusLookup    = "command_status_lookup"
	gapIdempotentReplay       = "idempotent_replay"
	gapCostBound              = "cost_bound_enforcement"
	gapDisclosureDestinations = "disclosure_destination_enforcement"
	gapMinimumFreshness       = "minimum_freshness_enforcement"
	gapSourceRevision         = "source_revision_report"
	gapIndexRevision          = "index_revision_report"
	gapRevisionExport         = "revision_export"
	gapRestore                = "revision_restore"
	gapReadFacade             = "go_read_facade"
	gapSingleWriterEnforced   = "single_writer_enforcement"
	gapVerifiableCommit       = "verifiable_running_commit"
)

// operationCapability is one row of the writer capability report.
type operationCapability struct {
	Kind string `json:"kind"`
	// Supported is true only when the pinned upstream provides everything
	// the frozen action and evidence schemas need for this kind.
	Supported bool `json:"supported"`
	// UpstreamVerb is the closest MEMORY_VERBS v1 tool, or empty when the
	// pinned upstream has no related call at all.
	UpstreamVerb string `json:"upstream_verb,omitempty"`
	// Missing names the upstream gaps that block this kind.
	Missing []string `json:"missing"`
}

// pinnedOperations is the single source of truth for what this adapter may
// dispatch. No kind is supported at this pin: gapSingleRequestCallPath alone
// blocks every one, because a Serenity tool call needs a three-request MCP
// session handshake and one Invoke is exactly one physical request.
func pinnedOperations() []operationCapability {
	return []operationCapability{
		{Kind: kindRecall, UpstreamVerb: "recall", Missing: []string{
			gapSingleRequestCallPath, gapUUIDClaimIdentity, gapClaimConfidenceFresh,
			gapMinimumFreshness, gapCostBound, gapDisclosureDestinations}},
		{Kind: kindRemember, UpstreamVerb: "remember", Missing: []string{
			gapSingleRequestCallPath, gapUUIDClaimIdentity, gapClaimConfidenceFresh,
			gapCommandIdentity, gapCommandStatusLookup, gapIdempotentReplay}},
		{Kind: kindInspect, Missing: []string{
			gapSingleRequestCallPath, gapFetchClaimByID, gapUUIDClaimIdentity,
			gapClaimConfidenceFresh, gapMinimumFreshness}},
		{Kind: kindPromote, Missing: []string{
			gapSingleRequestCallPath, gapPromotionLineage, gapUUIDClaimIdentity,
			gapClaimConfidenceFresh, gapCommandIdentity, gapCommandStatusLookup, gapIdempotentReplay}},
		{Kind: kindRetract, UpstreamVerb: "forget", Missing: []string{
			gapSingleRequestCallPath, gapUUIDClaimIdentity, gapCommandIdentity, gapCommandStatusLookup}},
		{Kind: kindExportRevision, Missing: []string{
			gapSingleRequestCallPath, gapSourceRevision, gapRevisionExport}},
	}
}

// pinnedUnsupportedGuarantees lists every cross-cutting guarantee the frozen
// profile can claim that the pinned upstream does not provide. loadProfile
// refuses a profile that claims any of them.
func pinnedUnsupportedGuarantees() []string {
	return []string{
		gapCommandIdentity, gapCommandStatusLookup, gapIdempotentReplay,
		gapCostBound, gapDisclosureDestinations,
		gapSourceRevision, gapIndexRevision, gapMinimumFreshness, gapReadFacade,
		gapRevisionExport, gapRestore,
		gapSingleWriterEnforced, gapVerifiableCommit,
	}
}

// capabilityFor returns the report row for kind.
func capabilityFor(kind string) (operationCapability, bool) {
	for _, op := range pinnedOperations() {
		if op.Kind == kind {
			return op, true
		}
	}
	return operationCapability{}, false
}

type upstreamPin struct {
	Module           string `json:"module"`
	Commit           string `json:"commit"`
	Describe         string `json:"describe"`
	ProtocolRevision string `json:"protocol_revision"`
}

// capabilityReport is the writer capability report published in Contract().
type capabilityReport struct {
	Schema                string                `json:"schema"`
	AdapterVersion        string                `json:"adapter_version"`
	Upstream              upstreamPin           `json:"upstream"`
	Operations            []operationCapability `json:"operations"`
	UnsupportedGuarantees []string              `json:"unsupported_guarantees"`
	// PhysicalCalls states what this build can send: "none". The adapter
	// holds no HTTP client, so the statement is structural, not a promise.
	PhysicalCalls string `json:"physical_calls"`
	// CommandLookup and LostAcknowledgement state the Reconcile semantics.
	CommandLookup       string `json:"command_lookup"`
	LostAcknowledgement string `json:"lost_acknowledgement"`
}

func buildCapabilityReport() capabilityReport {
	return capabilityReport{
		Schema:         "zatiti.serenity.capability-report/v1",
		AdapterVersion: adapterVersion,
		Upstream: upstreamPin{
			Module: pinnedModule, Commit: pinnedCommit, Describe: pinnedDescribe,
			ProtocolRevision: pinnedProtocolRevision,
		},
		Operations:            pinnedOperations(),
		UnsupportedGuarantees: pinnedUnsupportedGuarantees(),
		PhysicalCalls:         "none",
		CommandLookup:         "unsupported",
		LostAcknowledgement:   "retained_unknown",
	}
}
