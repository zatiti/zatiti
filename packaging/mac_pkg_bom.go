package packaging

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// inspectMacPkgBOM independently checks the package's actual BOM bytes using
// the fixed macOS system decoder. An unavailable tool or unrecognized output
// fails closed. The CPIO stream must still be checked separately.
func inspectMacPkgBOM(ctx context.Context, x *macPkgXAR, b MacPkgBinding, plan MacDownloadPlan) error {
	bindingRaw, err := EncodeMacPkgBinding(b)
	if err != nil {
		return err
	}
	raw, err := x.readSmallMember("component.pkg/Bom")
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(raw, []byte("BOMStore")) {
		return errf(CodeVerificationFailed, "Mac package BOM header is invalid")
	}
	dir, err := os.MkdirTemp("", "zatiti-bom-*")
	if err != nil {
		return errWrap(CodePrerequisiteMissing, "private BOM inspection directory is unavailable", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.Chmod(dir, 0o700); err != nil {
		return errWrap(CodePrerequisiteMissing, "private BOM inspection directory cannot be secured", err)
	}
	p := filepath.Join(dir, "Bom")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		return errWrap(CodePrerequisiteMissing, "BOM inspection bytes cannot be staged", err)
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, "/usr/bin/lsbom", "-p", "mfs", p)
	cmd.Env = []string{"LC_ALL=C", "PATH=/usr/bin:/bin"}
	output := &boundedBOMOutput{}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return errWrap(CodeVerificationFailed, "Mac package BOM cannot be decoded by the system tool", err)
	}
	base := "./Library/Application Support/zatiti-installer/inbox/" + strconv.FormatInt(b.ReleaseSequence, 10) + "/" + b.Arch
	want := map[string]string{
		".": "dir", "./Library": "dir", "./Library/Application Support": "dir",
		"./Library/Application Support/zatiti-installer":                                                   "40700",
		"./Library/Application Support/zatiti-installer/inbox":                                             "40700",
		"./Library/Application Support/zatiti-installer/inbox/" + strconv.FormatInt(b.ReleaseSequence, 10): "40700",
		base:                        "40700",
		base + "/binding.json":      "100600\t" + strconv.Itoa(len(bindingRaw)),
		base + "/controller.tar.gz": "100600\t" + strconv.FormatInt(plan.Controller.Size, 10),
		base + "/desktop.tar.gz":    "100600\t" + strconv.FormatInt(plan.Desktop.Size, 10),
	}
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != len(want) {
		return errf(CodeVerificationFailed, "Mac package BOM inventory differs from signed plan")
	}
	seen := make(map[string]bool, len(want))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || seen[fields[1]] {
			return errf(CodeVerificationFailed, "Mac package BOM entry is ambiguous")
		}
		seen[fields[1]] = true
		expected, ok := want[fields[1]]
		if !ok {
			return errf(CodeVerificationFailed, "Mac package BOM has an unexpected path")
		}
		if expected == "dir" {
			if !strings.HasPrefix(fields[0], "40") || fields[2] != "" {
				return errf(CodeVerificationFailed, "Mac package BOM ancestor is not a directory")
			}
		} else if expected == "40700" {
			if fields[0] != expected || fields[2] != "" {
				return errf(CodeVerificationFailed, "Mac package BOM directory mode is unsafe")
			}
		} else if fields[0]+"\t"+fields[2] != expected {
			return errf(CodeVerificationFailed, "Mac package BOM entry differs from signed plan")
		}
	}
	return nil
}

type boundedBOMOutput struct{ bytes.Buffer }

func (w *boundedBOMOutput) Write(p []byte) (int, error) {
	if w.Len()+len(p) > 16<<10 {
		return 0, errors.New("BOM output exceeds bound")
	}
	return w.Buffer.Write(p)
}
