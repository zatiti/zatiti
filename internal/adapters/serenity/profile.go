package serenity

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/zatiti/zatiti/internal/contract"
)

// pinnedRoute is the only route the pinned upstream serves MEMORY_VERBS on.
const pinnedRoute = "/mcp"

// serenityProfile is the decoded, validated zatiti.serenity/v2 adapter
// profile, with brain mappings indexed for lookup at Invoke time.
type serenityProfile struct {
	Brains             map[contract.ID]wireBrainMapping
	Enforcement        wireBoundEnforcement
	CapabilityEvidence wireCapabilityEvidence
	Digest             contract.Digest
}

// loadProfile validates raw against the zatiti.serenity/v2 schema, strict
// decodes it, and checks three things the schema cannot express:
//
//   - capability_evidence.profile_digest binds this exact profile: the digest
//     of the canonical profile with the capability_evidence field itself
//     omitted. Evidence that does not bind its own bytes grants no authority.
//   - The profile names exactly the upstream source and protocol this build
//     was written against (see PROTOCOL.md). Any other source is unqualified.
//   - The profile claims nothing the pinned upstream does not provide. A
//     profile is trusted local configuration that the memory and effects
//     owners read independently, so an overclaim is refused here rather than
//     silently ignored at Invoke.
func loadProfile(raw json.RawMessage) (*serenityProfile, error) {
	schema, err := profileSchema()
	if err != nil {
		return nil, internalError("serenity profile schema composition failed: %v", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("serenity profile does not match the zatiti.serenity/v2 schema: %v", err)
	}
	var w wireSerenityProfile
	if err := contract.DecodeStrict(raw, &w); err != nil {
		return nil, invalidInput("serenity profile decode failed: %v", err)
	}

	digest, err := profileDigestWithoutCapabilityEvidence(raw)
	if err != nil {
		return nil, invalidInput("serenity profile canonicalization failed: %v", err)
	}
	if digest != w.CapabilityEvidence.ProfileDigest {
		return nil, invalidInput(
			"serenity profile capability_evidence.profile_digest %q does not bind this exact profile (computed %q); self-referential capability evidence grants no authority",
			w.CapabilityEvidence.ProfileDigest, digest)
	}

	if err := checkPin(&w); err != nil {
		return nil, err
	}
	if err := checkClaims(&w); err != nil {
		return nil, err
	}
	brains, err := indexBrains(w.BrainMappings)
	if err != nil {
		return nil, err
	}

	return &serenityProfile{
		Brains:             brains,
		Enforcement:        w.Enforcement,
		CapabilityEvidence: w.CapabilityEvidence,
		Digest:             digest,
	}, nil
}

// checkPin refuses a profile qualified against any source, protocol or
// adapter build other than this one.
func checkPin(w *wireSerenityProfile) error {
	pins := []struct{ field, got, want string }{
		{"commit", w.Commit, pinnedCommit},
		{"version", w.Version, pinnedDescribe},
		{"capability_evidence.source_revision", w.CapabilityEvidence.SourceRevision, pinnedCommit},
		{"capability_evidence.protocol_revision", w.CapabilityEvidence.ProtocolRevision, pinnedProtocolRevision},
		{"capability_evidence.adapter_version", w.CapabilityEvidence.AdapterVersion, adapterVersion},
	}
	for _, p := range pins {
		if p.got != p.want {
			return capabilityUnsupported(
				"serenity profile %s %q is not the source this adapter build is pinned to (%q); an unpinned upstream is unqualified",
				p.field, p.got, p.want)
		}
	}
	return nil
}

// checkClaims refuses every profile claim the pinned upstream cannot back.
// Each refusal names the claiming field and the gap from the capability
// report.
func checkClaims(w *wireSerenityProfile) error {
	overclaim := func(field, gap string) error {
		return capabilityUnsupported(
			"serenity profile %s claims %s, which the pinned upstream %s@%s does not provide; see PROTOCOL.md",
			field, gap, pinnedModule, pinnedCommit[:12])
	}

	for _, kind := range w.SupportedOperations {
		// The schema enum already bounds kind; an unlisted kind yields the
		// zero row, which is unsupported.
		if op, _ := capabilityFor(kind); !op.Supported {
			return capabilityUnsupported(
				"serenity profile supported_operations lists %q, which the pinned upstream %s@%s cannot serve: missing %v; see PROTOCOL.md",
				kind, pinnedModule, pinnedCommit[:12], op.Missing)
		}
	}
	if len(w.CapabilityEvidence.Capabilities) > 0 {
		return capabilityUnsupported(
			"serenity profile capability_evidence.capabilities lists %v, but this adapter build qualifies no capability against the pinned upstream",
			w.CapabilityEvidence.Capabilities)
	}

	if w.Enforcement.Cost == "enforced" {
		return overclaim("enforcement.cost", gapCostBound)
	}
	if w.Enforcement.Disclosure == "enforced" {
		return overclaim("enforcement.disclosure", gapDisclosureDestinations)
	}

	lookup := w.CommandStatusLookup
	if lookup.Mode != "unsupported" {
		return overclaim("command_status_lookup.mode", gapCommandStatusLookup)
	}
	if lookup.CommandIdentitySupported {
		return overclaim("command_status_lookup.command_identity_supported", gapCommandIdentity)
	}
	if lookup.RetentionSeconds != 0 {
		return overclaim("command_status_lookup.retention_seconds", gapIdempotentReplay)
	}

	fresh := w.Freshness
	if fresh.SourceRevisionSupported {
		return overclaim("freshness.source_revision_supported", gapSourceRevision)
	}
	if fresh.IndexRevisionSupported {
		return overclaim("freshness.index_revision_supported", gapIndexRevision)
	}
	if fresh.MinimumFreshnessEnforceable {
		return overclaim("freshness.minimum_freshness_enforceable", gapMinimumFreshness)
	}
	if fresh.ReadFacade != "unsupported" {
		return overclaim("freshness.read_facade", gapReadFacade)
	}

	backup := w.BackupRevisionProtocol
	if backup.Mode != "unsupported" {
		return overclaim("backup_revision_protocol.mode", gapRevisionExport)
	}
	if backup.ImmutableRevisionExport {
		return overclaim("backup_revision_protocol.immutable_revision_export", gapRevisionExport)
	}
	if backup.RestoreSupported {
		return overclaim("backup_revision_protocol.restore_supported", gapRestore)
	}
	return nil
}

// indexBrains validates both local and hosted identity spaces. Local pins
// identify one brain per endpoint. A hosted OAuth grant selects one project,
// so neither a project nor its connection may alias another local brain.
func indexBrains(mappings []wireBrainMapping) (map[contract.ID]wireBrainMapping, error) {
	brains := make(map[contract.ID]wireBrainMapping, len(mappings))
	endpoints := make(map[string]contract.ID, len(mappings))
	projects := make(map[string]contract.ID, len(mappings))
	connections := make(map[contract.ID]contract.ID, len(mappings))
	for _, m := range mappings {
		if _, dup := brains[m.BrainID]; dup {
			return nil, invalidInput("serenity profile maps brain %s more than once", m.BrainID)
		}
		if m.HostedProjectID != "" {
			if m.Endpoint != "https://serenity.sire.run/mcp" || m.ConnectionID == "" || m.RootRef != "" || m.WriterOwner != "" {
				return nil, invalidInput("serenity hosted brain mapping has mismatched identity fields")
			}
			if other, dup := projects[m.HostedProjectID]; dup {
				return nil, invalidInput("serenity hosted project is mapped by brains %s and %s", other, m.BrainID)
			}
			if other, dup := connections[m.ConnectionID]; dup {
				return nil, invalidInput("serenity hosted connection is mapped by brains %s and %s", other, m.BrainID)
			}
			brains[m.BrainID] = m
			projects[m.HostedProjectID] = m.BrainID
			connections[m.ConnectionID] = m.BrainID
			continue
		}
		if m.ConnectionID != "" || m.RootRef == "" || m.WriterOwner == "" {
			return nil, invalidInput("serenity local brain mapping has mismatched identity fields")
		}
		if err := checkEndpoint(m.Endpoint); err != nil {
			return nil, invalidInput("serenity profile brain %s endpoint is invalid: %v", m.BrainID, err)
		}
		if other, dup := endpoints[m.Endpoint]; dup {
			return nil, invalidInput("serenity profile maps brains %s and %s to one endpoint; the pinned upstream serves one brain per endpoint", other, m.BrainID)
		}
		brains[m.BrainID] = m
		endpoints[m.Endpoint] = m.BrainID
	}
	return brains, nil
}

// checkEndpoint requires a secret-free absolute URL on the pinned route.
// The message never echoes the endpoint, which may have carried a credential.
func checkEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("it is not a URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("host is empty")
	}
	if u.User != nil {
		return fmt.Errorf("it carries user information; profiles are secret-free")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("it carries a query or fragment")
	}
	if u.Path != pinnedRoute {
		return fmt.Errorf("path must be %s, the only route the pinned upstream serves", pinnedRoute)
	}
	return nil
}

// profileDigestWithoutCapabilityEvidence computes the canonical-JSON SHA-256
// digest of raw with its top-level capability_evidence field removed.
func profileDigestWithoutCapabilityEvidence(raw json.RawMessage) (contract.Digest, error) {
	var doc map[string]json.RawMessage
	if err := contract.DecodeStrict(raw, &doc); err != nil {
		return "", err
	}
	delete(doc, "capability_evidence")
	stripped, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal stripped profile: %w", err)
	}
	canon, err := contract.Canonicalize(stripped)
	if err != nil {
		return "", fmt.Errorf("canonicalize stripped profile: %w", err)
	}
	return contract.Hash(canon), nil
}
