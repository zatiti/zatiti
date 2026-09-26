package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// Tool adapter profiles are trusted local configuration: one strictly
// validated, secret-free JSON profile per tool adapter at
// <state-dir>/adapters/<name>.json. Hosted model profiles are durable
// execution configuration, resolved per dispatch by the controller through
// responsesAdapterFactory; they are not loaded from a process-global file.
// No provider, model or price is guessed when a pinned profile is absent.

// adapterConstructors maps the landed adapter names to their constructors.
var adapterConstructors = map[string]func(contract.AdapterDependencies, json.RawMessage) (contract.Adapter, error){
	"github":   github.New,
	"httpread": httpread.New,
	"mcp":      mcpclient.New,
	"serenity": serenity.New,
}

// unimplementedAdapters names adapters the product wires by design whose
// package has not landed. serve reports them at startup. Empty on this tree:
// every tool adapter is landed, and responses is supplied by its per-dispatch
// factory.
var unimplementedAdapters = []string{}

// maxAdapterProfileBytes bounds one profile file.
const maxAdapterProfileBytes = 1 << 20

// readMCPAdmissionProfile reads the single trusted installation-local MCP
// adapter profile. The same immutable bytes are passed to connections for
// admission and to mcpclient for dispatch; neither side rereads the path.
// Missing configuration leaves MCP unavailable, while malformed filesystem
// objects fail startup without including profile contents in an error.
func readMCPAdmissionProfile(dir string) (json.RawMessage, error) {
	path := filepath.Join(dir, "mcp.json")
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspecting MCP adapter profile: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxAdapterProfileBytes {
		return nil, fmt.Errorf("MCP adapter profile must be a regular file no larger than %d bytes", maxAdapterProfileBytes)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening MCP adapter profile failed")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() > maxAdapterProfileBytes {
		return nil, fmt.Errorf("MCP adapter profile changed or is not a bounded regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxAdapterProfileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading MCP adapter profile failed")
	}
	if len(raw) > maxAdapterProfileBytes {
		return nil, fmt.Errorf("MCP adapter profile must be no larger than %d bytes", maxAdapterProfileBytes)
	}
	return json.RawMessage(raw), nil
}

// loadAdapters constructs every adapter that has a profile in dir. A
// profile for an unknown adapter, or one its constructor refuses, fails
// startup: silently serving without it would hide a configuration defect.
// It returns the registered adapters and the names of landed adapters that
// have no profile.
func loadAdapters(dir string, deps contract.AdapterDependencies) (map[string]contract.Adapter, []string, error) {
	profile, err := readMCPAdmissionProfile(dir)
	if err != nil {
		return nil, nil, err
	}
	return loadAdaptersWithMCPProfile(dir, deps, profile)
}

// loadAdaptersWithMCPProfile constructs the MCP adapter from the exact profile
// bytes already supplied to connections at installation assembly. An empty
// profile explicitly means MCP is not configured; this function does not
// inspect mcp.json again, even if it changed after assembly.
func loadAdaptersWithMCPProfile(dir string, deps contract.AdapterDependencies, mcpProfile json.RawMessage) (map[string]contract.Adapter, []string, error) {
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
		// MCP is constructed below from the assembly snapshot, not from a
		// second read of this mutable path.
		if name == "mcp" {
			continue
		}
		// Responses providers are selected by the durable execution profile
		// on each dispatch. The former responses.json file is retained only
		// as an explicit legacy/import source; it must never select or
		// override a provider for a running controller.
		if name == "responses" {
			continue
		}
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
	if len(mcpProfile) > 0 {
		adapter, err := mcpclient.New(deps, append(json.RawMessage(nil), mcpProfile...))
		if err != nil {
			// Constructor errors are deliberately redacted at this boundary:
			// startup diagnostics must never echo profile material.
			return nil, nil, fmt.Errorf("adapter mcp: invalid installation profile")
		}
		if adapter == nil || adapter.Name() != "mcp" {
			return nil, nil, fmt.Errorf("adapter mcp: constructor returned an unexpected adapter")
		}
		adapters["mcp"] = adapter
	}
	var missing []string
	for _, name := range landedAdapterNames() {
		if _, ok := adapters[name]; !ok {
			missing = append(missing, name)
		}
	}
	return adapters, missing, nil
}

// newResponsesAdapter constructs a Responses adapter for the immutable
// profile pinned to one execution dispatch. It deliberately takes the
// profile as an argument instead of loading process-global configuration,
// so changing a durable model profile takes effect without restarting the
// controller. The adapter resolves its credential from the installation's
// protected SecretStore through these dependencies.
func newResponsesAdapter(deps contract.AdapterDependencies, rawProfile json.RawMessage) (contract.Adapter, error) {
	return responses.New(deps, rawProfile)
}

// responsesAdapterFactory closes over installation-scoped dependencies and
// is attached to the controller. The controller supplies the exact,
// revision-pinned profile on every dispatch.
func responsesAdapterFactory(deps contract.AdapterDependencies) func(json.RawMessage) (contract.Adapter, error) {
	return func(rawProfile json.RawMessage) (contract.Adapter, error) {
		return newResponsesAdapter(deps, rawProfile)
	}
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
