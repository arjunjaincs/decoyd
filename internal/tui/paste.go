package tui

import "strings"

// pasteIntoBuffer inserts text at cursor position pos in buf, then returns the
// updated buffer and the new cursor position.
//
// Control characters that have no meaning in a single-line text field (CR, LF,
// tab) are stripped so that pasting a multi-line value (e.g. from a notes app)
// does not corrupt the field. CRLF pairs are collapsed before stripping so that
// Windows-style line endings don't leave stray CR characters behind.
func pasteIntoBuffer(buf []rune, pos int, text string) ([]rune, int) {
	// Collapse Windows-style CRLF first, then strip bare CR / LF / tab.
	text = strings.ReplaceAll(text, "\r\n", "")
	ins := make([]rune, 0, len(text))
	for _, r := range text {
		if r != '\r' && r != '\n' && r != '\t' {
			ins = append(ins, r)
		}
	}
	if len(ins) == 0 {
		return buf, pos
	}
	nb := make([]rune, 0, len(buf)+len(ins))
	nb = append(nb, buf[:pos]...)
	nb = append(nb, ins...)
	nb = append(nb, buf[pos:]...)
	return nb, pos + len(ins)
}
