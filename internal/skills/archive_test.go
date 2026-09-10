package skills

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Z05: the skill archive is untrusted data. Every safety fence the extractor
// pins — traversal, absolute paths, backslashes, drive letters, dot segments,
// NUL and control bytes, non-UTF-8 names, symlinks, devices and special
// entries, duplicates and case collisions, depth, entry count, expansion —
// must reject before any byte reaches storage.

func TestExtractArchiveValidControl(t *testing.T) {
	zipData := buildZip([]zipEntry{
		{Name: "SKILL.md", Data: []byte(minimalSkillMD)},
		{Name: "scripts/", Mode: fs.ModeDir | 0o755},
		{Name: "scripts/run.sh", Data: []byte("#!/bin/sh\n")},
	})
	files, err := extractSkillArchive(zipData)
	if err != nil {
		t.Fatalf("valid control archive rejected: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2 (directories are skipped)", len(files))
	}
	if files[0].Path != "SKILL.md" || files[1].Path != "scripts/run.sh" {
		t.Fatalf("files not in path order: %+v", files)
	}
	if string(files[0].Data) != minimalSkillMD {
		t.Fatalf("SKILL.md not retained byte-exact")
	}
}

func TestExtractArchiveRejections(t *testing.T) {
	md := []byte(minimalSkillMD)
	cases := []struct {
		name    string
		entries []zipEntry
	}{
		{"traversal", []zipEntry{{Name: "../evil.txt", Data: md}}},
		{"absolute path", []zipEntry{{Name: "/etc/passwd", Data: md}}},
		{"backslash separator", []zipEntry{{Name: "scripts\\run.sh", Data: md}}},
		{"drive letter", []zipEntry{{Name: "C:/evil.txt", Data: md}}},
		{"dot segment", []zipEntry{{Name: "./SKILL.md", Data: md}}},
		{"nul byte in name", []zipEntry{{Name: "sk\x00ill.md", Data: md}}},
		{"control character in name", []zipEntry{{Name: "sk\x01ill.md", Data: md}}},
		{"non utf-8 name", []zipEntry{{Name: "sk\xffill.md", Data: md}}},
		{"symlink entry", []zipEntry{
			{Name: "SKILL.md", Data: md},
			{Name: "link", Mode: fs.ModeSymlink | 0o777},
		}},
		{"device entry", []zipEntry{
			{Name: "SKILL.md", Data: md},
			{Name: "dev", Mode: fs.ModeDevice | fs.ModeCharDevice | 0o644},
		}},
		{"named pipe entry", []zipEntry{
			{Name: "SKILL.md", Data: md},
			{Name: "pipe", Mode: fs.ModeNamedPipe | 0o644},
		}},
		{"socket entry", []zipEntry{
			{Name: "SKILL.md", Data: md},
			{Name: "sock", Mode: fs.ModeSocket | 0o644},
		}},
		{"file name with directory metadata", []zipEntry{
			{Name: "SKILL.md", Data: md},
			{Name: "sub", Mode: fs.ModeDir | 0o755},
		}},
		{"duplicate path", []zipEntry{
			{Name: "SKILL.md", Data: md},
			{Name: "a.txt", Data: []byte("1")},
			{Name: "a.txt", Data: []byte("2")},
		}},
		{"case collision", []zipEntry{
			{Name: "SKILL.md", Data: md},
			{Name: "skill.md", Data: md},
		}},
		{"missing skill manifest", []zipEntry{{Name: "README.md", Data: md}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := extractSkillArchive(buildZip(tc.entries))
			if err == nil {
				t.Fatal("adversarial archive accepted")
			}
			var f *contract.Fault
			if !asFault(err, &f) || f.Code != contract.CodeInvalidInput {
				t.Fatalf("rejection fault = %v, want invalid_input", err)
			}
		})
	}
}

