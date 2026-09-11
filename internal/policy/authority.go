package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Authority intersection.
//
// The engine mirrors identity's grant semantics locally (this package cannot
// import sibling domains): a principal's effective authority is the union of
// its unrevoked, unexpired allow grants minus its deny grants, narrowed by
// active restrictions. Identity's _identity.authority output already applies
// revocation and parent-chain liveness filtering; this package rebuilds the
// scope/capability/destination envelope from that output and intersects it
// with standing policy, the configuration snapshot (binding permissions,
// project classification, owner principals) and the worker/task envelope.
//
// Every decision is a pure function of the injected clock, the stored policy
// state and the peer payloads: no map-order dependence, no wall-clock reads
// outside deps.Clock, and deterministically sorted reasons and requirements.

// Supported standing-rule condition keys. Any other key inside an executable
// rule position fails the decision closed.
const (
	condMaxCost                = "max_cost"
	condRequiredClassification = "required_classification"
	condNotBefore              = "not_before"
	condExpiresAt              = "expires_at"
	condRequiredToolVersion    = "required_tool_version"
	condRequiredWorkerID       = "required_worker_id"
	condRequiredReview         = "required_review"
)

// supportedConditionKeys is the fixed evaluation order; conditions run in
// this order regardless of map iteration order so decisions are stable.
var supportedConditionKeys = []string{
	condMaxCost,
	condRequiredClassification,
	condNotBefore,
	condExpiresAt,
	condRequiredToolVersion,
	condRequiredWorkerID,
	condRequiredReview,
}

// reviewRequirementTTL bounds how long one exact review requirement stays
// satisfiable before the requirement must be re-derived.
const reviewRequirementTTL = 24 * time.Hour

// capWildcard is the capability wildcard accepted in grants, restrictions
// and rules.
const capWildcard = "*"

// envelope is the scope/capability/destination authority envelope rebuilt
// from identity's grant list.
type envelope struct {
	orgs, projects, workers, tasks map[contract.ID]bool
	orgWildcard                    bool
	projectWildcard                bool
	workerWildcard                 bool
	taskWildcard                   bool
	caps                           map[string]bool
	dests                          map[string]bool
	capWildcard                    bool
	destWildcard                   bool
	denied                         map[string]bool
	deniedWildcard                 bool
	expires                        *time.Time
}

func newEnvelope() *envelope {
	return &envelope{
		orgs:     map[contract.ID]bool{},
		projects: map[contract.ID]bool{},
		workers:  map[contract.ID]bool{},
		tasks:    map[contract.ID]bool{},
		caps:     map[string]bool{},
		dests:    map[string]bool{},
		denied:   map[string]bool{},
	}
}

// addAllow folds one allow grant into the envelope. An empty destination
// list means the grant is destination-unconstrained.
func (e *envelope) addAllow(g peerGrant) {
	e.dimAllow(g.Scope.OrganizationID, &e.orgWildcard, e.orgs)
	e.dimAllow(g.Scope.ProjectID, &e.projectWildcard, e.projects)
	e.dimAllow(g.Scope.WorkerID, &e.workerWildcard, e.workers)
	e.dimAllow(g.Scope.TaskID, &e.taskWildcard, e.tasks)
	for _, c := range g.Capabilities {
		if c == capWildcard {
			e.capWildcard = true
			continue
		}
		e.caps[c] = true
	}
	if len(g.Destinations) == 0 {
		e.destWildcard = true
	}
	for _, d := range g.Destinations {
		e.dests[d] = true
	}
	if g.ExpiresAt != nil && (e.expires == nil || g.ExpiresAt.Before(*e.expires)) {
		t := *g.ExpiresAt
		e.expires = &t
	}
}

// addDeny folds one deny grant into the denied capability set. A deny grant
// whose scope does not apply to the request scope is ignored: an org-scoped
// denial never reaches an unrelated request, while an unset grant dimension
// or an unset request dimension keeps the denial global.
func (e *envelope) addDeny(g peerGrant, req contract.Scope) {
	if !denyApplies(g, req) {
		return
	}
	for _, c := range g.Capabilities {
		if c == capWildcard {
			e.deniedWildcard = true
			continue
		}
		e.denied[c] = true
	}
}

