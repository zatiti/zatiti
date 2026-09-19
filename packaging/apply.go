package packaging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ServiceManager loads and unloads launchers. Both verbs are idempotent. The
// implementations in servicemanager.go drive launchctl and systemctl; tests
// and dry runs supply their own.
type ServiceManager interface {
	Do(ctx context.Context, action ServiceAction) error
}

// Apply executes a plan: Prepare, then Before, Steps and After. It
// re-validates the layout and every step, and refuses a plan whose view of
// the installed release is stale.
//
// When a step fails after the services were unloaded but before the active
// link moved, the old release and its launchers are intact, so Apply loads
// the services again before returning the failure.
func Apply(ctx context.Context, plan Plan, services ServiceManager) error {
	if err := checkPlan(plan); err != nil {
		return err
	}
	if len(plan.Before)+len(plan.After) != 0 && services == nil {
		return errf(CodePrerequisiteMissing, "the plan changes services and no service manager is configured")
	}
	if plan.Kind != PlanUninstall {
		inst, err := Inspect(plan.Layout)
		if err != nil {
			return err
		}
		if inst.Current != plan.PreviousVersion {
			return errf(CodeConflict, "the active release changed after the plan was built")
		}
	}
	for _, step := range plan.Prepare {
		if err := runStep(ctx, plan.Layout, step); err != nil {
			return err
		}
	}
	for _, action := range plan.Before {
		if err := doAction(ctx, services, action); err != nil {
			return err
		}
	}
	linkMoved := false
	for _, step := range plan.Steps {
		if err := runStep(ctx, plan.Layout, step); err != nil {
			if plan.Kind == PlanUpgrade && !linkMoved {
				for _, action := range plan.After {
					// Best effort: the step failure is the error the
					// caller must see.
					_ = doAction(context.WithoutCancel(ctx), services, action)
				}
			}
			return err
		}
		if step.Action == ActionSwapLink {
			linkMoved = true
		}
	}
	for _, action := range plan.After {
		if err := doAction(ctx, services, action); err != nil {
			return err
		}
	}
	return nil
}

func doAction(ctx context.Context, services ServiceManager, action ServiceAction) error {
	if err := ctx.Err(); err != nil {
		return errWrap(CodeInternalError, "the plan was cancelled before it completed", err)
	}
	return services.Do(ctx, action)
}

// checkPlan proves that every step stays inside the layout: writes land in
// the distribution directory or directly in the launcher directory, and the
// only step that may name the state directory is its confirmed removal.
func checkPlan(plan Plan) error {
	l := plan.Layout
	if err := l.validate(); err != nil {
		return err
	}
	if plan.Kind != PlanInstall && plan.Kind != PlanUpgrade && plan.Kind != PlanUninstall {
		return errf(CodeInvalidInput, "the plan kind is unknown")
	}
	if plan.RemovesState && plan.Kind != PlanUninstall {
		return errf(CodeInvalidInput, "only an uninstall may remove state")
	}
	for _, action := range append(append([]ServiceAction{}, plan.Before...), plan.After...) {
		if l.Manager == ManagerNone {
			return errf(CodeInvalidInput, "a desktop layout runs no service")
		}
		if action.Verb != VerbLoad && action.Verb != VerbUnload {
			return errf(CodeInvalidInput, "the plan holds an unknown service verb")
		}
		if !launchdLabelPattern.MatchString(action.Label) || action.UnitPath != filepath.Join(l.UnitDir, unitFileName(l.Manager, action.Label)) {
			return errf(CodeInvalidInput, "a service action must name a launcher in the layout's launcher directory")
		}
	}
	for _, step := range append(append([]Step{}, plan.Prepare...), plan.Steps...) {
		if !cleanAbs(step.Path) {
			return errf(CodeInvalidInput, "a plan step must name a clean absolute path")
		}
		inDist := pathWithin(step.Path, l.DistRoot)
		isUnit := filepath.Dir(step.Path) == l.UnitDir
		ok := false
		switch step.Action {
		case ActionEnsureDir:
			ok = inDist || step.Path == l.UnitDir
		case ActionResetDir, ActionCopyFile:
			ok = inDist && step.Path != l.DistRoot
		case ActionWriteFile, ActionRemoveFile:
			ok = (inDist && step.Path != l.DistRoot) || isUnit
		case ActionPublishDir:
			ok = filepath.Dir(step.Path) == l.Versions && cleanAbs(step.Target) && filepath.Dir(step.Target) == l.Versions
		case ActionSwapLink:
			version := versionPattern.MatchString(filepath.Base(step.Target))
			switch {
			case step.Path == l.Current:
				ok = filepath.Dir(step.Target) == dirNameVersions && version
			case l.CurrentBundle != "" && step.Path == l.CurrentBundle:
				ok = filepath.Dir(step.Target) == dirNameBundles && version
			case l.Manager == ManagerNone && isUnit:
				// The application link in the applications directory
				// points into the active bundle.
				ok = cleanAbs(step.Target) && pathWithin(step.Target, l.CurrentBundle) && step.Target != l.CurrentBundle
			}
		case ActionExtractBundle:
			ok = l.Bundles != "" && filepath.Dir(step.Path) == l.Bundles && cleanAbs(step.Target) && filepath.Dir(step.Target) == l.Bundles &&
				cleanAbs(step.Source) && pathWithin(step.Source, l.Versions) && validateRelPath(string(step.Content)) == nil
		case ActionRemoveTree:
			ok = inDist || (plan.RemovesState && step.Path == l.StateDir)
		}
		if !ok {
			return errf(CodeInvalidInput, "plan step %s reaches outside the installation layout", step.Action)
		}
		if step.Action == ActionCopyFile && (!cleanAbs(step.Source) || pathWithin(step.Source, l.StateDir)) {
			return errf(CodeInvalidInput, "a copy step must name a clean absolute source outside the state directory")
		}
		if step.Mode&^0o755 != 0 {
			return errf(CodeInvalidInput, "a plan step must not grant group or world write, or special, permissions")
		}
	}
	return nil
}

