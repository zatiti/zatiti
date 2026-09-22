package main

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zatiti/zatiti/internal/cli"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/platform"
)

// The connection-setup helper (P24 item 3): the trusted local process
// internal/connections' AGENTS.md and localio.go name but do not
// themselves implement -- "Probe/rotation/setup remote work uses prepared
// governed effect or trusted local helper, not network inside Unit...
// Complete accepts opaque helper receipt only, verify it internally; no
// pasted OAuth code/token" (internal/connections/AGENTS.md; mirrored in
// source at internal/connections/localio.go:22-24, :276, :320-323).
//
// It is the ONLY place a provider credential's raw bytes exist outside the
// secret store: never as an operation JSON field, an MCP tool argument, a
// log line or any other model-visible surface (the hard security boundary
// P24's assignment names explicitly).
//
// CLI/MCP parity, precisely stated: every OTHER piece of the setup flow
// (connection.create, connection.setup.begin/complete/cancel/status) is an
// ordinary catalog operation, generated identically for both transports by
// internal/cli.New and internal/mcp.Serve from the same descriptor -- true
// parity, no cmd/zatiti-specific code needed. This one step -- capturing
// the raw secret from the operator -- is deliberately CLI-only and has no
// MCP equivalent, on purpose: an MCP tool call's arguments are exactly the
// "operation JSON/model-visible surface" this package's hard boundary
// forbids the secret from ever entering, so an MCP tool that accepted a
// credential argument would violate the boundary this command exists to
// uphold. An operator using an MCP-only client completes credential setup
// by running `zatiti connection helper` in a terminal alongside it, the
// same way `zatiti init`'s owner credential only ever reaches a local
// profile file, never an MCP response body.
//
// Mechanics: platform.Open (internal/platform/platform.go:88-91) explicitly
// does not take the installation lock -- only (*Platform).Acquire does --
// so this command opens the SAME state directory's secret store directly,
// concurrently with a running `zatiti serve`, with no lock contention. It
// then talks to the running controller only through the ordinary
// contract.Operator/socket path for the three connection.setup.*
// operations (never for the raw secret itself), exactly like every other
// CLI command.
//
// Scope: this command implements setup method "store_reference" only (an
// operator-supplied credential, e.g. an API key, captured from this
// terminal with echo suppressed). Method "browser" (OAuth) is deliberately
// NOT implemented: internal/connections' credentialMeta
// (internal/connections/localio.go:64-68) carries client_id/authorize_url/
// redirect_uri but no token-endpoint field, so the code-for-token exchange
// step has no frozen wire shape anywhere this package can read. Inventing
// a token-endpoint convention unbacked by any frozen schema is exactly the
// kind of workaround the P24 assignment says to stop and report instead of
// building; flagged in the P24 handoff as a prerequisite gap for whichever
// card owns the OAuth flow's exact metadata shape.
//
// KNOWN GAP (verified, not fixable from cmd/zatiti -- see the P24 handoff):
// internal/connections/localio.go:495 reads the shared HMAC receipt key
// with `s.secrets.Get(ctx, helperReceiptKeyRef)`, passing the literal
// constant string directly as the reference. Neither landed
// contract.SecretStore backend (internal/platform/secret.go's
// keychainSecrets, internal/platform/secret_headless.go's headlessSecrets)
// returns that string from Put: keychainSecrets.Put deterministically
// returns "kc1:"+base64(key) (secret.go:146-158), and
// headlessSecrets.Put returns a random "hl1:"+hex(16) per call
// (secret_headless.go:80-119) -- never the key argument itself. So no
// caller, including this helper, can ever make a later
// Get(ctx, helperReceiptKeyRef) succeed against a real installation;
// internal/connections' own tests never catch this because
// internal/connections/helpers_test.go's fakeSecrets.Put returns its
// reference argument unchanged (identity), masking the mismatch. This
// command still calls ensureHelperReceiptKey (best effort: reads, then
// mints and stores a fresh key if absent) and keeps the freshly minted key
// in memory to sign THIS invocation's receipt correctly, but
// connection.setup.complete's own server-side verification
// (internal/connections/localio.go:480-530) will report
// prerequisite_missing "the helper receipt key is not provisioned" against
// a real platform-backed installation regardless, until internal/connections
// or internal/platform (both outside cmd/zatiti's write scope) close this
// gap -- for example by internal/connections storing and re-resolving the
// opaque reference Put actually returns, the way this command's own
// credential_ref handling already does correctly (see runConnectionHelper
// below: it uses secrets.Put's *return value*, never its own invented
// label, as the credential_ref the receipt names).