func (e *envelope) dimAllow(v contract.ID, wildcard *bool, vals map[contract.ID]bool) {
	if v == "" {
		*wildcard = true
		return
	}
	vals[v] = true
}

// dimCover reports whether an envelope dimension covers the request
// dimension: a wildcard covers everything, an empty dimension set is
// unconstrained, and a set dimension matches only the exact request value.
// A request dimension left unset under a set envelope dimension fails, so a
// grant can never be stretched to a broader request.
func dimCover(wildcard bool, vals map[contract.ID]bool, want contract.ID) bool {
	if wildcard || len(vals) == 0 {
		return true
	}
	return want != "" && vals[want]
}

// dimMatches is coverage for stored resource scopes: an empty stored
// dimension is unconstrained, a set one must equal the request dimension.
func dimMatches(have, want contract.ID) bool {
	return have == "" || have == want
}

func (e *envelope) coversScope(req contract.Scope) bool {
	return dimCover(e.orgWildcard, e.orgs, req.OrganizationID) &&
		dimCover(e.projectWildcard, e.projects, req.ProjectID) &&
		dimCover(e.workerWildcard, e.workers, req.WorkerID) &&
		dimCover(e.taskWildcard, e.tasks, req.TaskID)
}

// dimDeny mirrors identity's deny dimension semantics: a deny grant applies
// when the grant dimension is unset (anywhere), the request dimension is
// unset (a broader request is still denied), or the values match exactly.
func dimDeny(have, want contract.ID) bool {
	return have == "" || want == "" || have == want
}

// denyApplies reports whether one deny grant's scope applies to the request.
func denyApplies(g peerGrant, req contract.Scope) bool {
	return dimDeny(g.Scope.OrganizationID, req.OrganizationID) &&
		dimDeny(g.Scope.ProjectID, req.ProjectID) &&
		dimDeny(g.Scope.WorkerID, req.WorkerID) &&
		dimDeny(g.Scope.TaskID, req.TaskID)
}

// hasCap reports whether the envelope's allow set carries the capability
// after deny subtraction.
func (e *envelope) hasCap(c string) bool {
	if e.deniedWildcard || e.denied[c] {
		return false
	}
	return e.capWildcard || e.caps[c]
}

// deniedCap reports whether an explicit deny grant covers the capability.
// Denials are absolute: binding permissions never rescue them.
func (e *envelope) deniedCap(c string) bool {
	return e.deniedWildcard || e.denied[c]
}

// allowsDest reports whether the envelope permits one destination.
func (e *envelope) allowsDest(d string) bool {
	return e.destWildcard || e.dests[d]
}

// buildEnvelope folds identity's effective grants into one envelope against
// the request scope. Grants with an expiry at or before now are dropped
// entirely: an expired grant, allow or deny, no longer expresses current
// authority.
func buildEnvelope(grants []peerGrant, now time.Time, req contract.Scope) *envelope {
	e := newEnvelope()
	for _, g := range grants {
		if g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
			continue
		}
		if g.Denied {
			e.addDeny(g, req)
			continue
		}
		e.addAllow(g)
	}
	return e
}

// Peer helpers.

// callPeer invokes one internal owner operation on the declared ports. The
// call retains the current unit, actor, scope, generation and transaction.
func (s *Service) callPeer(ctx context.Context, unit contract.Unit, op string, input any) (json.RawMessage, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("policy: encode %s call: %w", op, err)
	}
	payload, err := s.deps.Ports.Call(ctx, unit, contract.Invocation{
		Operation: op,
		Version:   descriptorVersion,
		Input:     raw,
	})
	if err != nil {
		return nil, err
	}
	if payload.Error != nil {
		return nil, payload.Error
	}
	return payload.Data, nil
}

