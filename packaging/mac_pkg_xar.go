package packaging

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/xml"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
)

// macPkgXAR is only the bounded archive boundary. Its inventory does not
// establish payload, BOM, Distribution, signer, or notarization validity.
type macPkgXAR struct {
	file    *os.File
	heap    int64
	entries map[string]macXAREntry
}

type macXAREntry struct {
	offset   int64
	length   int64
	size     int64
	encoding string
}

type macXARTOC struct {
	XMLName xml.Name `xml:"xar"`
	TOC     struct {
		Files []macXARFile `xml:"file"`
	} `xml:"toc"`
}

type macXARFile struct {
	Names []string `xml:"name"`
	Type  string   `xml:"type"`
	Data  struct {
		Offset   string `xml:"offset"`
		Length   string `xml:"length"`
		Size     string `xml:"size"`
		Encoding struct {
			Style string `xml:"style,attr"`
		} `xml:"encoding"`
	} `xml:"data"`
	Children []macXARFile `xml:"file"`
}

const maxMacXARTOCBytes = 1 << 20

func openMacPkgXAR(pkg string) (_ *macPkgXAR, err error) {
	f, err := os.Open(pkg)
	if err != nil {
		return nil, errWrap(CodeVerificationFailed, "Mac package cannot be opened", err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 28 || info.Size() > maxMacAssetBytes {
		return nil, errf(CodeVerificationFailed, "Mac package size or file type is invalid")
	}
	var header [28]byte
	if _, err = io.ReadFull(f, header[:]); err != nil {
		return nil, errf(CodeVerificationFailed, "Mac package XAR header is truncated")
	}
	if string(header[:4]) != "xar!" || binary.BigEndian.Uint16(header[4:6]) != 28 || binary.BigEndian.Uint16(header[6:8]) != 1 {
		return nil, errf(CodeVerificationFailed, "Mac package XAR header is unsupported")
	}
	compressed := binary.BigEndian.Uint64(header[8:16])
	expanded := binary.BigEndian.Uint64(header[16:24])
	if compressed == 0 || compressed > maxMacXARTOCBytes || expanded == 0 || expanded > maxMacXARTOCBytes || compressed > uint64(info.Size()-28) {
		return nil, errf(CodeVerificationFailed, "Mac package XAR table exceeds bounds")
	}
	compressedTOC := make([]byte, int(compressed))
	if _, err = io.ReadFull(f, compressedTOC); err != nil {
		return nil, errf(CodeVerificationFailed, "Mac package XAR table is truncated")
	}
	zr, zerr := zlib.NewReader(bytes.NewReader(compressedTOC))
	if zerr != nil {
		return nil, errf(CodeVerificationFailed, "Mac package XAR table compression is invalid")
	}
	decoded, readErr := io.ReadAll(io.LimitReader(zr, int64(expanded)+1))
	closeErr := zr.Close()
	if readErr != nil || closeErr != nil || uint64(len(decoded)) != expanded {
		return nil, errf(CodeVerificationFailed, "Mac package XAR table length differs from header")
	}
	var toc macXARTOC
	if xml.Unmarshal(decoded, &toc) != nil || toc.XMLName.Local != "xar" {
		return nil, errf(CodeVerificationFailed, "Mac package XAR table is invalid XML")
	}
	x := &macPkgXAR{file: f, heap: 28 + int64(compressed), entries: make(map[string]macXAREntry)}
	if len(toc.TOC.Files) != 2 {
		return nil, errf(CodeVerificationFailed, "Mac package has unexpected top-level members")
	}
	for _, member := range toc.TOC.Files {
		if err = x.collect(member, "", info.Size()); err != nil {
			return nil, err
		}
	}
	if len(x.entries) != 4 {
		return nil, errf(CodeVerificationFailed, "Mac package component inventory differs from required shape")
	}
	if _, ok := x.entries["Distribution"]; !ok {
		return nil, errf(CodeVerificationFailed, "Mac package Distribution is missing")
	}
	return x, nil
}

func (x *macPkgXAR) collect(member macXARFile, parent string, fileSize int64) error {
	if len(member.Names) == 0 || len(member.Names) > 2 || member.Names[0] == "" {
		return errf(CodeVerificationFailed, "Mac package XAR member name is invalid")
	}
	for _, name := range member.Names {
		if name != member.Names[0] || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || path.Clean(name) != name {
			return errf(CodeVerificationFailed, "Mac package XAR member path is unsafe")
		}
	}
	name := member.Names[0]
	if parent == "" {
		if name != "Distribution" && name != "component.pkg" {
			return errf(CodeVerificationFailed, "Mac package has an unexpected top-level member")
		}
	} else if parent != "component.pkg" || (name != "Bom" && name != "Payload" && name != "PackageInfo") {
		return errf(CodeVerificationFailed, "Mac package component has an unexpected member")
	}
	full := name
	if parent != "" {
		full = parent + "/" + name
	}
	if member.Type == "directory" {
		if full != "component.pkg" || len(member.Children) != 3 || member.Data.Size != "" {
			return errf(CodeVerificationFailed, "Mac package has an unexpected component directory")
		}
		for _, child := range member.Children {
			if err := x.collect(child, full, fileSize); err != nil {
				return err
			}
		}
		return nil
	}
	if member.Type != "file" || len(member.Children) != 0 || full == "component.pkg" {
		return errf(CodeVerificationFailed, "Mac package XAR member type is unsupported")
	}
	offset, e1 := strconv.ParseInt(member.Data.Offset, 10, 64)
	length, e2 := strconv.ParseInt(member.Data.Length, 10, 64)
	size, e3 := strconv.ParseInt(member.Data.Size, 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || offset < 0 || length <= 0 || size <= 0 || size > maxMacAssetBytes || length > maxMacAssetBytes || offset > fileSize-x.heap || length > fileSize-x.heap-offset {
		return errf(CodeVerificationFailed, "Mac package XAR member extent is invalid")
	}
	style := member.Data.Encoding.Style
	if style != "application/x-gzip" && style != "application/octet-stream" {
		return errf(CodeVerificationFailed, "Mac package XAR member compression is unsupported")
	}
	if _, exists := x.entries[full]; exists {
		return errf(CodeVerificationFailed, "Mac package XAR member is duplicated")
	}
	x.entries[full] = macXAREntry{offset: offset, length: length, size: size, encoding: style}
	return nil
}

func (x *macPkgXAR) Close() error { return x.file.Close() }

// readSmallMember reads and checks a bounded XAR member's actual bytes. The
// Payload is intentionally excluded: it needs streaming CPIO and BOM checks.
func (x *macPkgXAR) readSmallMember(name string) ([]byte, error) {
	entry, ok := x.entries[name]
	if !ok || name == "component.pkg/Payload" || entry.size > maxMacXARTOCBytes {
		return nil, errf(CodeVerificationFailed, "Mac package metadata member is absent or oversized")
	}
	section := io.NewSectionReader(x.file, x.heap+entry.offset, entry.length)
	var reader io.Reader = section
	if entry.encoding == "application/x-gzip" {
		gz, err := zlib.NewReader(section)
		if err != nil {
			return nil, errf(CodeVerificationFailed, "Mac package metadata compression is invalid")
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	} else if entry.length != entry.size {
		return nil, errf(CodeVerificationFailed, "Mac package metadata length differs from declaration")
	}
	data, err := io.ReadAll(io.LimitReader(reader, entry.size+1))
	if err != nil || int64(len(data)) != entry.size {
		return nil, errf(CodeVerificationFailed, "Mac package metadata bytes differ from declaration")
	}
	var trailing [1]byte
	if n, err := reader.Read(trailing[:]); n != 0 || err != io.EOF {
		return nil, errf(CodeVerificationFailed, "Mac package metadata has trailing decoded bytes")
	}
	return data, nil
}