const (
	// helperReceiptPrefix and helperReceiptKeyRef MUST stay byte-for-byte
	// identical to the unexported constants of the same name in
	// internal/connections/localio.go:34,38. connection.setup.complete's
	// Perform phase (localio.go:480-530, verifyReceipt at :534-561)
	// verifies a receipt this command mints against exactly this prefix
	// and decodes the same JSON payload shape (helperPayload below mirrors
	// localio.go:54-59's unexported helperPayload field-for-field).
	// Neither identifier is exported by internal/connections (Go cannot
	// import an unexported constant across packages), so this is a
	// deliberate, documented wire-format mirror -- the same pattern
	// internal/controller's own wire.go already uses for frozen
	// cross-package shapes it does not import Go types for.
	helperReceiptPrefix   = "zatiti-helper/v1."
	helperReceiptKeyRef   = "connections/helper/receipt-key"
	helperReceiptKeyBytes = 32
)

// helperPayload mirrors internal/connections/localio.go:54-59's unexported
// helperPayload: the exact JSON fields verifyReceipt decodes and checks.
type helperPayload struct {
	ChallengeID     contract.ID `json:"challenge_id"`
	CredentialRef   string      `json:"credential_ref"`
	AccountIdentity string      `json:"account_identity"`
	ExpiresAt       time.Time   `json:"expires_at"`
}

// helperReceiptKeyProvisioned reports whether the shared HMAC receipt key
// is retrievable at its well-known reference right now, without creating
// it. Given the verified gap documented above, this reports false on
// essentially every real installation today; it is still computed
// honestly (a direct Get, not a hardcoded false) so it starts reporting
// true the moment the underlying gap closes, with no readiness-logic
// change needed here.
func helperReceiptKeyProvisioned(ctx context.Context, secrets contract.SecretStore) bool {
	if secrets == nil {
		return false
	}
	key, err := secrets.Get(ctx, helperReceiptKeyRef)
	defer zero(key)
	return err == nil && len(key) > 0
}

// ensureHelperReceiptKey returns the shared HMAC key for this invocation:
// the stored one if Get(helperReceiptKeyRef) already resolves, else a
// freshly minted 32-byte random key, best-effort stored (see the package
// doc comment's KNOWN GAP for why a later Get by the same literal
// reference is not guaranteed to find it again). The returned bytes must
// be zeroed by the caller once the receipt is signed.
func ensureHelperReceiptKey(ctx context.Context, secrets contract.SecretStore) ([]byte, error) {
	if key, err := secrets.Get(ctx, helperReceiptKeyRef); err == nil && len(key) > 0 {
		return key, nil
	}
	key := make([]byte, helperReceiptKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating the helper receipt key: %w", err)
	}
	// Best effort: this Put succeeds and stores the key, but under an
	// opaque reference platform mints (never helperReceiptKeyRef itself);
	// a later, separate process's Get(helperReceiptKeyRef) is not
	// guaranteed to find it. Ignoring a Put failure here is deliberate:
	// this invocation's own receipt is still signed correctly below from
	// the in-memory key regardless of whether storing it for reuse
	// succeeds.
	_, _ = secrets.Put(ctx, helperReceiptKeyRef, key)
	return key, nil
}

// mintHelperReceipt builds the opaque receipt string
// connection.setup.complete's Perform phase verifies:
// helperReceiptPrefix + base64url(payload JSON) + "." +
// hex(HMAC-SHA256(payload JSON, key)) -- exactly
// internal/connections/localio.go:534-561's verifyReceipt algorithm run in
// reverse.
func mintHelperReceipt(key []byte, payload helperPayload) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encoding the helper receipt payload: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	return helperReceiptPrefix + base64.RawURLEncoding.EncodeToString(body) + "." + sig, nil
}

