package contracttrace

import (
	"strings"
	"unicode"
)

type sqlAccess struct{ table, role string }

// sqlTokens omits comments and string literals. This inventories simple SQL,
// not a dialect-complete parser or a query validator.
func sqlTokens(q string) []string {
	tokens := []string{}
	for i := 0; i < len(q); {
		if unicode.IsSpace(rune(q[i])) {
			i++
			continue
		}
		if strings.HasPrefix(q[i:], "--") {
			for i < len(q) && q[i] != '\n' {
				i++
			}
			continue
		}
		if strings.HasPrefix(q[i:], "/*") {
			end := strings.Index(q[i+2:], "*/")
			if end < 0 {
				break
			}
			i += end + 4
			continue
		}
		if q[i] == '\'' {
			i++
			for i < len(q) {
				if q[i] == '\'' {
					i++
					if i < len(q) && q[i] == '\'' {
						i++
						continue
					}
					break
				}
				i++
			}
			continue
		}
		if q[i] == '"' || q[i] == '`' || q[i] == '[' {
			close := q[i]
			if close == '[' {
				close = ']'
			}
			i++
			start := i
			var value strings.Builder
			for i < len(q) {
				if q[i] == close {
					value.WriteString(q[start:i])
					i++
					if i < len(q) && q[i] == close {
						value.WriteByte(close)
						i++
						start = i
						continue
					}
					break
				}
				i++
			}
			tokens = append(tokens, value.String())
			continue
		}
		if unicode.IsLetter(rune(q[i])) || q[i] == '_' {
			start := i
			i++
			for i < len(q) && (unicode.IsLetter(rune(q[i])) || unicode.IsDigit(rune(q[i])) || q[i] == '_') {
				i++
			}
			tokens = append(tokens, q[start:i])
			continue
		}
		tokens = append(tokens, string(q[i]))
		i++
	}
	return tokens
}
func inventorySQLTables(q string) []sqlAccess {
	t := sqlTokens(q)
	result := []sqlAccess{}
	cte := map[string]bool{}
	if len(t) > 0 && strings.EqualFold(t[0], "WITH") {
		for i := 1; i+1 < len(t); i++ {
			if strings.EqualFold(t[i+1], "AS") {
				cte[strings.ToLower(t[i])] = true
			}
		}
	}
	for i := 0; i+1 < len(t); i++ {
		role := ""
		switch strings.ToUpper(t[i]) {
		case "FROM", "JOIN":
			role = "read"
			if i > 0 && strings.EqualFold(t[i-1], "DELETE") {
				role = "write"
			}
		case "INTO", "UPDATE":
			role = "write"
		case "TABLE":
			if i > 0 && (strings.EqualFold(t[i-1], "CREATE") || strings.EqualFold(t[i-1], "ALTER") || strings.EqualFold(t[i-1], "DROP")) {
				role = "schema"
			}
		}
		if role == "" {
			continue
		}
		j := i + 1
		if strings.EqualFold(t[j], "IF") {
			j++
			if j < len(t) && strings.EqualFold(t[j], "NOT") {
				j++
			}
			if j < len(t) && strings.EqualFold(t[j], "EXISTS") {
				j++
			}
		}
		if j >= len(t) {
			continue
		}
		table := t[j]
		if table == "(" || table == "?" || table == "$" {
			continue
		}
		if j+2 < len(t) && t[j+1] == "." {
			table += "." + t[j+2]
		}
		table = strings.ToLower(table)
		if cte[table] {
			continue
		}
		result = append(result, sqlAccess{table: table, role: role})
	}
	return result
}
