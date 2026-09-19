package main

import (
	"fmt"
	"regexp"
	"strings"
)

// The workflow policy needs a syntax tree, and the standard library has no
// YAML parser. This file parses a deliberately strict subset of YAML: block
// mappings, block sequences, single-line flow sequences of scalars, plain and
// quoted scalars, literal and folded block scalars, and comments. Every other
// construct (anchors, aliases, tags, flow mappings, multi-line plain scalars,
// multiple documents, tabs, duplicate keys) is rejected, so a workflow the
// policy cannot read precisely fails validation instead of being guessed at.
// Full YAML conformance is checked independently by actionlint.

const (
	maxYAMLBytes = 1 << 20
	maxYAMLDepth = 32
)

type nodeKind int

const (
	scalarNode nodeKind = iota
	mapNode
	seqNode
)

// node is one value in the parsed tree.
type node struct {
	Kind    nodeKind
	Line    int
	Value   string // scalar text
	Quoted  bool   // scalar was quoted or a block scalar
	Null    bool   // scalar had no value at all
	Comment string // trailing comment on a scalar line
	Entries []entry
	Items   []*node
}

type entry struct {
	Key   string
	Line  int
	Value *node
}

// get returns the value for key in a mapping, or nil.
func (n *node) get(key string) *node {
	if n == nil || n.Kind != mapNode {
		return nil
	}
	for _, e := range n.Entries {
		if e.Key == key {
			return e.Value
		}
	}
	return nil
}

// path walks nested mapping keys and returns nil when any hop is absent.
func (n *node) path(keys ...string) *node {
	cur := n
	for _, k := range keys {
		cur = cur.get(k)
		if cur == nil {
			return nil
		}
	}
	return cur
}

// scalar returns the text of a scalar node and whether it is one.
func (n *node) scalar() (string, bool) {
	if n == nil || n.Kind != scalarNode || n.Null {
		return "", false
	}
	return n.Value, true
}

