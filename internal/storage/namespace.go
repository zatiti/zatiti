package storage

import (
	"fmt"
	"strings"
)

// Migration namespace validation. Each owner owns tables prefixed with its
// package name and `_`; a migration body may only create, alter, drop or
// write objects inside that namespace. The scanner below enforces the rule
// for standard DDL and DML shapes; review covers anything exotic. It is a
// structural check, not a full SQL grammar.

// token kinds produced by tokenizeSQL.
type tokenKind int

const (
	tokenIdent  tokenKind = iota // unquoted identifier or keyword
	tokenQuoted                  // quoted identifier
	tokenString                  // string literal
	tokenNumber                  // numeric literal
	tokenPunct                   // punctuation
)

type token struct {
	kind  tokenKind
	text  string // identifier text (unquoted for quoted identifiers), literal text, or punctuation character
	upper string // uppercase of text for identifier keyword matching; empty for literals
}

// tokenizeSQL splits SQL text into tokens. It handles line and block
// comments, single-quoted strings, and double-quoted, backtick-quoted and
// bracketed identifiers. Unterminated literals and comments are errors.
func tokenizeSQL(src string) ([]token, error) {
	toks := make([]token, 0, len(src)/4)
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated block comment")
			}
			i += end + 4
		case c == '\'':
			text, next, ok := scanQuoted(src, i, '\'')
			if !ok {
				return nil, fmt.Errorf("unterminated string literal")
			}
			toks = append(toks, token{kind: tokenString, text: text})
			i = next
		case c == '"':
			text, next, ok := scanQuoted(src, i, '"')
			if !ok {
				return nil, fmt.Errorf("unterminated quoted identifier")
			}
			toks = append(toks, token{kind: tokenQuoted, text: text})
			i = next
		case c == '`':
			text, next, ok := scanQuoted(src, i, '`')
			if !ok {
				return nil, fmt.Errorf("unterminated quoted identifier")
			}
			toks = append(toks, token{kind: tokenQuoted, text: text})
			i = next
		case c == '[':
			end := strings.IndexByte(src[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unterminated bracketed identifier")
			}
			toks = append(toks, token{kind: tokenQuoted, text: src[i+1 : i+end]})
			i += end + 1
		case isIdentifierStart(c):
			j := i
			for j < len(src) && isIdentifierPart(src[j]) {
				j++
			}
			text := src[i:j]
			toks = append(toks, token{kind: tokenIdent, text: text, upper: strings.ToUpper(text)})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && isNumberPart(src[j], j == i) {
				j++
			}
			toks = append(toks, token{kind: tokenNumber, text: src[i:j]})
			i = j
		default:
			toks = append(toks, token{kind: tokenPunct, text: string(c)})
			i++
		}
	}
	return toks, nil
}

// scanQuoted reads a quoted run starting at src[i] == quote. Doubled quotes
// inside are escapes and appear unescaped in the returned text.
func scanQuoted(src string, i int, quote byte) (text string, next int, ok bool) {
	var b strings.Builder
	j := i + 1
	for j < len(src) {
		if src[j] != quote {
			b.WriteByte(src[j])
			j++
			continue
		}
		if j+1 < len(src) && src[j+1] == quote {
			b.WriteByte(quote)
			j += 2
			continue
		}
		return b.String(), j + 1, true
	}
	return "", i, false
}

