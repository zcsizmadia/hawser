package wsl

import (
	"bytes"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// decodeOutput converts wsl.exe output to a Go string.
//
// wsl.exe writes its *own* messages as UTF-16LE — distro lists, --status,
// "The operation completed successfully" — while the output of a command run
// inside a distro comes back as raw UTF-8 bytes. Reading the former as UTF-8
// yields the familiar "H e l l o" with a NUL between every character, and
// naively stripping NULs corrupts any genuine binary or multi-byte content.
//
// So: honor a BOM when present, otherwise detect UTF-16 by structure rather
// than by guessing, and fall through to UTF-8 unchanged.
func decodeOutput(b []byte) string {
	if len(b) == 0 {
		return ""
	}

	// Explicit BOMs are unambiguous.
	switch {
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return decodeUTF16(b[2:], false)
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return decodeUTF16(b[2:], true)
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return string(b[3:])
	}

	if looksUTF16LE(b) {
		return decodeUTF16(b, false)
	}
	return string(b)
}

// looksUTF16LE reports whether the bytes are structurally UTF-16LE text: an
// even length, and ASCII-range characters each followed by a zero high byte.
//
// The check is conservative on purpose. Valid UTF-8 that merely contains a NUL
// must not be misread as UTF-16, so this requires the alternating pattern to
// hold across the sample rather than merely finding NULs anywhere.
func looksUTF16LE(b []byte) bool {
	if len(b) < 4 || len(b)%2 != 0 {
		return false
	}
	// A NUL is not legal in UTF-8 text output; without one, this is not UTF-16.
	if !bytes.Contains(b, []byte{0}) {
		return false
	}

	sample := b
	if len(sample) > 512 {
		sample = sample[:512]
		if len(sample)%2 != 0 {
			sample = sample[:len(sample)-1]
		}
	}

	var pairs, zeroHigh int
	for i := 0; i+1 < len(sample); i += 2 {
		pairs++
		if sample[i+1] == 0 {
			zeroHigh++
		}
	}
	if pairs == 0 {
		return false
	}
	// wsl.exe output is overwhelmingly ASCII, so nearly every high byte is
	// zero. Requiring a large majority avoids false positives on UTF-8 that
	// happens to contain an embedded NUL.
	return zeroHigh*10 >= pairs*8
}

func decodeUTF16(b []byte, bigEndian bool) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1] // drop a stray trailing byte rather than fail
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if bigEndian {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			u = append(u, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	s := string(utf16.Decode(u))
	if !utf8.ValidString(s) {
		return string(b)
	}
	return s
}

// cleanLines splits decoded output into trimmed, non-empty lines. wsl.exe pads
// its table output and mixes line endings, so callers should not parse raw.
func cleanLines(s string) []string {
	raw := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, t)
		}
	}
	return out
}
