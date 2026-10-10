package switcher

import (
	"bytes"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Collapse empty-line runs outside TOML values, preserving multiline strings.
func normalizeEmptyLines(b []byte) []byte {
	type span struct{ start, end int }
	protected := []span{}
	var parser unstable.Parser
	parser.Reset(b)
	for parser.NextExpression() {
		n := parser.Expression()
		if n.Kind == unstable.KeyValue {
			protected = append(protected, span{int(n.Raw.Offset), int(n.Raw.Offset + n.Raw.Length)})
		}
	}
	if parser.Error() != nil {
		return b
	}
	out := []byte{}
	offset, index, empty := 0, 0, false
	for _, line := range bytes.SplitAfter(b, []byte{'\n'}) {
		end := offset + len(line)
		for index < len(protected) && protected[index].end <= offset {
			index++
		}
		inside := index < len(protected) && protected[index].start < end && protected[index].end > offset
		blank := !inside && len(bytes.TrimSpace(line)) == 0
		if !blank || !empty {
			out = append(out, line...)
		}
		empty = blank
		offset = end
	}
	return out
}

// Remove an owned assignment's whole line and adjacent empty lines. Comments
// remain standalone; boundaries come from TOML expressions, never string values.
func ownedAssignmentSpan(b []byte, start, end int) (int, int) {
	line := bytes.LastIndexByte(b[:start], '\n') + 1
	if len(bytes.TrimSpace(b[line:start])) == 0 {
		start = line
	}
	for end < len(b) && (b[end] == ' ' || b[end] == '\t' || b[end] == '\r') {
		end++
	}
	if end < len(b) && b[end] == '\n' {
		end++
		for end < len(b) {
			next := bytes.IndexByte(b[end:], '\n')
			if next < 0 {
				break
			}
			next += end
			if len(bytes.TrimSpace(b[end:next])) != 0 {
				break
			}
			end = next + 1
		}
	}
	return start, end
}
