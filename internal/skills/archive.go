package skills

import (
	"archive/zip"
	"bytes"
	"io"
	"io/fs"
	"sort"
	"strings"
	"unicode/utf8"
)

// Skill archive limits, carried from the shared wire conventions:
// 16 MiB compressed, 64 MiB expanded, 4096 entries, path depth 32.
const (
	maxArchiveCompressed = 16 << 20
	maxArchiveExpanded   = 64 << 20
	maxArchiveEntries    = 4096
	maxArchiveDepth      = 32
)

// skillManifestName is the required root instruction document.
const skillManifestName = "SKILL.md"

// archiveFile is one validated regular file extracted from the archive.
type archiveFile struct {
	Path string
	Data []byte
}

// extractSkillArchive validates a zip archive against every safety fence —
// path traversal, absolute paths, backslashes, escaping symlinks, device and
// special entries, duplicate and case-colliding paths, depth, entry count
// and oversized expansion — and returns the regular files in path order.
// Nothing extracted here is ever executed; the bytes are untrusted data.
func extractSkillArchive(zipData []byte) ([]archiveFile, error) {
	if len(zipData) > maxArchiveCompressed {
		return nil, invalidInput("skill archive exceeds the %d MiB compressed limit", maxArchiveCompressed>>20)
	}
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, invalidInput("skill archive is not a readable zip: %v", err)
	}
	if len(reader.File) > maxArchiveEntries {
		return nil, invalidInput("skill archive exceeds the %d entry limit", maxArchiveEntries)
	}

	seen := map[string]string{} // lowercased path -> original path
	files := make([]archiveFile, 0, len(reader.File))
	remaining := int64(maxArchiveExpanded)
	var declaredTotal int64
	for _, f := range reader.File {
		name := f.Name
		if !utf8.ValidString(name) {
			return nil, invalidInput("skill archive entry name is not valid UTF-8")
		}
		if name == "" || strings.ContainsRune(name, 0) {
			return nil, invalidInput("skill archive entry name is empty or contains a NUL byte")
		}
		if strings.Contains(name, "\\") {
			return nil, invalidInput("skill archive entry %s uses backslash separators", truncateForMessage(name))
		}
		if strings.HasPrefix(name, "/") {
			return nil, invalidInput("skill archive entry %s is an absolute path", truncateForMessage(name))
		}
		if driveLetter(name) {
			return nil, invalidInput("skill archive entry %s carries a drive-letter prefix", truncateForMessage(name))
		}
		if hasUnsafeSegment(name) {
			return nil, invalidInput("skill archive entry %s contains an unsafe path segment", truncateForMessage(name))
		}
		if hasControlChars(name) {
			return nil, invalidInput("skill archive entry %s contains control characters", truncateForMessage(name))
		}

		isDir := strings.HasSuffix(name, "/")
		normal := strings.TrimSuffix(name, "/")
		depth := strings.Count(normal, "/") + 1
		if depth > maxArchiveDepth {
			return nil, invalidInput("skill archive entry %s exceeds the depth limit of %d", truncateForMessage(name), maxArchiveDepth)
		}
		mode := f.Mode()
		if isDir {
			if mode&fs.ModeDir == 0 {
				return nil, invalidInput("skill archive entry %s is a directory name with non-directory metadata", truncateForMessage(name))
			}
		} else {
			// Only regular files are accepted. Symlinks, devices, pipes,
			// sockets and irregular entries are rejected before any read.
			if mode&(fs.ModeSymlink|fs.ModeDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeIrregular) != 0 {
				return nil, invalidInput("skill archive entry %s is not a regular file", truncateForMessage(name))
			}
			if mode&fs.ModeDir != 0 {
				return nil, invalidInput("skill archive entry %s mixes file name with directory metadata", truncateForMessage(name))
			}
		}

		// Duplicate and case-colliding paths share one namespace; either
		// collision would make extraction order ambiguous.
		lower := strings.ToLower(normal)
		if prev, ok := seen[lower]; ok {
			return nil, invalidInput("skill archive contains duplicate or case-colliding path %s (already present as %s)", truncateForMessage(name), truncateForMessage(prev))
		}
		seen[lower] = name

		// Reject oversized declared expansion before reading anything.
		declared := int64(f.UncompressedSize64)
		declaredTotal += declared
		if declaredTotal > maxArchiveExpanded {
			return nil, invalidInput("skill archive declares more than %d MiB of expanded content", maxArchiveExpanded>>20)
		}
		if isDir {
			continue
		}

		data, err := readBounded(f, remaining)
		if err != nil {
			return nil, err
		}
		remaining -= int64(len(data))
		files = append(files, archiveFile{Path: normal, Data: data})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	hasManifest := false
	for _, f := range files {
		if f.Path == skillManifestName {
			hasManifest = true
			break
		}
	}
	if !hasManifest {
		return nil, invalidInput("skill archive does not contain a root %s", skillManifestName)
	}
	return files, nil
}

// readBounded reads one archive entry, refusing actual bytes beyond the
// remaining expansion budget even when the declared size lies.
func readBounded(f *zip.File, remaining int64) ([]byte, error) {
	if remaining <= 0 {
		return nil, invalidInput("skill archive exceeds the %d MiB expanded limit", maxArchiveExpanded>>20)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, invalidInput("skill archive entry %s cannot be opened: %v", truncateForMessage(f.Name), err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, remaining+1))
	if err != nil {
		return nil, invalidInput("skill archive entry %s cannot be read: %v", truncateForMessage(f.Name), err)
	}
	if int64(len(data)) > remaining {
		return nil, invalidInput("skill archive entry %s exceeds the expansion budget", truncateForMessage(f.Name))
	}
	return data, nil
}

// driveLetter reports Windows drive-letter prefixes such as C:.
func driveLetter(name string) bool {
	if len(name) < 2 || name[1] != ':' {
		return false
	}
	c := name[0]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// hasUnsafeSegment reports dot-dot or dot segments that could escape the
// extraction root.
func hasUnsafeSegment(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." || seg == "." {
			return true
		}
	}
	return false
}

// hasControlChars reports ASCII control characters in a path.
func hasControlChars(name string) bool {
	for i := 0; i < len(name); i++ {
		if name[i] < 0x20 || name[i] == 0x7f {
			return true
		}
	}
	return false
}