func TestExtractArchiveDepthAndCount(t *testing.T) {
	md := []byte(minimalSkillMD)

	t.Run("depth over 32", func(t *testing.T) {
		deep := strings.Repeat("a/", 33) + "x.txt" // 34 segments
		_, err := extractSkillArchive(buildZip([]zipEntry{
			{Name: "SKILL.md", Data: md},
			{Name: deep, Data: []byte("x")},
		}))
		if err == nil {
			t.Fatal("over-deep archive accepted")
		}
		var f *contract.Fault
		if !asFault(err, &f) || f.Code != contract.CodeInvalidInput {
			t.Fatalf("rejection fault = %v, want invalid_input", err)
		}
	})

	t.Run("more than 4096 entries", func(t *testing.T) {
		entries := make([]zipEntry, 0, maxArchiveEntries+1)
		entries = append(entries, zipEntry{Name: "SKILL.md", Data: md})
		for i := 0; i < maxArchiveEntries; i++ {
			entries = append(entries, zipEntry{Name: fmt.Sprintf("f%04d.txt", i)})
		}
		_, err := extractSkillArchive(buildZip(entries))
		if err == nil {
			t.Fatal("over-count archive accepted")
		}
		var f *contract.Fault
		if !asFault(err, &f) || f.Code != contract.CodeInvalidInput {
			t.Fatalf("rejection fault = %v, want invalid_input", err)
		}
	})

	t.Run("declared expansion over 64 MiB", func(t *testing.T) {
		chunk := bytes.Repeat([]byte{0}, 17*1024)
		entries := make([]zipEntry, 0, maxArchiveEntries)
		for i := 0; i < maxArchiveEntries; i++ {
			entries = append(entries, zipEntry{Name: fmt.Sprintf("f%04d.txt", i), Data: chunk})
		}
		_, err := extractSkillArchive(buildZip(entries))
		if err == nil {
			t.Fatal("over-expansion archive accepted")
		}
		var f *contract.Fault
		if !asFault(err, &f) || f.Code != contract.CodeInvalidInput {
			t.Fatalf("rejection fault = %v, want invalid_input", err)
		}
	})
}

// TestExtractArchiveExpansionBomb proves a lying declared size cannot bypass
// the expansion budget: the central directory claims 1000 uncompressed bytes
// while the deflate stream actually inflates to ~70 MiB. readBounded must
// refuse at the remaining budget.
func TestExtractArchiveExpansionBomb(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, err := w.CreateHeader(&zip.FileHeader{Name: "SKILL.md", Method: zip.Deflate})
	if err != nil {
		t.Fatalf("zip header: %v", err)
	}
	chunk := make([]byte, 1<<20) // 1 MiB of zeros
	for i := 0; i < 70; i++ {
		if _, err := fw.Write(chunk); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	zipData := buf.Bytes()

	// Forge the central directory's uncompressed size field down. Central
	// header layout: signature(4) versionMadeBy(2) versionNeeded(2)
	// flags(2) method(2) time(2) date(2) crc(4) compressedSize(4)
	// uncompressedSize(4) — the size sits at offset 24.
	at := bytes.LastIndex(zipData, []byte("PK\x01\x02"))
	if at < 0 {
		t.Fatal("no central directory in fixture")
	}
	binary.LittleEndian.PutUint32(zipData[at+24:at+28], 1000)

	_, err = extractSkillArchive(zipData)
	if err == nil {
		t.Fatal("expansion bomb accepted")
	}
	var f *contract.Fault
	if !asFault(err, &f) || f.Code != contract.CodeInvalidInput {
		t.Fatalf("rejection fault = %v, want invalid_input", err)
	}
}

// TestImportRejectsAdversarialArchive drives one corpus case through the
// full import and proves nothing leaks: no version row, no published blobs,
// no staged configuration change, no event.
func TestImportRejectsAdversarialArchive(t *testing.T) {
	e := newEnv(t)
	zipData := buildZip([]zipEntry{
		{Name: "SKILL.md", Data: []byte(minimalSkillMD)},
		{Name: "../escape.txt", Data: []byte("x")},
	})
	e.runImportExpectFault(e.importFixture(zipData), contract.CodeInvalidInput)

	rows, err := e.skillsRows()
	if err != nil {
		t.Fatalf("skills rows: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("skills rows after rejected import = %d, want 0", len(rows))
	}
	if e.blobs.publishCalls() != 0 {
		t.Fatalf("published blobs = %d, want 0", e.blobs.publishCalls())
	}
	if calls := e.ports.callsOf("_configuration.stage"); len(calls) != 0 {
		t.Fatalf("configuration stage calls = %d, want 0", len(calls))
	}
	events, err := e.db.Events(e.ctx, 0, 100)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0", len(events))
	}
}