func runStep(ctx context.Context, l Layout, step Step) error {
	if err := ctx.Err(); err != nil {
		return errWrap(CodeInternalError, "the plan was cancelled before it completed", err)
	}
	if err := refuseSymlinkedParents(l, step.Path); err != nil {
		return err
	}
	var err error
	switch step.Action {
	case ActionEnsureDir:
		err = ensureDir(step.Path, step.Mode, pathWithin(step.Path, l.DistRoot))
	case ActionResetDir:
		if err = removeTree(step.Path); err == nil {
			err = ensureDir(step.Path, step.Mode, true)
		}
	case ActionCopyFile:
		err = copyVerified(step)
	case ActionWriteFile:
		err = writeAtomic(step.Path, step.Content, step.Mode)
	case ActionPublishDir:
		if err = os.Rename(step.Path, step.Target); err == nil {
			err = syncDir(filepath.Dir(step.Target))
		}
	case ActionSwapLink:
		err = swapLink(step.Path, step.Target)
	case ActionExtractBundle:
		if err = extractBundle(step.Source, step.Path, step.SHA256, step.Size, string(step.Content)); err == nil {
			if err = os.Rename(step.Path, step.Target); err == nil {
				err = syncDir(filepath.Dir(step.Target))
			}
		}
	case ActionRemoveFile:
		err = removeFile(step.Path)
	case ActionRemoveTree:
		err = removeTree(step.Path)
	}
	if err == nil {
		return nil
	}
	var perr *Error
	if errors.As(err, &perr) {
		return err
	}
	return errWrap(CodeInternalError, "plan step "+step.Action+" failed", err)
}

// refuseSymlinkedParents walks from the distribution, launcher or state
// directory down to the parent of target and refuses any symlink on the way,
// so a planted link cannot redirect a write or a removal. The active release
// link is the only symlink the layout holds, and no step path runs through
// it.
func refuseSymlinkedParents(l Layout, target string) error {
	base := ""
	for _, candidate := range []string{l.DistRoot, l.UnitDir, l.StateDir} {
		if pathWithin(target, candidate) {
			base = candidate
		}
	}
	if base == "" {
		return errf(CodeInvalidInput, "a plan step reaches outside the installation layout")
	}
	dirs := []string{base}
	if target != base {
		rel, err := filepath.Rel(base, filepath.Dir(target))
		if err != nil {
			return errf(CodeInvalidInput, "a plan step reaches outside the installation layout")
		}
		if rel != "." {
			dir := base
			for _, part := range strings.Split(rel, string(filepath.Separator)) {
				dir = filepath.Join(dir, part)
				dirs = append(dirs, dir)
			}
		}
	}
	for _, dir := range dirs {
		info, err := os.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return errf(CodeConflict, "a directory in the installation layout is a symlink; refusing to write through it")
		}
	}
	return nil
}

// ensureDir creates dir and, for directories this package owns, pins the
// mode so the process umask cannot widen or narrow it. A launcher directory
// that already exists keeps the mode the operator gave it.
func ensureDir(dir string, mode fs.FileMode, owned bool) error {
	if err := os.MkdirAll(dir, mode); err != nil {
		return err
	}
	if !owned {
		return nil
	}
	return os.Chmod(dir, mode)
}

// copyVerified copies one distribution file and hashes what it wrote. The
// planner already verified the source tree; this catches a source that
// changed between planning and copying.
func copyVerified(step Step) error {
	info, err := os.Lstat(step.Source)
	if err != nil {
		return errWrap(CodeNotFound, "a distribution file disappeared before it was copied", err)
	}
	if !info.Mode().IsRegular() {
		return errf(CodeVerificationFailed, "a distribution file is no longer a regular file")
	}
	src, err := os.Open(step.Source)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(step.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, step.Mode)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), src)
	if err == nil {
		err = dst.Chmod(step.Mode)
	}
	if err == nil {
		err = dst.Sync()
	}
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	if err == nil && (n != step.Size || hex.EncodeToString(h.Sum(nil)) != step.SHA256) {
		err = errf(CodeVerificationFailed, "a distribution file changed after it was verified")
	}
	if err != nil {
		_ = os.Remove(step.Path)
	}
	return err
}

func writeAtomic(path string, content []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(content)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return syncDir(filepath.Dir(path))
}

// swapLink points link at target atomically. Anything at link that is not a
// symlink was not put there by this package and is left alone.
func swapLink(link, target string) error {
	if info, err := os.Lstat(link); err == nil && info.Mode()&fs.ModeSymlink == 0 {
		return errf(CodeConflict, "the active release link exists but is not a link this package manages")
	}
	next := link + ".next"
	if err := os.Remove(next); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Symlink(target, next); err != nil {
		return err
	}
	if err := os.Rename(next, link); err != nil {
		_ = os.Remove(next)
		return err
	}
	return syncDir(filepath.Dir(link))
}

func removeFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errf(CodeConflict, "a file to remove is a directory; refusing to remove it")
	}
	return os.Remove(path)
}

func removeTree(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errf(CodeConflict, "a directory to remove is a symlink or file; refusing to remove it")
	}
	return os.RemoveAll(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if closeErr := d.Close(); err == nil {
		err = closeErr
	}
	return err
}