// callAuthority reads the actor's current authority from identity. A missing
// principal is reported as a deny decision so callers below treat it like
// any other fence.
func (s *Service) callAuthority(ctx context.Context, unit contract.Unit, principal contract.ID, scope contract.Scope) (authorityResource, error) {
	data, err := s.callPeer(ctx, unit, "_identity.authority", authorityCallInput{
		PrincipalID: principal,
		Scope:       scope,
	})
	if err != nil {
		return authorityResource{}, err
	}
	var body authorityBody
	if err := json.Unmarshal(data, &body); err != nil {
		return authorityResource{}, fmt.Errorf("policy: decode identity authority: %w", err)
	}
	return body.Resource, nil
}

// callSnapshot reads the configuration scope snapshot.
func (s *Service) callSnapshot(ctx context.Context, unit contract.Unit, scope contract.Scope) (scopeSnapshot, error) {
	data, err := s.callPeer(ctx, unit, "_configuration.snapshot", scopeCallInput{Scope: scope})
	if err != nil {
		return scopeSnapshot{}, err
	}
	var body snapshotBody
	if err := json.Unmarshal(data, &body); err != nil {
		return scopeSnapshot{}, fmt.Errorf("policy: decode configuration snapshot: %w", err)
	}
	return body.Resource, nil
}

// callTaskSnapshot reads one task's current state from tasks.
func (s *Service) callTaskSnapshot(ctx context.Context, unit contract.Unit, scope contract.Scope, id contract.ID) (peerTask, error) {
	data, err := s.callPeer(ctx, unit, "_tasks.snapshot", taskCallInput{Scope: scope, ID: id})
	if err != nil {
		return peerTask{}, err
	}
	var body taskBody
	if err := json.Unmarshal(data, &body); err != nil {
		return peerTask{}, fmt.Errorf("policy: decode task snapshot: %w", err)
	}
	return body.Resource, nil
}

// callReviewsCheck reports whether an exact digest-bound review exists and
// its current decision.
func (s *Service) callReviewsCheck(ctx context.Context, unit contract.Unit, scope contract.Scope, digest string) (bool, string, error) {
	data, err := s.callPeer(ctx, unit, "_reviews.check", reviewsCheckCallInput{
		Scope:        scope,
		ActionDigest: digest,
	})
	if err != nil {
		return false, "", err
	}
	var body reviewsCheckBody
	if err := json.Unmarshal(data, &body); err != nil {
		return false, "", fmt.Errorf("policy: decode reviews check: %w", err)
	}
	decision := ""
	if body.Decision != nil {
		decision = body.Decision.Decision
	}
	return body.Eligible, decision, nil
}

// isNotFound reports whether the error is a not_found fault.
func isNotFound(err error) bool {
	var f *contract.Fault
	if !errors.As(err, &f) {
		return false
	}
	return f.Code == contract.CodeNotFound
}

// defaultReviewRequired reports whether the capability falls into one of the
// default review classes: publication, outbound messages, merge, deploy,
// credential-account substitution and permission expansion require an
// eligible owner's decision unless a narrower standing policy already
// governs the class. The families are fixed capability-name conventions of
// the frozen contract.
func defaultReviewRequired(capability string) bool {
	switch {
	case strings.HasPrefix(capability, "publication."),
		strings.HasPrefix(capability, "message."),
		strings.HasPrefix(capability, "merge."),
		strings.HasPrefix(capability, "repository.merge"),
		strings.HasPrefix(capability, "deploy"),
		strings.HasPrefix(capability, "credential.substitute"),
		strings.HasPrefix(capability, "account.substitute"),
		strings.HasPrefix(capability, "grant."),
		capability == "configuration.apply":
		return true
	}
	return false
}

// actionDigest derives the exact review digest for a capability check
// without a sealed candidate and without an exact action: the SHA-256 of the
// canonical JSON of the capability and scope.
func actionDigest(capability string, scope contract.Scope) string {
	raw, err := json.Marshal(struct {
		Capability string         `json:"capability"`
		Scope      contract.Scope `json:"scope"`
	}{Capability: capability, Scope: scope})
	if err != nil {
		// Marshaling two fixed-type fields cannot fail.
		return ""
	}
	return string(contract.Hash(raw))
}

