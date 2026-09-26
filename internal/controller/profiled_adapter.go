package controller

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// adapterForDispatch resolves a version-pinned adapter profile. Profile bytes
// are canonicalized before hashing, so equivalent JSON encodings share one
// immutable adapter instance. A dispatch without a profile follows the
// historical registration path (including existing OpenAI v1 installs).
func (c *Controller) adapterForDispatch(d contract.Dispatch) (contract.Adapter, error) {
	if len(d.AdapterProfile) == 0 {
		adapter, ok := c.adapters[d.Adapter]
		if !ok {
			return nil, capabilityUnsupported("adapter %q is not registered with this controller", d.Adapter)
		}
		return adapter, nil
	}
	if d.Adapter != "responses" {
		return nil, capabilityUnsupported("pinned adapter profiles are supported only by the responses adapter, not %q", d.Adapter)
	}
	if d.CredentialRef == "" {
		return nil, prerequisiteMissing("pinned responses profile has no current credential reference")
	}
	if _, err := responsesProfileConnection(d.AdapterProfile); err != nil {
		return nil, err
	}
	canonical, err := contract.Canonicalize(d.AdapterProfile)
	if err != nil {
		return nil, invalidInput("pinned responses profile is not canonical JSON: %v", err)
	}
	digest := contract.Hash(canonical)

	c.mu.Lock()
	if adapter, ok := c.profileAdapters[digest]; ok {
		c.mu.Unlock()
		return adapter, nil
	}
	factory := c.deps.ResponsesAdapterFactory
	c.mu.Unlock()
	if factory == nil {
		return nil, prerequisiteMissing("no responses profile adapter factory is attached")
	}
	adapter, err := factory(canonical)
	if err != nil {
		return nil, err
	}
	if adapter == nil || adapter.Name() != d.Adapter {
		return nil, capabilityUnsupported("responses profile factory returned an adapter other than %q", d.Adapter)
	}

	// A concurrent first dispatch may have completed the same construction.
	c.mu.Lock()
	if existing, ok := c.profileAdapters[digest]; ok {
		c.mu.Unlock()
		return existing, nil
	}
	// Historical immutable profiles may span many years of queued work; cap
	// the in-memory acceleration map so profile churn cannot grow it forever.
	if len(c.profileAdapters) < 128 {
		c.profileAdapters[digest] = adapter
	}
	c.mu.Unlock()
	return adapter, nil
}

// responsesProfileConnection reads only the minimal untrusted profile
// fields needed to reject an incomplete pinned connection before dispatch.
// The profile factory performs the full strict schema and evidence check;
// Effects remains responsible for rechecking live connection authority.
func responsesProfileConnection(raw json.RawMessage) (contract.ID, error) {
	var p struct {
		ConnectionID contract.ID `json:"connection_id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", invalidInput("pinned responses profile cannot be decoded: %v", err)
	}
	if p.ConnectionID == "" {
		return "", prerequisiteMissing("pinned responses profile has no connection identity")
	}
	return p.ConnectionID, nil
}