// attachConnectionHelper adds `zatiti connection helper` under the
// generated "connection" group internal/cli.New already built from the
// catalog (connection.create/.get/.setup.begin/... all resolve under a
// "connection" grouping command per internal/cli/cli.go:104-123's
// resolveNode). "helper" has no Descriptor -- it is a cmd/zatiti-only
// concept -- so it is nested in after the fact rather than through
// internal/cli's descriptor-driven path, exactly the way
// serveCommand/mcpCommand attach transport mechanics that also have no
// descriptor. Every connection.* descriptor guarantees the "connection"
// group already exists by the time run() calls this; the fallback creates
// it defensively rather than panicking if that ever stops holding.
func attachConnectionHelper(root *cobra.Command, cfg *config, op contract.Operator, streams cli.IO) {
	var group *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "connection" {
			group = c
			break
		}
	}
	if group == nil {
		group = &cobra.Command{Use: "connection", SilenceUsage: true, SilenceErrors: true}
		root.AddCommand(group)
	}
	group.AddCommand(connectionHelperCommand(cfg, op, streams))
}

// connectionHelperCommand is `zatiti connection helper`.
func connectionHelperCommand(cfg *config, op contract.Operator, streams cli.IO) *cobra.Command {
	var connectionID, installationID string
	cmd := &cobra.Command{
		Use:   "helper",
		Short: "the trusted local helper: capture a provider credential from this terminal and complete connection.setup",
		Long: "connection helper is the trusted local process connection.setup.complete's opaque helper_ref requires. " +
			"It reads the raw credential from THIS terminal only, writes it directly to the local secret store, and " +
			"completes setup with an opaque receipt -- the raw credential never becomes an operation argument, an MCP " +
			"tool argument, or any other model-visible input. Run it after `connection create` and before the " +
			"connection is usable.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, _ []string) error {
			if connectionID == "" || installationID == "" {
				err := errors.New("--connection-id and --installation-id are required")
				return &exitError{code: contract.CLIExit(&contract.Fault{Code: contract.CodeInvalidInput}), err: err}
			}
			if err := runConnectionHelper(c.Context(), cfg, op, streams, contract.ID(installationID), contract.ID(connectionID)); err != nil {
				_, _ = fmt.Fprintf(streams.Err, "zatiti connection helper: %v\n", err)
				return &exitError{code: exitFor(err), err: err}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&connectionID, "connection-id", "", "the connection to complete setup for (already created via connection.create)")
	f.StringVar(&installationID, "installation-id", "", "the installation scope")
	f.StringVar(&cfg.CredentialBackend, "credential-backend", cfg.CredentialBackend, "secret custody: keychain or headless (must match the running controller's)")
	f.StringVar(&cfg.MasterKeyRef, "master-key", cfg.MasterKeyRef, "master key reference, file:<path> or secret:<key> (must match the running controller's)")
	return cmd
}

// runConnectionHelper is the full flow: read the connection, begin setup,
// prompt for the credential on this terminal only, write it directly to
// the local secret store, mint the opaque receipt and complete setup. No
// step prints the credential; only diagnostics (never the CLI-envelope
// stdout other commands use) report progress.
func runConnectionHelper(ctx context.Context, cfg *config, op contract.Operator, streams cli.IO, installationID, connectionID contract.ID) error {
	if cfg.MasterKeyRef == "" {
		return errors.New("a master key reference is required (--master-key): the secret store is always encrypted at rest")
	}
	plat, err := platform.Open(platform.Config{StateDir: cfg.StateDir, CredentialBackend: cfg.CredentialBackend, MasterKeyRef: cfg.MasterKeyRef})
	if err != nil {
		return fmt.Errorf("opening the local secret store: %w", err)
	}
	defer func() { _ = plat.Close() }()
	secrets := plat.Secrets()

	scope := map[string]any{"installation_id": installationID}

	getRes, err := callOperation(ctx, op, "connection.get", map[string]any{"scope": scope, "id": connectionID})
	if err != nil {
		return fmt.Errorf("connection.get: %w", err)
	}
	var conn struct {
		Resource struct {
			Version         int64  `json:"version"`
			AccountIdentity string `json:"account_identity"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(getRes.Data, &conn); err != nil {
		return fmt.Errorf("decoding connection.get: %w", err)
	}

	beginRes, err := callOperation(ctx, op, "connection.setup.begin", map[string]any{
		"scope": scope, "connection_id": connectionID, "expected_version": conn.Resource.Version, "method": "store_reference",
	})
	if err != nil {
		return fmt.Errorf("connection.setup.begin: %w", err)
	}
	var challenge struct {
		Resource struct {
			ID        contract.ID `json:"id"`
			Version   int64       `json:"version"`
			ExpiresAt time.Time   `json:"expires_at"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(beginRes.Data, &challenge); err != nil {
		return fmt.Errorf("decoding connection.setup.begin: %w", err)
	}

	_, _ = fmt.Fprintf(streams.Err, "zatiti: enter the credential for connection %s (account %s); input is not echoed and never leaves this terminal as an operation argument\n", connectionID, conn.Resource.AccountIdentity)
	secret, err := readSecretLine(streams.In)
	if err != nil {
		return fmt.Errorf("reading the credential: %w", err)
	}
	defer zero(secret)
	if len(secret) == 0 {
		return errors.New("an empty credential was entered; setup cancelled")
	}

	// credentialRef is the store's OWN opaque return value, never a label
	// this command invents: connection.setup.complete's Perform phase later
	// calls secrets.Get(ctx, payload.CredentialRef) with exactly what this
	// receipt names, so it must be the reference platform.Secrets().Put
	// actually returns (contract.SecretStore.Put's opaque-reference
	// contract, internal/contract/stores.go:11).
	credentialRef, err := secrets.Put(ctx, "connections/credential/"+string(contract.NewID()), secret)
	if err != nil {
		return fmt.Errorf("writing the credential to the local secret store: %w", err)
	}

	key, err := ensureHelperReceiptKey(ctx, secrets)
	if err != nil {
		return err
	}
	defer zero(key)
	receipt, err := mintHelperReceipt(key, helperPayload{
		ChallengeID: challenge.Resource.ID, CredentialRef: credentialRef,
		AccountIdentity: conn.Resource.AccountIdentity, ExpiresAt: challenge.Resource.ExpiresAt,
	})
	if err != nil {
		return err
	}

	if _, err := callOperation(ctx, op, "connection.setup.complete", map[string]any{
		"scope": scope, "challenge_id": challenge.Resource.ID, "expected_version": challenge.Resource.Version, "helper_ref": receipt,
	}); err != nil {
		return fmt.Errorf("connection.setup.complete: %w", err)
	}
	_, _ = fmt.Fprintf(streams.Err, "zatiti: connection %s setup complete\n", connectionID)
	return nil
}

// callOperation runs one operation call and normalizes a non-completed
// outcome (a transport error, or a completed transport carrying a fault)
// into a single Go error the caller can wrap and exitFor can map to a CLI
// exit code.
func callOperation(ctx context.Context, op contract.Operator, operation string, input map[string]any) (contract.Result, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return contract.Result{}, fmt.Errorf("encoding request input: %w", err)
	}
	res, err := op.Call(ctx, operation, contract.Request{Schema: contract.SchemaRequest, Input: raw})
	if err != nil {
		return contract.Result{}, err
	}
	if res.Status != contract.StatusCompleted {
		if res.Error != nil {
			return contract.Result{}, res.Error
		}
		return contract.Result{}, fmt.Errorf("status %s", res.Status)
	}
	return res, nil
}

// readSecretLine reads one line from in, trimming its trailing newline.
// The returned bytes are the raw credential; the caller owns zeroing them.
func readSecretLine(in io.Reader) ([]byte, error) {
	restore := suppressTerminalEcho()
	defer restore()
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	return []byte(line), nil
}

// suppressTerminalEcho best-effort disables local terminal echo on
// /dev/tty for the duration of a secret prompt. It is a UX nicety, not the
// security boundary itself -- the boundary is that this raw text is never
// written to any operation JSON, log or diagnostic. It is a harmless no-op
// whenever there is no controlling terminal (tests, CI, piped input):
// opening /dev/tty or running stty failures are ignored. cmd/zatiti adds no
// golang.org/x/term dependency for this (root dependency edits are
// integration's scope, not this card's, per cmd/zatiti/AGENTS.md), so this
// shells out to the same `stty` utility interactive shells already rely on.
func suppressTerminalEcho() func() {
	if !toggleEcho(false) {
		return func() {}
	}
	return func() { toggleEcho(true) }
}

// toggleEcho runs `stty [-]echo` against /dev/tty and reports whether it
// ran (not whether the terminal existed already in that state).
func toggleEcho(on bool) bool {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	defer func() { _ = tty.Close() }()
	arg := "-echo"
	if on {
		arg = "echo"
	}
	cmd := exec.Command("stty", arg)
	cmd.Stdin = tty
	return cmd.Run() == nil
}