// exactActionDigest derives the exact review digest for one concrete action:
// the SHA-256 of its canonical struct-ordered JSON. A review created for
// this digest approves exactly this action.
func exactActionDigest(action wireAction) string {
	raw, err := json.Marshal(action)
	if err != nil {
		// Marshaling a fixed-struct action cannot fail.
		return ""
	}
	return string(contract.Hash(raw))
}

// scopeDims counts the set non-installation scope dimensions; the count is
// the specificity used to let narrower standing policies shadow broader
// ones.
func scopeDims(s contract.Scope) int {
	n := 0
	for _, d := range []contract.ID{s.OrganizationID, s.ProjectID, s.WorkerID, s.TaskID} {
		if d != "" {
			n++
		}
	}
	return n
}

// policyCovers reports whether a stored policy's scope governs the request
// scope: every set policy dimension must equal the request dimension, and a
// set policy dimension over an unset request dimension is a broader request
// the policy does not govern.
func policyCovers(p *policyRow, req contract.Scope) bool {
	return dimMatches(p.Scope.OrganizationID, req.OrganizationID) &&
		dimMatches(p.Scope.ProjectID, req.ProjectID) &&
		dimMatches(p.Scope.WorkerID, req.WorkerID) &&
		dimMatches(p.Scope.TaskID, req.TaskID)
}

// actionScopeMatches requires exact dimension equality between the action
// scope and the request scope.
func actionScopeMatches(a, req contract.Scope) bool {
	return a == req
}

// bindingCovers reports whether a binding's scope covers the request scope.
func bindingCovers(b peerBinding, req contract.Scope) bool {
	return dimMatches(b.Scope.OrganizationID, req.OrganizationID) &&
		dimMatches(b.Scope.ProjectID, req.ProjectID) &&
		dimMatches(b.Scope.WorkerID, req.WorkerID) &&
		dimMatches(b.Scope.TaskID, req.TaskID)
}

