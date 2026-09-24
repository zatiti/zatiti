package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zatiti/zatiti/internal/adapters/github"
	"github.com/zatiti/zatiti/internal/adapters/httpread"
	"github.com/zatiti/zatiti/internal/adapters/mcpclient"
	"github.com/zatiti/zatiti/internal/adapters/responses"
	"github.com/zatiti/zatiti/internal/adapters/serenity"
	"github.com/zatiti/zatiti/internal/contract"
)

// Adapter profiles are trusted local configuration: one strictly validated,
// secret-free JSON profile per adapter at <state-dir>/adapters/<name>.json.
// An adapter with no profile is not registered and a dispatch naming it is
// recorded not_sent with capability_unsupported by the controller; it is
// never guessed at. A profile is loaded only if it validates in full
// against its adapter's frozen schema (responses: zatiti.responses/v1,
// self-binding capability_evidence included); an invalid or partial
// profile fails startup rather than silently constructing a degraded
// adapter or falling back to any default provider/model/price (P24 item 1).

// adapterConstructors maps the landed adapter names to their constructors.
var adapterConstructors = map[string]func(contract.AdapterDependencies, json.RawMessage) (contract.Adapter, error){
	"github":    github.New,
	"httpread":  httpread.New,
	"mcp":       mcpclient.New,
	"responses": responses.New,
	"serenity":  serenity.New,
}

// unimplementedAdapters names adapters the product wires by design whose
// package has not landed. serve reports them at startup. Empty on this
// tree: every adapter the product names (github, httpread, responses,
// serenity) has a landed constructor above.
var unimplementedAdapters = []string{}

// maxAdapterProfileBytes bounds one profile file.
const maxAdapterProfileBytes = 1 << 20

// loadAdapters constructs every adapter that has a profile in dir. A
// profile for an unknown adapter, or one its constructor refuses, fails
// startup: silently serving without it would hide a configuration defect.
// It returns the registered adapters and the names of landed adapters that
// have no profile.
func loadAdapters(dir string, deps contract.AdapterDependencies) (map[string]contract.Adapter, []string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		entries = nil
	} else if err != nil {
		return nil, nil, fmt.Errorf("reading the adapter profile directory: %w", err)
	}
	adapters := map[string]contract.Adapter{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		construct, ok := adapterConstructors[name]
		if !ok {
			return nil, nil, fmt.Errorf("adapter profile %q names no landed adapter (landed: %s)", e.Name(), strings.Join(landedAdapterNames(), ", "))
		}
		path := filepath.Join(dir, e.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return nil, nil, fmt.Errorf("inspecting adapter profile %s: %w", e.Name(), err)
		}
		if !info.Mode().IsRegular() || info.Size() > maxAdapterProfileBytes {
			return nil, nil, fmt.Errorf("adapter profile %s must be a regular file under %d bytes", e.Name(), maxAdapterProfileBytes)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("reading adapter profile %s: %w", e.Name(), err)
		}
		adapter, err := construct(deps, raw)
		if err != nil {
			return nil, nil, fmt.Errorf("adapter %s: %w", name, err)
		}
		if adapter.Name() != name {
			return nil, nil, fmt.Errorf("adapter profile %s constructed an adapter named %q", e.Name(), adapter.Name())
		}
		adapters[adapter.Name()] = adapter
	}
	var missing []string
	for _, name := range landedAdapterNames() {
		if _, ok := adapters[name]; !ok {
			missing = append(missing, name)
		}
	}
	return adapters, missing, nil
}

func landedAdapterNames() []string {
	names := make([]string, 0, len(adapterConstructors))
	for name := range adapterConstructors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// adapterDependencies builds the shared adapter dependencies: one HTTP
// client with a fresh transport (never a proxy inherited from the
// environment), the platform secret and blob stores and the process clock.
func adapterDependencies(h *installationHandle) contract.AdapterDependencies {
	return contract.AdapterDependencies{
		HTTP:    &http.Client{Transport: &http.Transport{Proxy: nil}},
		Secrets: h.plat.Secrets(),
		Clock:   h.clock,
		Blobs:   h.plat.Blobs(),
	}
}
