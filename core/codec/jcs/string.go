// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jcs

import (
	"unicode/utf16"
	"unicode/utf8"
)

// str decodes the string literal at p.pos (which holds '"') to UTF-8,
// refusing invalid UTF-8, raw control characters and lone surrogates.
func (p *parser) str() ([]byte, error) {
	p.pos++
	out := make([]byte, 0, 16)
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		switch {
		case c == '"':
			p.pos++
			return out, nil
		case c == '\\':
			var err error
			if out, err = p.escape(out); err != nil {
				return nil, err
			}
		case c < 0x20:
			return nil, p.fail(ErrSyntax)
		case c < utf8.RuneSelf:
			out = append(out, c)
			p.pos++
		default:
			r, size := utf8.DecodeRune(p.data[p.pos:])
			if r == utf8.RuneError && size == 1 {
				return nil, p.fail(ErrInvalidUTF8)
			}
			out = append(out, p.data[p.pos:p.pos+size]...)
			p.pos += size
		}
	}
	return nil, p.fail(ErrSyntax)
}

// escape decodes the escape sequence at p.pos (which holds '\').
func (p *parser) escape(out []byte) ([]byte, error) {
	if p.pos+1 >= len(p.data) {
		return nil, p.fail(ErrSyntax)
	}
	c := p.data[p.pos+1]
	if c != 'u' {
		b, ok := shortEscape(c)
		if !ok {
			return nil, p.fail(ErrSyntax)
		}
		p.pos += 2
		return append(out, b), nil
	}
	r, err := p.unicodeEscape()
	if err != nil {
		return nil, err
	}
	return utf8.AppendRune(out, r), nil
}

func shortEscape(c byte) (byte, bool) {
	switch c {
	case '"', '\\', '/':
		return c, true
	case 'b':
		return '\b', true
	case 'f':
		return '\f', true
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	}
	return 0, false
}

// unicodeEscape decodes \uXXXX, joining a high and low surrogate pair.
func (p *parser) unicodeEscape() (rune, error) {
	u, ok := p.hex4(p.pos + 2)
	if !ok {
		return 0, p.fail(ErrSyntax)
	}
	switch {
	case u >= 0xDC00 && u <= 0xDFFF:
		return 0, p.fail(ErrLoneSurrogate)
	case u < 0xD800 || u > 0xDBFF:
		p.pos += 6
		return u, nil
	}
	low, ok := p.lowSurrogate(p.pos + 6)
	if !ok {
		return 0, p.fail(ErrLoneSurrogate)
	}
	p.pos += 12
	return utf16.DecodeRune(u, low), nil
}

// lowSurrogate parses a \uDC00..\uDFFF escape at data[at:].
func (p *parser) lowSurrogate(at int) (rune, bool) {
	if at+2 > len(p.data) || p.data[at] != '\\' || p.data[at+1] != 'u' {
		return 0, false
	}
	low, ok := p.hex4(at + 2)
	return low, ok && low >= 0xDC00 && low <= 0xDFFF
}

// hex4 parses four hex digits at data[at:].
func (p *parser) hex4(at int) (rune, bool) {
	if at < 0 || at+4 > len(p.data) {
		return 0, false
	}
	var r rune
	for _, c := range p.data[at : at+4] {
		var v byte
		switch {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			return 0, false
		}
		r = r<<4 | rune(v)
	}
	return r, true
}

// appendString writes s quoted with ECMAScript JSON.stringify escaping
// (RFC 8785 section 3.2.2.2): only '"', '\' and U+0000..U+001F are escaped.
func appendString(dst []byte, s []byte) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	for _, c := range s {
		switch {
		case c == '"' || c == '\\':
			dst = append(dst, '\\', c)
		case c >= 0x20:
			dst = append(dst, c)
		case c == '\b':
			dst = append(dst, '\\', 'b')
		case c == '\t':
			dst = append(dst, '\\', 't')
		case c == '\n':
			dst = append(dst, '\\', 'n')
		case c == '\f':
			dst = append(dst, '\\', 'f')
		case c == '\r':
			dst = append(dst, '\\', 'r')
		default:
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
		}
	}
	return append(dst, '"')
}