// eligiblePrincipals derives the review-eligible principals from the scope
// snapshot: the chiefs of every ancestor organization, deduplicated and
// sorted.
func eligiblePrincipals(snap scopeSnapshot) []contract.ID {
	seen := map[contract.ID]bool{}
	out := make([]contract.ID, 0, len(snap.Ancestors))
	for _, org := range snap.Ancestors {
		if org.ChiefID == "" || seen[org.ChiefID] {
			continue
		}
		seen[org.ChiefID] = true
		out = append(out, org.ChiefID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// evaluate is the deterministic authority intersection. It returns the
// decision for the actor's capability request, optionally bounded by one
// exact action and one sealed candidate digest.
func (s *Service) evaluate(ctx context.Context, unit contract.Unit, scope contract.Scope, capability string, action *wireAction, candidateDigest string) (wirePolicyResult, error) {
	now := s.deps.Clock.Now()
	deny := func(reason string) (wirePolicyResult, error) {
		return wirePolicyResult{Decision: decisionDeny, Reasons: []string{reason}, Requirements: []wireDecisionRequirement{}}, nil
	}

	actor := unit.Actor()
	if actor.PrincipalID == "" {
		return deny("no authenticated actor is associated with this request")
	}

	// An empty capability is the capability-less explain mode: only
	// wildcard rules and capability-independent fences apply, and the
	// entitlement check is skipped.
	capabilityKnown := capability != ""

	// Authority is read from identity; a peer failure other than a
	// missing principal propagates so the decision fails closed instead
	// of inventing an answer.
	authority, err := s.callAuthority(ctx, unit, actor.PrincipalID, scope)
	if err != nil {
		if isNotFound(err) {
			return deny("the principal holds no authority in this scope")
		}
		return wirePolicyResult{}, err
	}
	if authority.Principal.Revoked {
		return deny("the principal is revoked")
	}

	env := buildEnvelope(authority.Grants, now, scope)
	if !env.coversScope(scope) {
		return deny("current grants do not cover the request scope")
	}
	for _, r := range authority.Restrictions {
		if r == capWildcard || (capabilityKnown && r == capability) {
			return deny(fmt.Sprintf("an active restriction covers capability %s", capability))
		}
	}

	if scope.TaskID != "" {
		task, err := s.callTaskSnapshot(ctx, unit, scope, scope.TaskID)
		if err != nil {
			if isNotFound(err) {
				return deny("the task in the request scope does not exist")
			}
			return wirePolicyResult{}, err
		}
		if task.State == "cancelled" {
			return deny("the task in the request scope is cancelled")
		}
		if scope.WorkerID != "" && task.WorkerID != scope.WorkerID {
			return deny("the task in the request scope belongs to a different worker")
		}
	}

	snap, err := s.callSnapshot(ctx, unit, scope)
	if err != nil {
		return wirePolicyResult{}, err
	}
	if scope.WorkerID != "" && (snap.Worker == nil || snap.Worker.ID != scope.WorkerID) {
		return deny("the worker in the request scope is not bound in the current configuration")
	}

	if action != nil {
		if !actionScopeMatches(action.Scope, scope) {
			return deny("the action scope does not match the request scope")
		}
		if action.Destination != "" && !env.allowsDest(action.Destination) {
			return deny("current grants do not include the action destination")
		}
		if env.expires != nil && !env.expires.After(action.ExpiresAt) {
			return deny("current grants expire before the action window ends")
		}
	}

	// Binding permissions join the capability set without ever widening
	// scope or overriding denials.
	bindingCaps := map[string]bool{}
	for _, b := range snap.Bindings {
		if !bindingCovers(b, scope) {
			continue
		}
		for _, c := range b.Permissions {
			bindingCaps[c] = true
		}
	}

	// Denials are absolute: a deny grant or an active restriction wins
	// before any permission source is consulted. The entitlement check is
	// meaningful only for a named capability.
	if capabilityKnown {
		if env.deniedCap(capability) {
			return deny(fmt.Sprintf("a deny grant covers capability %s", capability))
		}
		if !env.hasCap(capability) && !bindingCaps[capability] {
			return deny("current grants and bindings do not include the requested capability")
		}
	}

	// Standing policies: explicit denies from every covering scope win;
	// allow and review rules from the most specific covering scope shadow
	// broader ones (a narrower standing policy governs its class).
	policies, err := s.loadActivePolicies(ctx, unit, scope.InstallationID)
	if err != nil {
		return wirePolicyResult{}, err
	}
	type matchedRule struct {
		policy *policyRow
		rule   wireRule
	}
	var denies []matchedRule
	var narrow []matchedRule
	maxSpec := -1
	for i := range policies {
		p := &policies[i]
		if !policyCovers(p, scope) {
			continue
		}
		for _, r := range p.Rules {
			if r.Capability != capability && r.Capability != capWildcard {
				continue
			}
			if r.Decision == decisionDeny {
				denies = append(denies, matchedRule{policy: p, rule: r})
				continue
			}
			spec := scopeDims(p.Scope)
			if spec > maxSpec {
				maxSpec = spec
				narrow = nil
			}
			if spec == maxSpec {
				narrow = append(narrow, matchedRule{policy: p, rule: r})
			}
		}
	}
	if len(denies) > 0 {
		if capabilityKnown {
			return deny(fmt.Sprintf("standing policy %s denies capability %s", denies[0].policy.ID, capability))
		}
		return deny(fmt.Sprintf("standing policy %s denies wildcard-matched actions", denies[0].policy.ID))
	}

	// Outcome priorities: deny > review > prerequisite_missing > allow.
	const (
		prioAllow = 0
		prioReq   = 1
		prioRev   = 2
		prioDeny  = 3
	)
	priority := prioAllow
	var reasons []string
	upgrade := func(p int, reason string) {
		if p > priority {
			priority = p
			reasons = []string{reason}
		} else if p == priority {
			reasons = append(reasons, reason)
		}
	}

	classification := ""
	if snap.Project != nil {
		classification = snap.Project.Classification
	}
	for _, m := range narrow {
		outcome, reason, err := s.applyRuleConditions(m.rule, m.policy, scope, action, capability, classification, now)
		if err != nil {
			return wirePolicyResult{}, err
		}
		switch outcome {
		case decisionDeny:
			upgrade(prioDeny, reason)
		case decisionReview:
			upgrade(prioRev, reason)
		case decisionPrerequisiteMissing:
			upgrade(prioReq, reason)
		}
	}

	decision := decisionAllow
	switch {
	case priority >= prioDeny:
		decision = decisionDeny
	case priority == prioRev:
		decision = decisionReview
	case priority == prioReq:
		decision = decisionPrerequisiteMissing
	}

	if decision == decisionAllow && len(narrow) == 0 && capabilityKnown && defaultReviewRequired(capability) {
		decision = decisionReview
		reasons = []string{fmt.Sprintf("capability %s is a default review class and no narrower standing policy governs it", capability)}
	}

	var requirement *wireDecisionRequirement
	if decision == decisionReview {
		digest := candidateDigest
		if digest == "" && action != nil {
			digest = exactActionDigest(*action)
		}
		if digest == "" {
			digest = actionDigest(capability, scope)
		}
		requirement = &wireDecisionRequirement{
			ActionDigest:       digest,
			HumanRequired:      true,
			EligiblePrincipals: eligiblePrincipals(snap),
			ExpiresAt:          now.Add(reviewRequirementTTL),
			SeparateProposer:   actor.Kind == "worker" || actor.Kind == "client_agent",
		}
	}

	// A sealed candidate digest consults the exact review state: an
	// approved decision satisfies a review requirement, a rejected one
	// denies, and a pending or absent review keeps the requirement.
	if candidateDigest != "" && decision == decisionReview {
		eligible, state, err := s.callReviewsCheck(ctx, unit, scope, candidateDigest)
		if err != nil {
			return wirePolicyResult{}, err
		}
		switch {
		case eligible && state == "approve":
			decision = decisionAllow
			reasons = []string{fmt.Sprintf("exact review %s is approved", candidateDigest)}
			requirement = nil
		case eligible && state == "reject":
			decision = decisionDeny
			reasons = []string{fmt.Sprintf("exact review %s is rejected", candidateDigest)}
			requirement = nil
		}
	}

	sort.Strings(reasons)
	result := wirePolicyResult{Decision: decision, Reasons: reasons, Requirements: []wireDecisionRequirement{}}
	if requirement != nil {
		result.Requirements = append(result.Requirements, *requirement)
	}
	return result, nil
}

// applyRuleConditions evaluates one allow or review rule's bounded
// declarative conditions in fixed order. It returns the outcome decision and
// reason; an error only signals an infrastructure failure, never an unmet
// condition, which is itself a decision. A deny-effect rule never reaches
// this function: deny rules are unconditional fences.
func (s *Service) applyRuleConditions(rule wireRule, p *policyRow, scope contract.Scope, action *wireAction, capability, classification string, now time.Time) (outcome string, reason string, err error) {
	conds := map[string]json.RawMessage{}
	if len(rule.Conditions) > 0 {
		if err := json.Unmarshal(rule.Conditions, &conds); err != nil {
			return decisionDeny, fmt.Sprintf("policy %s rule conditions are malformed", p.ID), nil
		}
	}
	for key := range conds {
		switch key {
		case condMaxCost, condRequiredClassification, condNotBefore, condExpiresAt,
			condRequiredToolVersion, condRequiredWorkerID, condRequiredReview:
		default:
			return decisionDeny, fmt.Sprintf("policy %s uses unsupported condition %q", p.ID, key), nil
		}
	}
	for _, key := range supportedConditionKeys {
		raw, present := conds[key]
		if !present {
			continue
		}
		outcome, reason := evaluateCondition(key, raw, scope, action, capability, classification, p, now)
		if outcome != decisionAllow {
			return outcome, reason, nil
		}
	}
	if rule.HumanRequired {
		return decisionReview, fmt.Sprintf("policy %s marks capability %s human required", p.ID, capability), nil
	}
	return decisionAllow, "", nil
}

// evaluateCondition evaluates one supported condition against the request.
// Unmet conditions on an allow or review rule refuse the action (fail
// closed) rather than falling through to weaker defaults.
func evaluateCondition(key string, raw json.RawMessage, scope contract.Scope, action *wireAction, capability, classification string, p *policyRow, now time.Time) (outcome string, reason string) {
	switch key {
	case condMaxCost:
		var bound wireMoney
		if err := json.Unmarshal(raw, &bound); err != nil {
			return decisionDeny, fmt.Sprintf("policy %s condition max_cost is malformed", p.ID)
		}
		if action == nil {
			return decisionPrerequisiteMissing, fmt.Sprintf("policy %s condition max_cost requires the exact action", p.ID)
		}
		if action.CostBound.MicroUnit > bound.MicroUnit {
			return decisionDeny, fmt.Sprintf("action cost bound exceeds policy %s maximum", p.ID)
		}
		return decisionAllow, ""
	case condRequiredClassification:
		var want string
		if err := json.Unmarshal(raw, &want); err != nil {
			return decisionDeny, fmt.Sprintf("policy %s condition required_classification is malformed", p.ID)
		}
		if classification == "" {
			return decisionDeny, fmt.Sprintf("policy %s requires a project classification the request scope does not establish", p.ID)
		}
		if classification != want {
			return decisionDeny, fmt.Sprintf("project classification %s does not satisfy policy %s requirement %s", classification, p.ID, want)
		}
		return decisionAllow, ""
	case condNotBefore:
		at, ok := parseConditionTime(raw)
		if !ok {
			return decisionDeny, fmt.Sprintf("policy %s condition not_before is malformed", p.ID)
		}
		if now.Before(at) {
			return decisionDeny, fmt.Sprintf("policy %s admits capability %s only from %s", p.ID, capability, formatStamp(at))
		}
		return decisionAllow, ""
	case condExpiresAt:
		at, ok := parseConditionTime(raw)
		if !ok {
			return decisionDeny, fmt.Sprintf("policy %s condition expires_at is malformed", p.ID)
		}
		if !now.Before(at) {
			return decisionDeny, fmt.Sprintf("policy %s admission for capability %s expired at %s", p.ID, capability, formatStamp(at))
		}
		return decisionAllow, ""
	case condRequiredToolVersion:
		var want wireRef
		if err := json.Unmarshal(raw, &want); err != nil {
			return decisionDeny, fmt.Sprintf("policy %s condition required_tool_version is malformed", p.ID)
		}
		if action == nil {
			return decisionPrerequisiteMissing, fmt.Sprintf("policy %s condition required_tool_version requires the exact action", p.ID)
		}
		if action.Tool.ID != want.ID || action.Tool.Version != want.Version {
			return decisionDeny, fmt.Sprintf("action tool %s v%d does not satisfy policy %s required version %s v%d", action.Tool.ID, action.Tool.Version, p.ID, want.ID, want.Version)
		}
		return decisionAllow, ""
	case condRequiredWorkerID:
		var want contract.ID
		if err := json.Unmarshal(raw, &want); err != nil {
			return decisionDeny, fmt.Sprintf("policy %s condition required_worker_id is malformed", p.ID)
		}
		if scope.WorkerID != want {
			return decisionDeny, fmt.Sprintf("policy %s restricts capability %s to worker %s", p.ID, capability, want)
		}
		return decisionAllow, ""
	case condRequiredReview:
		var want bool
		if err := json.Unmarshal(raw, &want); err != nil {
			return decisionDeny, fmt.Sprintf("policy %s condition required_review is malformed", p.ID)
		}
		if want {
			return decisionReview, fmt.Sprintf("policy %s requires an exact review for capability %s", p.ID, capability)
		}
		return decisionAllow, ""
	}
	// Unreachable: the supported-key check rejects anything else first.
	return decisionDeny, fmt.Sprintf("policy %s uses unsupported condition %q", p.ID, key)
}

// parseConditionTime parses one RFC3339 condition timestamp.
func parseConditionTime(raw json.RawMessage) (time.Time, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
