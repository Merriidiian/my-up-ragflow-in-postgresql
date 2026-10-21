package dbcompat

import "strings"

// NormalizeSQL converts legacy MySQL identifier quotes without touching strings,
// comments, bound values or PostgreSQL dollar-quoted bodies. The bare table name
// user is quoted because USER is a PostgreSQL keyword.
func NormalizeSQL(query string) string {
	var out strings.Builder
	for i := 0; i < len(query); {
		switch {
		case query[i] == '\'' || query[i] == '"':
			quote := query[i]
			start := i
			i++
			for i < len(query) {
				if query[i] == quote {
					i++
					if i < len(query) && query[i] == quote {
						i++
						continue
					}
					break
				}
				if query[i] == '\\' && quote == '\'' && start > 0 && (query[start-1] == 'E' || query[start-1] == 'e') && i+1 < len(query) {
					i += 2
				} else {
					i++
				}
			}
			out.WriteString(query[start:i])
		case strings.HasPrefix(query[i:], "--"):
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				out.WriteString(query[i:])
				return out.String()
			}
			out.WriteString(query[i : i+end+1])
			i += end + 1
		case strings.HasPrefix(query[i:], "/*"):
			start := i
			depth := 1
			i += 2
			for i < len(query) && depth > 0 {
				if strings.HasPrefix(query[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(query[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
			out.WriteString(query[start:i])
		case query[i] == '`':
			out.WriteByte('"')
			i++
			for i < len(query) {
				if query[i] == '`' {
					if i+1 < len(query) && query[i+1] == '`' {
						out.WriteByte('`')
						i += 2
						continue
					}
					i++
					break
				}
				if query[i] == '"' {
					out.WriteByte('"')
				}
				out.WriteByte(query[i])
				i++
			}
			out.WriteByte('"')
		case query[i] == '$':
			end := i + 1
			for end < len(query) && (letter(query[end]) || query[end] >= '0' && query[end] <= '9') {
				end++
			}
			if end < len(query) && query[end] == '$' && (end == i+1 || letter(query[i+1])) {
				delimiter := query[i : end+1]
				close := strings.Index(query[end+1:], delimiter)
				if close >= 0 {
					stop := end + 1 + close + len(delimiter)
					out.WriteString(query[i:stop])
					i = stop
					continue
				}
			}
			out.WriteByte(query[i])
			i++
		case letter(query[i]):
			start := i
			i++
			for i < len(query) && (letter(query[i]) || query[i] >= '0' && query[i] <= '9' || query[i] == '$') {
				i++
			}
			word := query[start:i]
			if strings.EqualFold(word, "user") {
				out.WriteString(`"user"`)
			} else {
				out.WriteString(word)
			}
		default:
			out.WriteByte(query[i])
			i++
		}
	}
	return out.String()
}

func letter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c >= 128 }
