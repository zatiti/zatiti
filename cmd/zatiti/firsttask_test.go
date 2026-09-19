package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/installation"
)

// TestFirstTaskSequenceRunsAsPrinted executes installation.FirstTaskSequence
// — the text doctor prints and `task create --help` shows — command by
// command through the real CLI against a real controller, filling each
// UPPER_CASE placeholder from the previous command's output exactly as an
// operator would. A help example that stops working fails here first.
func TestFirstTaskSequenceRunsAsPrinted(t *testing.T) {
	cfg := serveConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, logs := serveInBackground(t, ctx, cfg)
	env := mapEnv(map[string]string{envStateDir: cfg.StateDir, envSocket: cfg.SocketPath})
	// The real catalog, assembled once: each CLI invocation below would
	// otherwise rebuild the registry over all sixteen modules, which under
	// the race detector costs more than the commands themselves.
	descriptors, err := catalog()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	opts := runOptions{env: env, bootstrapWait: 30 * time.Second, catalog: func() ([]contract.Descriptor, error) { return descriptors, nil }}

	run := func(args ...string) contract.Result {
		t.Helper()
		var out, errb bytes.Buffer
		code := runWith(ctx, args, strings.NewReader(""), &out, &errb, opts)
		if code != 0 {
			t.Fatalf("zatiti %s: exit %d\nstdout: %s\nstderr: %s\nserve log:\n%s", strings.Join(args, " "), code, out.String(), errb.String(), logs.String())
		}
		var res contract.Result
		if err := json.Unmarshal(out.Bytes(), &res); err != nil {
			t.Fatalf("zatiti %s: stdout is not one envelope: %q", strings.Join(args, " "), out.String())
		}
		return res
	}
	field := func(res contract.Result, path ...string) string {
		t.Helper()
		var v any
		if err := json.Unmarshal(res.Data, &v); err != nil {
			t.Fatalf("decode data: %v", err)
		}
		for _, p := range path {
			switch node := v.(type) {
			case map[string]any:
				v = node[p]
			case []any:
				i, err := strconv.Atoi(p)
				if err != nil || i < 0 || i >= len(node) {
					t.Fatalf("path %v: index %q out of range for %d items", path, p, len(node))
				}
				v = node[i]
			default:
				t.Fatalf("path %v: %T is neither object nor array", path, v)
			}
		}
		switch x := v.(type) {
		case string:
			return x
		case float64:
			return strconv.FormatInt(int64(x), 10)
		default:
			t.Fatalf("path %v: unexpected %T", path, v)
			return ""
		}
	}

	init := run("init", "--json", "--input", `{"credential_store":"headless","owner_name":"First Task Owner","headless_key_ref":"installation/owner"}`)
	evidence := []byte(`{"qualification":"self-published evidence for the first task"}`)
	sum := sha256.Sum256(evidence)
	values := map[string]string{
		"INSTALLATION_ID": field(init, "resource", "installation_id"),
		"SIZE":            strconv.Itoa(len(evidence)),
		"DIGEST":          hex.EncodeToString(sum[:]),
		"BASE64":          base64.StdEncoding.EncodeToString(evidence),
	}

	var task contract.Result
	for _, line := range strings.Split(installation.FirstTaskSequence, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		args := splitCommand(t, line)
		if args[0] != "zatiti" {
			t.Fatalf("sequence line does not start with zatiti: %q", line)
		}
		args = args[1:]
		for i, a := range args {
			args[i] = substitute(t, a, values)
		}
		res := run(args...)
		switch strings.Join(args[:2], " ") {
		case "principal list":
			values["OWNER_ID"] = principalOfKind(t, res, contract.KindHuman)
		case "worker list":
			values["WORKER_ID"] = field(res, "items", "0", "id")
		case "artifact upload":
			switch args[2] {
			case "begin":
				values["UPLOAD_ID"] = field(res, "resource", "id")
			case "chunk":
				values["UPLOAD_VERSION"] = field(res, "resource", "version")
			case "finish":
				values["ARTIFACT_ID"] = field(res, "resource", "id")
				if got := field(res, "resource", "digest"); got != values["DIGEST"] {
					t.Fatalf("published evidence digest %s, want %s", got, values["DIGEST"])
				}
			}
		case "task create":
			task = res
		}
	}
	if task.Status != contract.StatusCompleted || field(task, "resource", "state") != "draft" {
		t.Fatalf("the printed sequence did not end in a draft task: %s %s", task.Status, task.Data)
	}

	// doctor prints the same sequence while no budget is configured.
	doctor := run("installation", "doctor", "--json", "--input", `{"scope":{"installation_id":"`+values["INSTALLATION_ID"]+`"}}`)
	var status struct {
		Resource struct {
			Requirements []struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"requirements"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(doctor.Data, &status); err != nil {
		t.Fatalf("doctor data: %v", err)
	}
	found := false
	for _, r := range status.Resource.Requirements {
		if r.Code == "first_task_sequence" && strings.Contains(r.Message, installation.FirstTaskSequence) {
			found = true
		}
	}
	if !found {
		t.Fatalf("doctor does not carry the first-task sequence verbatim: %+v", status.Resource.Requirements)
	}

	// task create --help shows the facts and the same sequence verbatim.
	var help bytes.Buffer
	if code := runWith(ctx, []string{"task", "create", "--help"}, strings.NewReader(""), &help, &help, opts); code != 0 {
		t.Fatalf("task create --help: exit %d\n%s", code, help.String())
	}
	for _, want := range []string{installation.FirstTaskSequence, "INSTALLATION scope", `"XXX"`, "artifact_fault", "budget_unavailable", "principal list"} {
		if !strings.Contains(help.String(), want) {
			t.Fatalf("task create --help lacks %q:\n%s", want, help.String())
		}
	}
	cancel()
	if err := awaitExit(t, done, "context cancellation", logs); err != nil {
		t.Fatalf("serve = %v on shutdown", err)
	}
}

// substitute replaces every UPPER_CASE placeholder present in values and
// fails on one the sequence names but the run has not produced.
func substitute(t *testing.T, arg string, values map[string]string) string {
	t.Helper()
	for _, name := range []string{"INSTALLATION_ID", "OWNER_ID", "WORKER_ID", "UPLOAD_ID", "UPLOAD_VERSION", "ARTIFACT_ID", "SIZE", "DIGEST", "BASE64"} {
		if !strings.Contains(arg, name) {
			continue
		}
		v, ok := values[name]
		if !ok {
			t.Fatalf("placeholder %s is used before any command produced it", name)
		}
		arg = strings.ReplaceAll(arg, name, v)
	}
	return arg
}

func principalOfKind(t *testing.T, res contract.Result, kind string) string {
	t.Helper()
	var items struct {
		Items []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"items"`
	}
	if err := json.Unmarshal(res.Data, &items); err != nil {
		t.Fatalf("principal list data: %v", err)
	}
	for _, p := range items.Items {
		if p.Kind == kind {
			return p.ID
		}
	}
	t.Fatalf("no principal of kind %s in %s", kind, res.Data)
	return ""
}

// splitCommand splits one shell line the way a POSIX shell would for the
// quoting the sequence uses: whitespace separates words and single quotes
// group them.
func splitCommand(t *testing.T, line string) []string {
	t.Helper()
	var args []string
	var cur strings.Builder
	inQuote, inWord := false, false
	for _, r := range line {
		switch {
		case r == '\'':
			inQuote = !inQuote
			inWord = true
		case !inQuote && (r == ' ' || r == '\t'):
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inQuote {
		t.Fatalf("unterminated quote in sequence line: %q", line)
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args
}
