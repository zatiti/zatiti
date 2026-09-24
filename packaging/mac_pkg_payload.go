package packaging

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strconv"
	"strings"
)

// inspectMacPkgPayload checks the actual package Payload's old ASCII cpio
// stream against the independently signed release plan. It is not a complete
// installer verifier: Apple signing, notarization and Gatekeeper checks remain mandatory.
func inspectMacPkgPayload(ctx context.Context, pkg string, release MacReleaseDescriptor, plan MacDownloadPlan) error {
	binding, err := NewMacPkgBinding(release, plan)
	if err != nil {
		return err
	}
	x, err := openMacPkgXAR(pkg)
	if err != nil {
		return err
	}
	defer func() { _ = x.Close() }()
	entry := x.entries["component.pkg/Payload"]
	if entry.encoding != "application/octet-stream" || entry.length != entry.size {
		return errf(CodeVerificationFailed, "Mac package Payload encoding is unsupported")
	}
	section := io.NewSectionReader(x.file, x.heap+entry.offset, entry.length)
	gz, err := gzip.NewReader(section)
	if err != nil {
		return errf(CodeVerificationFailed, "Mac package Payload gzip is invalid")
	}
	defer func() { _ = gz.Close() }()
	if err := inspectMacCPIO(bufio.NewReader(&macPayloadContextReader{ctx: ctx, reader: gz}), binding, plan); err != nil {
		return err
	}
	if err := inspectMacPkgBOM(ctx, x, binding, plan); err != nil {
		return err
	}
	return inspectMacPkgMetadata(x, release.Version, plan.Arch)
}

type macPayloadContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *macPayloadContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// cpio odc has a fixed 76-byte octal header. The supported producer format
// is deliberately narrow; any alternate format needs a reviewed parser.
func inspectMacCPIO(r *bufio.Reader, b MacPkgBinding, plan MacDownloadPlan) error {
	base := "./Library/Application Support/zatiti-installer/inbox/" + strconv.FormatInt(b.ReleaseSequence, 10) + "/" + b.Arch
	dirs := map[string]uint64{
		".": 0, "./Library": 0, "./Library/Application Support": 0,
		"./Library/Application Support/zatiti-installer":                                                   0o700,
		"./Library/Application Support/zatiti-installer/inbox":                                             0o700,
		"./Library/Application Support/zatiti-installer/inbox/" + strconv.FormatInt(b.ReleaseSequence, 10): 0o700,
		base: 0o700,
	}
	files := map[string]MacDeliveryAsset{base + "/controller.tar.gz": plan.Controller, base + "/desktop.tar.gz": plan.Desktop}
	seen := make(map[string]bool, 11)
	inodes := make(map[[2]uint64]bool, 3)
	for i := 0; i < 12; i++ {
		var hdr [76]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil || string(hdr[:6]) != "070707" {
			return errf(CodeVerificationFailed, "Mac package Payload cpio header is invalid")
		}
		fields := []struct{ from, to int }{{6, 12}, {12, 18}, {18, 24}, {24, 30}, {30, 36}, {36, 42}, {42, 48}, {48, 59}, {59, 65}, {65, 76}}
		var nums [10]uint64
		for j, f := range fields {
			v, err := strconv.ParseUint(string(hdr[f.from:f.to]), 8, 64)
			if err != nil {
				return errf(CodeVerificationFailed, "Mac package Payload cpio field is invalid")
			}
			nums[j] = v
		}
		nameSize, size := nums[8], nums[9]
		if nameSize < 2 || nameSize > 255 || size > uint64(maxMacAssetBytes) {
			return errf(CodeVerificationFailed, "Mac package Payload cpio entry exceeds bounds")
		}
		nameBytes := make([]byte, int(nameSize))
		if _, err := io.ReadFull(r, nameBytes); err != nil || nameBytes[len(nameBytes)-1] != 0 {
			return errf(CodeVerificationFailed, "Mac package Payload cpio name is invalid")
		}
		name := string(nameBytes[:len(nameBytes)-1])
		if strings.IndexByte(name, 0) >= 0 || seen[name] {
			return errf(CodeVerificationFailed, "Mac package Payload has a duplicate or unsafe path")
		}
		if name == "TRAILER!!!" {
			if size != 0 || len(seen) != len(dirs)+3 || i != len(dirs)+3 {
				return errf(CodeVerificationFailed, "Mac package Payload cpio trailer is premature")
			}
			// cpio pads the archive to a 512-byte boundary. Any additional
			// decoded data, including concatenated gzip members, is rejected.
			var tail [512]byte
			n, err := io.ReadFull(r, tail[:])
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				return errf(CodeVerificationFailed, "Mac package Payload has extra data")
			}
			for _, v := range tail[:n] {
				if v != 0 {
					return errf(CodeVerificationFailed, "Mac package Payload has nonzero padding")
				}
			}
			return nil
		}
		seen[name] = true
		mode, linkCount := nums[2], nums[5]
		if wantMode, ok := dirs[name]; ok {
			if mode&0o170000 != 0o040000 || size != 0 || (wantMode != 0 && mode&0o777 != wantMode) || linkCount == 0 {
				return errf(CodeVerificationFailed, "Mac package Payload directory mode is invalid")
			}
			continue
		}
		if mode&0o170000 != 0o100000 || mode&0o777 != 0o600 || linkCount != 1 {
			return errf(CodeVerificationFailed, "Mac package Payload has a nonregular, linked or permissive file")
		}
		inode := [2]uint64{nums[0], nums[1]}
		if inodes[inode] {
			return errf(CodeVerificationFailed, "Mac package Payload file inode is repeated")
		}
		inodes[inode] = true
		var wantSize int64
		var wantDigest string
		if name == base+"/binding.json" {
			bindingBytes, err := EncodeMacPkgBinding(b)
			if err != nil {
				return err
			}
			wantSize = int64(len(bindingBytes))
			sum := sha256.Sum256(bindingBytes)
			wantDigest = hex.EncodeToString(sum[:])
		} else if asset, ok := files[name]; ok {
			wantSize, wantDigest = asset.Size, asset.SHA256
		} else {
			return errf(CodeVerificationFailed, "Mac package Payload has an unexpected file")
		}
		if size != uint64(wantSize) {
			return errf(CodeVerificationFailed, "Mac package Payload file size differs from signed plan")
		}
		h := sha256.New()
		if _, err := io.CopyN(h, r, int64(size)); err != nil || hex.EncodeToString(h.Sum(nil)) != wantDigest {
			return errf(CodeVerificationFailed, "Mac package Payload file bytes differ from signed plan")
		}
	}
	return errf(CodeVerificationFailed, "Mac package Payload has too many entries")
}
