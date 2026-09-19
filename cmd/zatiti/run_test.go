package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/client"
	"github.com/zatiti/zatiti/internal/contract"
)

func execRun(t *testing.T, args []string, opts runOptions) (int, string, string) {
	t.Helper()
	if opts.env == nil {
		// t.TempDir paths exceed the socket length bound on macOS; the socket
		// is never bound by these tests but its resolved path is validated.
		opts.env = mapEnv(map[string]string{
			envStateDir: filepath.Join(t.TempDir(), "state"),
			envSocket:   filepath.Join(shortTempDir(t), "s.sock"),
		})
	}
	if opts.catalog == nil {
		opts.catalog = syntheticCatalog
	}
	var out, errb bytes.Buffer
	code := runWith(context.Background(), args, strings.NewReader(""), &out, &errb, opts)
	return code, out.String(), errb.String()
}

func TestHelpListsTransportAndProductCommands(t *testing.T) {
	code, out, _ := execRun(t, []string{"--help"}, runOptions{})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"serve", "mcp", "thing", "init", "--state-dir", "--profile", "--socket", "completion"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help lacks %q:\n%s", want, out)
		}
	}
	code, out, _ = execRun(t, []string{"mcp", "serve", "--help"}, runOptions{})
	if code != 0 || !strings.Contains(out, "--bootstrap") {
		t.Fatalf("mcp serve help: exit %d\n%s", code, out)
	}
}

func TestUnknownCommandExitsInvalidInput(t *testing.T) {
	code, out, errb := execRun(t, []string{"nonsense"}, runOptions{})
	if code != 2 || out != "" || !strings.Contains(errb, "zatiti: ") {
		t.Fatalf("exit %d out %q err %q", code, out, errb)
	}
	code, _, errb = execRun(t, []string{"thing", "list", "--bogus"}, runOptions{})
	if code != 2 || !strings.Contains(errb, "bogus") {
		t.Fatalf("exit %d err %q", code, errb)
	}
}

func TestGlobalFlagsRejectBadProfile(t *testing.T) {
	code, _, errb := execRun(t, []string{"--profile", "../x", "thing", "list"}, runOptions{operator: completedOperator()})
	if code != 2 || !strings.Contains(errb, "profile name") {
		t.Fatalf("exit %d err %q", code, errb)
	}
}

func TestExitCodesFollowTheFaultTable(t *testing.T) {
	cases := []struct {
		name string
		op   contract.Operator
		args []string
		want int
	}{
		{"completed query", completedOperator(), []string{"thing", "list", "--json"}, 0},
		{"completed mutation", completedOperator(), []string{"thing", "create", "--submission-key", "k", "--json"}, 0},
		{"permission denied", faultOperator(contract.CodePermissionDenied), []string{"thing", "list", "--json"}, 3},
		{"conflict", faultOperator(contract.CodeConflict), []string{"thing", "create", "--submission-key", "k"}, 4},
		{"prerequisite missing", faultOperator(contract.CodePrerequisiteMissing), []string{"thing", "list"}, 5},
		{"internal", faultOperator(contract.CodeInternalError), []string{"thing", "list"}, 1},
		{"controller unavailable transport", &fakeOperator{fn: func(context.Context, string, contract.Request) (contract.Result, error) {
			return contract.Result{}, client.ErrControllerUnavailable
		}}, []string{"thing", "list"}, 6},
		{"other transport", &fakeOperator{fn: func(context.Context, string, contract.Request) (contract.Result, error) {
			return contract.Result{}, errors.New("boom")
		}}, []string{"thing", "list"}, 1},
		{"bad inline input never reaches the operator", completedOperator(), []string{"thing", "list", "--input", "{not json"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errb := execRun(t, tc.args, runOptions{operator: tc.op})
			if code != tc.want {
				t.Fatalf("exit %d, want %d (stdout %q stderr %q)", code, tc.want, out, errb)
			}
			if strings.Contains(strings.Join(tc.args, " "), "--json") && code != 2 {
				var env contract.Result
				if err := json.Unmarshal([]byte(out), &env); err != nil {
					t.Fatalf("stdout is not one envelope: %q: %v", out, err)
				}
			}
		})
	}
}

func TestServeRefusesWithoutMasterKey(t *testing.T) {
	code, out, errb := execRun(t, []string{"serve"}, runOptions{})
	if code != 2 || out != "" || !strings.Contains(errb, "master key") {
		t.Fatalf("exit %d out %q err %q", code, out, errb)
	}
}

func TestServeRefusesWhenAnotherControllerHoldsTheLock(t *testing.T) {
	cfg := serveConfig(t)
	// Take the installation lock the way a running controller would.
	holder, err := openPlatformLock(cfg)
	if err != nil {
		t.Fatalf("holding the lock: %v", err)
	}
	defer holder()
	code, out, errb := execRun(t, []string{"serve", "--credential-backend", "headless", "--master-key", cfg.MasterKeyRef, "--socket", cfg.SocketPath}, runOptions{
		env: mapEnv(map[string]string{envStateDir: cfg.StateDir}),
	})
	if code != 6 || out != "" || !strings.Contains(errb, "another controller") {
		t.Fatalf("exit %d out %q err %q", code, out, errb)
	}
}

func TestMCPBootstrapEnderRefusesOtherOperations(t *testing.T) {
	t.Parallel()
	inner := completedOperator()
	ended := false
	e := &bootstrapEnder{inner: inner, end: func() { ended = true }}
	if _, err := e.Call(context.Background(), "thing.list", contract.Request{}); faultCode(err) != contract.CodePermissionDenied {
		t.Fatalf("non-bootstrap call: %v", err)
	}
	if len(inner.calls) != 0 {
		t.Fatal("refused call reached the operator")
	}
	if _, err := e.Call(context.Background(), bootstrapOperation, contract.Request{}); err != nil {
		t.Fatalf("bootstrap call: %v", err)
	}
	if !ended || !e.completed() {
		t.Fatal("completed bootstrap did not end the session")
	}
}

// TestServeRefusesUnbindableSocketPathBeforeAssembly: a socket path the OS
// cannot bind is refused by name before any storage or assembly work, with
// the resolved path, its length and the remedy in the diagnostic.
func TestServeRefusesUnbindableSocketPathBeforeAssembly(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 100))
	code, out, errb := execRun(t, []string{"serve", "--credential-backend", "headless", "--master-key", "file:/nonexistent"}, runOptions{
		env: mapEnv(map[string]string{envStateDir: long}),
	})
	if code != 2 || out != "" {
		t.Fatalf("exit %d out %q err %q", code, out, errb)
	}
	for _, want := range []string{"socket path", strconv.Itoa(len(filepath.Join(long, socketFileName))), "--socket", "--state-dir"} {
		if !strings.Contains(errb, want) {
			t.Fatalf("diagnostic %q does not name %q", errb, want)
		}
	}
	if strings.Contains(errb, "installation opened") || strings.Contains(errb, "adapter") {
		t.Fatalf("assembly ran before the socket path refusal: %q", errb)
	}
}