// strings returns a scalar as a one-element list, or a sequence of scalars.
func (n *node) strings() ([]string, bool) {
	if n == nil {
		return nil, false
	}
	if s, ok := n.scalar(); ok {
		return []string{s}, true
	}
	if n.Kind != seqNode {
		return nil, false
	}
	out := make([]string, 0, len(n.Items))
	for _, it := range n.Items {
		s, ok := it.scalar()
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// walkScalars visits every scalar in the tree with its dotted location.
func (n *node) walkScalars(loc string, visit func(loc string, s *node)) {
	switch n.Kind {
	case scalarNode:
		visit(loc, n)
	case mapNode:
		for _, e := range n.Entries {
			e.Value.walkScalars(loc+"."+e.Key, visit)
		}
	case seqNode:
		for i, it := range n.Items {
			it.walkScalars(fmt.Sprintf("%s[%d]", loc, i), visit)
		}
	}
}

type yamlLine struct {
	num     int
	indent  int
	content string // without indentation; never blank or comment-only
	raw     string
}

type yamlParser struct {
	raw   []string // every physical line, for block scalars
	lines []yamlLine
	pos   int
}

var (
	keyPattern         = regexp.MustCompile(`^([A-Za-z0-9_][A-Za-z0-9_.-]*):(?:[ ]+(.*))?$`)
	blockHeaderPattern = regexp.MustCompile(`^([|>])([+-]?)$`)
)

func parseYAML(src []byte) (*node, error) {
	if len(src) > maxYAMLBytes {
		return nil, fmt.Errorf("document exceeds %d bytes", maxYAMLBytes)
	}
	if strings.ContainsRune(string(src), '\r') {
		return nil, fmt.Errorf("carriage returns are not supported")
	}
	p := &yamlParser{raw: strings.Split(string(src), "\n")}
	for i, raw := range p.raw {
		trimmed := strings.TrimLeft(raw, " ")
		if strings.HasPrefix(trimmed, "\t") {
			return nil, fmt.Errorf("line %d: tab in indentation", i+1)
		}
		content := strings.TrimRight(trimmed, " ")
		if content == "" || strings.HasPrefix(content, "#") {
			continue
		}
		p.lines = append(p.lines, yamlLine{num: i + 1, indent: len(raw) - len(trimmed), content: content, raw: raw})
	}
	if len(p.lines) == 0 {
		return nil, fmt.Errorf("document is empty")
	}
	if p.lines[0].indent != 0 {
		return nil, fmt.Errorf("line %d: document must start at column 0", p.lines[0].num)
	}
	root, err := p.parseBlock(0, 0)
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.lines) {
		return nil, fmt.Errorf("line %d: unexpected indentation", p.lines[p.pos].num)
	}
	return root, nil
}

func (p *yamlParser) peek() (yamlLine, bool) {
	if p.pos >= len(p.lines) {
		return yamlLine{}, false
	}
	return p.lines[p.pos], true
}

func isSeqItem(content string) bool {
	return content == "-" || strings.HasPrefix(content, "- ")
}

func (p *yamlParser) parseBlock(indent, depth int) (*node, error) {
	if depth > maxYAMLDepth {
		return nil, fmt.Errorf("line %d: nesting exceeds %d levels", p.lines[p.pos].num, maxYAMLDepth)
	}
	ln := p.lines[p.pos]
	if ln.content == "---" || ln.content == "..." || strings.HasPrefix(ln.content, "--- ") {
		return nil, fmt.Errorf("line %d: document markers are not supported", ln.num)
	}
	if isSeqItem(ln.content) {
		return p.parseSeq(indent, depth)
	}
	return p.parseMap(indent, depth)
}

func (p *yamlParser) parseMap(indent, depth int) (*node, error) {
	out := &node{Kind: mapNode, Line: p.lines[p.pos].num}
	seen := map[string]bool{}
	for {
		ln, ok := p.peek()
		if !ok || ln.indent < indent {
			return out, nil
		}
		if ln.indent > indent {
			return nil, fmt.Errorf("line %d: unexpected indentation", ln.num)
		}
		if isSeqItem(ln.content) {
			return nil, fmt.Errorf("line %d: sequence item inside a mapping; indent sequences under their key", ln.num)
		}
		m := keyPattern.FindStringSubmatch(ln.content)
		if m == nil {
			return nil, fmt.Errorf("line %d: expected a plain \"key: value\" entry", ln.num)
		}
		key, rest := m[1], strings.TrimSpace(m[2])
		if seen[key] {
			return nil, fmt.Errorf("line %d: duplicate key %q", ln.num, key)
		}
		seen[key] = true
		p.pos++
		val, err := p.parseValue(rest, ln, indent, depth)
		if err != nil {
			return nil, err
		}
		out.Entries = append(out.Entries, entry{Key: key, Line: ln.num, Value: val})
	}
}

// parseValue parses what follows "key:" or "- " on line ln, whose owning
// block sits at the given indent.
func (p *yamlParser) parseValue(rest string, ln yamlLine, indent, depth int) (*node, error) {
	if rest == "" || strings.HasPrefix(rest, "#") {
		next, ok := p.peek()
		if !ok || next.indent <= indent {
			return &node{Kind: scalarNode, Line: ln.num, Null: true}, nil
		}
		return p.parseBlock(next.indent, depth+1)
	}
	header := rest
	if i := strings.Index(header, " #"); i >= 0 {
		header = strings.TrimSpace(header[:i])
	}
	if m := blockHeaderPattern.FindStringSubmatch(header); m != nil {
		return p.parseBlockScalar(m[1], m[2], ln, indent)
	}
	val, err := parseInline(rest, ln.num)
	if err != nil {
		return nil, err
	}
	if next, ok := p.peek(); ok && next.indent > indent {
		return nil, fmt.Errorf("line %d: multi-line plain scalars and nested values after an inline value are not supported", next.num)
	}
	return val, nil
}

func (p *yamlParser) parseBlockScalar(style, chomp string, ln yamlLine, indent int) (*node, error) {
	// Block scalar bodies are read from the physical lines because blank and
	// '#' lines are content there.
	start := ln.num // physical index of the line after the header
	blockIndent := -1
	var body []string
	end := start
	for i := start; i < len(p.raw); i++ {
		raw := p.raw[i]
		if strings.TrimSpace(raw) == "" {
			body = append(body, "")
			end = i + 1
			continue
		}
		lead := len(raw) - len(strings.TrimLeft(raw, " "))
		if lead <= indent {
			break
		}
		if blockIndent < 0 {
			blockIndent = lead
		}
		if lead < blockIndent {
			return nil, fmt.Errorf("line %d: block scalar line is less indented than its first line", i+1)
		}
		body = append(body, strings.TrimRight(raw[blockIndent:], " "))
		end = i + 1
	}
	for len(body) > 0 && body[len(body)-1] == "" {
		body = body[:len(body)-1]
	}
	if blockIndent < 0 {
		return nil, fmt.Errorf("line %d: block scalar has no content", ln.num)
	}
	for p.pos < len(p.lines) && p.lines[p.pos].num <= end {
		p.pos++
	}
	var text string
	if style == "|" {
		text = strings.Join(body, "\n")
	} else {
		var b strings.Builder
		for i, l := range body {
			switch {
			case i == 0:
			case l == "" || body[i-1] == "":
				b.WriteString("\n")
			default:
				b.WriteString(" ")
			}
			b.WriteString(l)
		}
		text = b.String()
	}
	if chomp != "-" {
		text += "\n"
	}
	return &node{Kind: scalarNode, Line: ln.num, Value: text, Quoted: true}, nil
}

func (p *yamlParser) parseSeq(indent, depth int) (*node, error) {
	out := &node{Kind: seqNode, Line: p.lines[p.pos].num}
	for {
		ln, ok := p.peek()
		if !ok || ln.indent < indent {
			return out, nil
		}
		if ln.indent > indent {
			return nil, fmt.Errorf("line %d: unexpected indentation", ln.num)
		}
		if !isSeqItem(ln.content) {
			return nil, fmt.Errorf("line %d: expected a sequence item", ln.num)
		}
		after := strings.TrimPrefix(ln.content, "-")
		rest := strings.TrimLeft(after, " ")
		if rest != "" && keyPattern.MatchString(rest) {
			// "- key: value" opens a mapping whose entries align with key.
			itemIndent := indent + 1 + (len(after) - len(rest))
			p.lines[p.pos].indent = itemIndent
			p.lines[p.pos].content = rest
			item, err := p.parseMap(itemIndent, depth+1)
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, item)
			continue
		}
		if i := strings.Index(rest, " #"); blockHeaderPattern.MatchString(rest) || (i >= 0 && blockHeaderPattern.MatchString(strings.TrimSpace(rest[:i]))) {
			return nil, fmt.Errorf("line %d: block scalars as sequence items are not supported", ln.num)
		}
		p.pos++
		item, err := p.parseValue(rest, ln, indent, depth)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, item)
	}
}

