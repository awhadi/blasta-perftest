package sql

import "strings"

// isReadOnly reports whether every statement in q is a read-only statement.
//
// A load test that writes can corrupt data, so this gate is deliberately
// conservative: it strips comments and string/identifier literals to find the
// real keywords, rejects any statement it does not positively recognise as a
// read, and rejects multi-statement input so a trailing DELETE cannot hide
// behind a leading SELECT.
func isReadOnly(q string) bool {
	stmts := splitStatements(stripLiterals(q))
	if len(stmts) == 0 {
		return false
	}
	for _, s := range stmts {
		if !stmtReadOnly(s) {
			return false
		}
	}
	return true
}

func stmtReadOnly(s string) bool {
	words := leadingWords(s, 1)
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "SELECT", "EXPLAIN", "SHOW", "TABLE", "VALUES", "WITH":
	default:
		return false
	}
	// A read-looking leading verb is not enough. "WITH d AS (DELETE FROM t
	// RETURNING *) SELECT * FROM d" reads at the top level but deletes every row
	// it touches, and "EXPLAIN ANALYZE DELETE FROM t" runs the delete for real.
	// hasWriteStatement also descends into CTE bodies, so one scan covers both.
	return !hasWriteStatement(s)
}

// writeKeywords are the words that make a statement (or a CTE body) a write.
var writeKeywords = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true,
	"TRUNCATE": true, "DROP": true, "ALTER": true, "CREATE": true,
	"GRANT": true, "REVOKE": true, "COPY": true, "CALL": true,
	"DO": true, "REINDEX": true, "REFRESH": true, "LOCK": true,
	"INTO": true, // "SELECT ... INTO new_table" creates a table in Postgres
}

// hasWriteStatement scans s for a write, following CTE bodies.
//
// Two shapes are covered: a write keyword at the statement's top level (as in
// "WITH x AS (...) DELETE FROM t" or "EXPLAIN ANALYZE DELETE FROM t"), and a
// write at the head of a CTE body (as in "WITH d AS (DELETE ...) SELECT").
func hasWriteStatement(s string) bool {
	depth := 0
	lastWord := "" // last top-level word seen, to spot a CTE's "AS (...)"
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '(':
			if depth == 0 && isCTEKeyword(lastWord) {
				if body := balancedBody(s, i); body != "" && hasWriteStatement(body) {
					return true
				}
			}
			depth++
			i++
		case c == ')':
			depth--
			i++
		case isWordByte(c):
			j := i
			for j < len(s) && isWordByte(s[j]) {
				j++
			}
			w := strings.ToUpper(s[i:j])
			if depth == 0 {
				if writeKeywords[w] {
					return true
				}
				lastWord = w
			}
			i = j
		default:
			i++
		}
	}
	return false
}

// isCTEKeyword reports whether w can immediately precede a CTE's parenthesised
// body: "AS (...)" and "AS MATERIALIZED (...)".
func isCTEKeyword(w string) bool {
	return w == "AS" || w == "MATERIALIZED"
}

// balancedBody returns the text inside the parentheses starting at open.
func balancedBody(s string, open int) string {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[open+1 : i]
			}
		}
	}
	return ""
}

// splitStatements splits on semicolons, dropping empty trailing fragments.
func splitStatements(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ";") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// stripLiterals blanks out comments, string literals, dollar-quoted bodies, and
// quoted identifiers so keyword scanning cannot be fooled by their contents.
func stripLiterals(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			b.WriteByte(' ')
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i = skipBlockComment(s, i)
			b.WriteByte(' ')
		case c == '\'':
			i = skipQuoted(s, i, '\'')
			b.WriteString("''")
		case c == '"':
			i = skipQuoted(s, i, '"')
			b.WriteString(`""`)
		case c == '$':
			if end, ok := skipDollarQuoted(s, i); ok {
				i = end
				b.WriteString("''")
			} else {
				i++
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// skipBlockComment returns the index just past the "/* ... */" starting at i,
// accounting for nested /* */ which Postgres allows.
func skipBlockComment(s string, i int) int {
	depth := 1
	i += 2
	for i < len(s) && depth > 0 {
		switch {
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			depth++
			i += 2
		case s[i] == '*' && i+1 < len(s) && s[i+1] == '/':
			depth--
			i += 2
		default:
			i++
		}
	}
	return i
}

func skipQuoted(s string, i int, q byte) int {
	i++ // opening quote
	for i < len(s) {
		if s[i] == q {
			if i+1 < len(s) && s[i+1] == q { // a doubled quote is an escape
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return len(s)
}

// skipDollarQuoted handles Postgres dollar-quoted strings: $$body$$ or
// $tag$body$tag$. It returns the index just past the closing tag.
func skipDollarQuoted(s string, i int) (int, bool) {
	tagEnd := i + 1
	for tagEnd < len(s) && isWordByte(s[tagEnd]) {
		tagEnd++
	}
	if tagEnd >= len(s) || s[tagEnd] != '$' {
		return i, false
	}
	tag := s[i : tagEnd+1]
	rest := s[tagEnd+1:]
	idx := strings.Index(rest, tag)
	if idx < 0 {
		return len(s), true
	}
	return tagEnd + 1 + idx + len(tag), true
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c >= 0x80
}

// leadingWords returns up to n leading bare words, upper-cased.
func leadingWords(s string, n int) []string {
	var out []string
	for i := 0; i < len(s) && len(out) < n; {
		if !isWordByte(s[i]) {
			i++
			continue
		}
		j := i
		for j < len(s) && isWordByte(s[j]) {
			j++
		}
		out = append(out, strings.ToUpper(s[i:j]))
		i = j
	}
	return out
}
