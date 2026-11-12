package main

import (
	"encoding/base64"
	"encoding/binary"
	"unicode/utf16"
)

// quoteExecStart escapes a path for use in a systemd unit file ExecStart= line.
//
// systemd unit file escaping rules (sd_unit_escape(3)):
//   - Backslash → double backslash
//   - Double-quote (") → \" (required when the path contains spaces)
//   - Percent (%) → %% (systemd expands %h, %u etc. in unit files)
//   - Dollar ($) → $$ (systemd variable-expansion in some contexts)
//
// The result is wrapped in double quotes so paths containing spaces are handled
// correctly by systemd's argument parser.
//
// References:
//   - systemd.service(5): ExecStart= argument quoting
//   - systemd(1): https://www.freedesktop.org/software/systemd/man/systemd.service.html
func quoteExecStart(path string) string {
	var out []byte
	out = append(out, '"')
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch c {
		case '\\':
			out = append(out, '\\', '\\')
		case '"':
			out = append(out, '\\', '"')
		case '%':
			out = append(out, '%', '%')
		case '$':
			out = append(out, '$', '$')
		default:
			out = append(out, c)
		}
	}
	out = append(out, '"')
	return string(out)
}

// encodeUTF16LE encodes s as UTF-16 little-endian bytes (no BOM).
// Used to build a PowerShell -EncodedCommand argument.
func encodeUTF16LE(s string) []byte {
	runes := []rune(s)
	u16 := utf16.Encode(runes)
	buf := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(buf[i*2:], v)
	}
	return buf
}

// encodedPSCommand returns the base64-encoded UTF-16LE encoding of psScript
// suitable for use with powershell.exe -EncodedCommand.
func encodedPSCommand(psScript string) string {
	return base64.StdEncoding.EncodeToString(encodeUTF16LE(psScript))
}