// parseInline parses a single-line value: flow sequence, quoted or plain.
func parseInline(s string, line int) (*node, error) {
	if strings.HasPrefix(s, "[") {
		return parseFlowSeq(s, line)
	}
	val, tail, err := parseScalarToken(s, line, false)
	if err != nil {
		return nil, err
	}
	tail = strings.TrimSpace(tail)
	if tail != "" {
		if !strings.HasPrefix(tail, "#") {
			return nil, fmt.Errorf("line %d: unexpected text after value", line)
		}
		val.Comment = strings.TrimSpace(strings.TrimPrefix(tail, "#"))
	}
	return val, nil
}

// parseScalarToken reads one scalar from the front of s and returns the
// unread remainder. Inside a flow sequence a plain scalar also ends at ','
// and ']'.
func parseScalarToken(s string, line int, inFlow bool) (*node, string, error) {
	if s == "" {
		return nil, "", fmt.Errorf("line %d: missing value", line)
	}
	switch s[0] {
	case '"':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			switch s[i] {
			case '"':
				return &node{Kind: scalarNode, Line: line, Value: b.String(), Quoted: true}, s[i+1:], nil
			case '\\':
				i++
				if i >= len(s) {
					return nil, "", fmt.Errorf("line %d: unterminated escape", line)
				}
				switch s[i] {
				case '"', '\\', '/':
					b.WriteByte(s[i])
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					return nil, "", fmt.Errorf("line %d: unsupported escape \\%c", line, s[i])
				}
			default:
				b.WriteByte(s[i])
			}
		}
		return nil, "", fmt.Errorf("line %d: unterminated double-quoted scalar", line)
	case '\'':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] != '\'' {
				b.WriteByte(s[i])
				continue
			}
			if i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			return &node{Kind: scalarNode, Line: line, Value: b.String(), Quoted: true}, s[i+1:], nil
		}
		return nil, "", fmt.Errorf("line %d: unterminated single-quoted scalar", line)
	case '{', '}', ']', '&', '*', '!', '%', '@', '`', '|', '>', ',', '?':
		return nil, "", fmt.Errorf("line %d: unsupported YAML construct starting with %q", line, s[0])
	}
	end := len(s)
	if i := strings.Index(s, " #"); i >= 0 {
		end = i
	}
	if inFlow {
		if i := strings.IndexAny(s[:end], ",]"); i >= 0 {
			end = i
		}
	}
	plain := strings.TrimSpace(s[:end])
	if plain == "" {
		return nil, "", fmt.Errorf("line %d: missing value", line)
	}
	if strings.Contains(plain, ": ") || strings.HasSuffix(plain, ":") {
		return nil, "", fmt.Errorf("line %d: plain scalar contains a mapping indicator; quote it", line)
	}
	if inFlow && strings.ContainsAny(plain, "[{}") {
		return nil, "", fmt.Errorf("line %d: nested flow collections are not supported", line)
	}
	return &node{Kind: scalarNode, Line: line, Value: plain}, s[end:], nil
}

func parseFlowSeq(s string, line int) (*node, error) {
	out := &node{Kind: seqNode, Line: line}
	rest := strings.TrimSpace(s[1:])
	if strings.HasPrefix(rest, "]") {
		return out, flowTail(rest[1:], line)
	}
	for {
		item, tail, err := parseScalarToken(rest, line, true)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, item)
		tail = strings.TrimSpace(tail)
		switch {
		case strings.HasPrefix(tail, ","):
			rest = strings.TrimSpace(tail[1:])
		case strings.HasPrefix(tail, "]"):
			return out, flowTail(tail[1:], line)
		default:
			return nil, fmt.Errorf("line %d: unterminated flow sequence", line)
		}
	}
}

func flowTail(tail string, line int) error {
	tail = strings.TrimSpace(tail)
	if tail != "" && !strings.HasPrefix(tail, "#") {
		return fmt.Errorf("line %d: unexpected text after flow sequence", line)
	}
	return nil
}