func isIdentifierStart(c byte) bool {
	return c == '_' || c >= 0x80 || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func isIdentifierPart(c byte) bool {
	return isIdentifierStart(c) || '0' <= c && c <= '9' || c == '$'
}

func isNumberPart(c byte, first bool) bool {
	if first {
		return '0' <= c && c <= '9'
	}
	return '0' <= c && c <= '9' || c == '.' || c == 'x' || c == 'X' ||
		'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// allowedStatementHeads are the statement types a migration body may contain.
var allowedStatementHeads = map[string]bool{
	"SELECT":  true,
	"WITH":    true,
	"INSERT":  true,
	"REPLACE": true,
	"UPDATE":  true,
	"DELETE":  true,
	"CREATE":  true,
	"ALTER":   true,
	"DROP":    true,
}

// conflictActions skip the optional OR-conflict clause of INSERT, REPLACE
// and UPDATE.
var conflictActions = map[string]bool{
	"ROLLBACK": true,
	"ABORT":    true,
	"REPLACE":  true,
	"FAIL":     true,
	"IGNORE":   true,
}

// validateMigrationNamespace checks that every statement in a migration body
// is an allowed statement type and only touches objects prefixed with
// owner_. Trigger bodies, conflict clauses and quoted identifiers are
// handled; a qualified schema name in any captured position is rejected.
func validateMigrationNamespace(owner, sqlText string) error {
	toks, err := tokenizeSQL(sqlText)
	if err != nil {
		return err
	}
	prefix := owner + "_"
	for _, stmt := range splitStatements(toks) {
		if len(stmt) == 0 {
			continue
		}
		head := stmt[0]
		if head.kind != tokenIdent || !allowedStatementHeads[head.upper] {
			return fmt.Errorf("statement type %q is not permitted in migrations", head.text)
		}
		names, err := captureNames(stmt)
		if err != nil {
			return err
		}
		for _, name := range names {
			if !strings.HasPrefix(strings.ToLower(name), prefix) {
				return fmt.Errorf("object %q is outside the %q namespace (prefix %q)", name, owner, prefix)
			}
		}
	}
	return nil
}

// splitStatements cuts a token slice on top-level semicolons. Semicolons
// inside trigger bodies (BEGIN ... END) and CASE expressions do not split a
// statement: block depth is tracked on BEGIN, CASE and END keywords. Quoted
// identifiers never match keywords, and migrations cannot start a BEGIN
// transaction because BEGIN is not an allowed statement head.
func splitStatements(toks []token) [][]token {
	var stmts [][]token
	start := 0
	depth := 0
	for i, t := range toks {
		if t.kind == tokenIdent {
			switch t.upper {
			case "BEGIN", "CASE":
				depth++
			case "END":
				if depth > 0 {
					depth--
				}
			}
		}
		if t.kind == tokenPunct && t.text == ";" && depth == 0 {
			stmts = append(stmts, toks[start:i])
			start = i + 1
		}
	}
	stmts = append(stmts, toks[start:])
	return stmts
}

// captureNames walks one statement and returns every table name the
// statement creates, alters, drops, writes to or references. It errs on
// shapes it cannot reduce to a safe capture.
func captureNames(stmt []token) ([]string, error) {
	var names []string
	capture := func(i int) (string, error) {
		if i >= len(stmt) {
			return "", fmt.Errorf("expected an object name, found end of statement")
		}
		t := stmt[i]
		if t.kind != tokenIdent && t.kind != tokenQuoted {
			return "", fmt.Errorf("expected an object name near %q", t.text)
		}
		if i+1 < len(stmt) && stmt[i+1].kind == tokenPunct && stmt[i+1].text == "." {
			qualified := t.text + "." + stmtText(stmt, i+2)
			return "", fmt.Errorf("qualified name %q is not allowed in migrations", qualified)
		}
		return strings.ToLower(t.text), nil
	}
	is := func(i int, kws ...string) bool {
		if i >= len(stmt) || stmt[i].kind != tokenIdent {
			return false
		}
		for _, kw := range kws {
			if stmt[i].upper == kw {
				return true
			}
		}
		return false
	}

	for i := 0; i < len(stmt); i++ {
		t := stmt[i]
		if t.kind != tokenIdent {
			continue
		}
		// Trigger timing and referential-action clauses reuse the DML
		// keywords: "AFTER UPDATE ON t", "AFTER INSERT ON t", and inside
		// table definitions "ON UPDATE CASCADE" / "ON DELETE SET NULL".
		// Those are not statements and name no new objects.
		if i > 0 && stmt[i-1].kind == tokenIdent {
			switch stmt[i-1].upper {
			case "AFTER", "BEFORE", "INSTEAD", "OF", "ON":
				continue
			}
		}
		switch t.upper {
		case "CREATE":
			j := i + 1
			for j < len(stmt) && stmt[j].kind == tokenIdent &&
				(stmt[j].upper == "TEMP" || stmt[j].upper == "TEMPORARY" || stmt[j].upper == "VIRTUAL" || stmt[j].upper == "UNIQUE") {
				j++
			}
			if !is(j, "TABLE", "INDEX", "VIEW", "TRIGGER") {
				return nil, fmt.Errorf("unsupported CREATE target near %q", stmtText(stmt, j))
			}
			object := stmt[j].upper
			j++
			for j < len(stmt) && stmt[j].kind == tokenIdent &&
				(stmt[j].upper == "IF" || stmt[j].upper == "NOT" || stmt[j].upper == "EXISTS") {
				j++
			}
			name, err := capture(j)
			if err != nil {
				return nil, fmt.Errorf("CREATE %s: %w", strings.ToLower(object), err)
			}
			names = append(names, name)
			if object == "INDEX" || object == "TRIGGER" {
				for k := j + 1; k < len(stmt); k++ {
					if is(k, "ON") {
						table, err := capture(k + 1)
						if err != nil {
							return nil, fmt.Errorf("CREATE %s: %w", strings.ToLower(object), err)
						}
						names = append(names, table)
						break
					}
				}
			}
		case "ALTER":
			if !is(i+1, "TABLE") {
				return nil, fmt.Errorf("unsupported ALTER form near %q", stmtText(stmt, i+1))
			}
			name, err := capture(i + 2)
			if err != nil {
				return nil, fmt.Errorf("ALTER TABLE: %w", err)
			}
			names = append(names, name)
			for k := i + 2; k+1 < len(stmt); k++ {
				if is(k, "RENAME") && is(k+1, "TO") {
					renamed, err := capture(k + 2)
					if err != nil {
						return nil, fmt.Errorf("ALTER TABLE RENAME TO: %w", err)
					}
					names = append(names, renamed)
					break
				}
			}
		case "DROP":
			if !is(i+1, "TABLE", "INDEX", "VIEW", "TRIGGER") {
				return nil, fmt.Errorf("unsupported DROP target near %q", stmtText(stmt, i+1))
			}
			j := i + 2
			for j < len(stmt) && stmt[j].kind == tokenIdent && (stmt[j].upper == "IF" || stmt[j].upper == "EXISTS") {
				j++
			}
			name, err := capture(j)
			if err != nil {
				return nil, fmt.Errorf("DROP: %w", err)
			}
			names = append(names, name)
		case "INSERT":
			j := i + 1
			if is(j, "OR") && j+1 < len(stmt) && stmt[j+1].kind == tokenIdent && conflictActions[stmt[j+1].upper] {
				j += 2
			}
			if !is(j, "INTO") {
				return nil, fmt.Errorf("unsupported INSERT form near %q", stmtText(stmt, j))
			}
			name, err := capture(j + 1)
			if err != nil {
				return nil, fmt.Errorf("INSERT INTO: %w", err)
			}
			names = append(names, name)
		case "REPLACE":
			if !is(i+1, "INTO") {
				return nil, fmt.Errorf("unsupported REPLACE form near %q", stmtText(stmt, i+1))
			}
			name, err := capture(i + 2)
			if err != nil {
				return nil, fmt.Errorf("REPLACE INTO: %w", err)
			}
			names = append(names, name)
		case "DELETE":
			if !is(i+1, "FROM") {
				return nil, fmt.Errorf("unsupported DELETE form near %q", stmtText(stmt, i+1))
			}
			name, err := capture(i + 2)
			if err != nil {
				return nil, fmt.Errorf("DELETE FROM: %w", err)
			}
			names = append(names, name)
		case "UPDATE":
			j := i + 1
			if is(j, "OR") && j+1 < len(stmt) && stmt[j+1].kind == tokenIdent && conflictActions[stmt[j+1].upper] {
				j += 2
			}
			name, err := capture(j)
			if err != nil {
				return nil, fmt.Errorf("UPDATE: %w", err)
			}
			names = append(names, name)
		case "REFERENCES":
			name, err := capture(i + 1)
			if err != nil {
				return nil, fmt.Errorf("REFERENCES: %w", err)
			}
			names = append(names, name)
		}
	}
	return names, nil
}

// stmtText returns the raw text at position i for error messages.
func stmtText(stmt []token, i int) string {
	if i < len(stmt) {
		return stmt[i].text
	}
	return "end of statement"
}
