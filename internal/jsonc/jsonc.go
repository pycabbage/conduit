package jsonc

// ToJSON removes // and /* */ comments from JSONC, preserving string literals.
func ToJSON(src []byte) []byte {
	out := make([]byte, 0, len(src))
	i := 0
	for i < len(src) {
		// String literal: copy verbatim including escape sequences.
		if src[i] == '"' {
			out = append(out, src[i])
			i++
			for i < len(src) {
				c := src[i]
				out = append(out, c)
				i++
				if c == '\\' && i < len(src) {
					out = append(out, src[i])
					i++
				} else if c == '"' {
					break
				}
			}
			continue
		}
		// Line comment: skip to end of line.
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		// Block comment: skip to */.
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			i += 2
			for i+1 < len(src) && (src[i] != '*' || src[i+1] != '/') {
				i++
			}
			i += 2
			continue
		}
		out = append(out, src[i])
		i++
	}
	return out
}
