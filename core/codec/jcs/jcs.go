// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package jcs canonicalises JSON per RFC 8785, the JSON Canonicalization
// Scheme, so a digest or signature over a JSON document computed in one
// language verifies in another.
//
// [Canonicalize] removes insignificant whitespace, sorts object members by
// the UTF-16 code units of their names, re-serialises strings with the
// minimal ECMAScript escaping and numbers with the ECMAScript
// Number-to-String algorithm (RFC 8785 section 3.2.2.3). Input must be
// I-JSON (RFC 7493): duplicate member names, invalid UTF-8, lone surrogate
// escapes and numbers outside the IEEE 754 double range are rejected with
// a named error rather than repaired.
//
// The parser is iterative (no recursion) and bounded by [MaxDepth]; every
// byte is visited a constant number of times, and the output is written in
// one pass.
package jcs

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"unicode/utf16"
)

// MaxDepth bounds array and object nesting; it matches encoding/json.
const MaxDepth = 10000

// Errors returned by [Canonicalize]; each is wrapped with the byte offset.
var (
	ErrSyntax        = errors.New("jcs: invalid JSON")
	ErrInvalidUTF8   = errors.New("jcs: invalid UTF-8")
	ErrLoneSurrogate = errors.New("jcs: lone surrogate")
	ErrDuplicateKey  = errors.New("jcs: duplicate object member name")
	ErrNumberRange   = errors.New("jcs: number outside IEEE 754 double range")
	ErrTooDeep       = errors.New("jcs: nesting exceeds MaxDepth")
)

type kind uint8

const (
	scalar kind = iota
	object
	array
)

// node is one parsed value; containers refer to children by index.
type node struct {
	kind    kind
	raw     []byte   // canonical bytes of a scalar
	members []member // object
	elems   []int    // array
}

type member struct {
	name  string
	units []uint16 // sort key: UTF-16 code units
	enc   []byte   // canonical name, quoted
	child int
}

// Canonicalize returns the RFC 8785 canonical form of the JSON text data.
func Canonicalize(data []byte) ([]byte, error) {
	p := parser{data: data}
	root, err := p.parse()
	if err != nil {
		return nil, err
	}
	return p.emit(root, len(data)), nil
}

type frameState uint8

const (
	stOpen  frameState = iota // after '[' or '{': item or close
	stItem                    // after ',': item required
	stValue                   // object: after name and ':'
	stSep                     // after an item: ',' or close
)

type frame struct {
	node  int
	state frameState
	name  []byte // pending member name (decoded UTF-8)
}

type parser struct {
	data  []byte
	pos   int
	nodes []node
	stack []frame
	root  int
	done  bool
}

func (p *parser) fail(sentinel error) error {
	return fmt.Errorf("%w at offset %d", sentinel, p.pos)
}

// parse runs one token per step; every step consumes at least one byte, so
// len(data)+1 steps always suffice (HISS-02).
func (p *parser) parse() (int, error) {
	for steps := 0; steps <= len(p.data) && !p.done; steps++ {
		if err := p.step(); err != nil {
			return 0, err
		}
	}
	if !p.done {
		return 0, p.fail(ErrSyntax)
	}
	p.skipSpace()
	if p.pos != len(p.data) {
		return 0, p.fail(ErrSyntax)
	}
	return p.root, nil
}

func (p *parser) step() error {
	p.skipSpace()
	if len(p.stack) == 0 {
		return p.value()
	}
	f := &p.stack[len(p.stack)-1]
	switch f.state {
	case stOpen, stItem:
		if f.state == stOpen && p.peek() == p.closer(f) {
			p.pos++
			return p.closeFrame()
		}
		if p.nodes[f.node].kind == object {
			return p.memberName(f)
		}
		return p.value()
	case stValue:
		return p.value()
	case stSep:
		return p.separator(f)
	}
	return p.fail(ErrSyntax)
}

func (p *parser) separator(f *frame) error {
	c := p.peek()
	p.pos++
	switch c {
	case ',':
		f.state = stItem
		return nil
	case p.closer(f):
		return p.closeFrame()
	}
	p.pos--
	return p.fail(ErrSyntax)
}

func (p *parser) closer(f *frame) byte { return closer(p.nodes[f.node].kind) }

func (p *parser) memberName(f *frame) error {
	if p.peek() != '"' {
		return p.fail(ErrSyntax)
	}
	name, err := p.str()
	if err != nil {
		return err
	}
	p.skipSpace()
	if p.peek() != ':' {
		return p.fail(ErrSyntax)
	}
	p.pos++
	f.name, f.state = name, stValue
	return nil
}

func (p *parser) value() error {
	switch c := p.peek(); {
	case c == '{':
		return p.open(object)
	case c == '[':
		return p.open(array)
	case c == '"':
		s, err := p.str()
		if err != nil {
			return err
		}
		return p.attach(appendString(make([]byte, 0, len(s)+2), s))
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	return p.literal()
}

func (p *parser) open(k kind) error {
	if len(p.stack) >= MaxDepth {
		return p.fail(ErrTooDeep)
	}
	p.pos++
	p.nodes = append(p.nodes, node{kind: k})
	p.stack = append(p.stack, frame{node: len(p.nodes) - 1, state: stOpen})
	return nil
}

func (p *parser) closeFrame() error {
	f := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	n := &p.nodes[f.node]
	if n.kind == object {
		slices.SortFunc(n.members, func(a, b member) int { return slices.Compare(a.units, b.units) })
		for i := 1; i < len(n.members); i++ {
			if slices.Equal(n.members[i-1].units, n.members[i].units) {
				return fmt.Errorf("%w %q in object ending at offset %d", ErrDuplicateKey, n.members[i].name, p.pos)
			}
		}
	}
	return p.link(f.node)
}

// attach stores a scalar and links it into the enclosing container.
func (p *parser) attach(raw []byte) error {
	p.nodes = append(p.nodes, node{kind: scalar, raw: raw})
	return p.link(len(p.nodes) - 1)
}

func (p *parser) link(child int) error {
	if len(p.stack) == 0 {
		p.root, p.done = child, true
		return nil
	}
	f := &p.stack[len(p.stack)-1]
	parent := &p.nodes[f.node]
	if parent.kind == object {
		name := string(f.name)
		parent.members = append(parent.members, member{
			name:  name,
			units: utf16.Encode([]rune(name)),
			enc:   appendString(make([]byte, 0, len(f.name)+2), f.name),
			child: child,
		})
		f.name = nil
	} else {
		parent.elems = append(parent.elems, child)
	}
	f.state = stSep
	return nil
}

func (p *parser) literal() error {
	for _, lit := range [...]string{"true", "false", "null"} {
		if len(p.data)-p.pos >= len(lit) && string(p.data[p.pos:p.pos+len(lit)]) == lit {
			p.pos += len(lit)
			return p.attach([]byte(lit))
		}
	}
	return p.fail(ErrSyntax)
}

func (p *parser) number() error {
	start := p.pos
	if !p.scanNumber() {
		return p.fail(ErrSyntax)
	}
	f, err := strconv.ParseFloat(string(p.data[start:p.pos]), 64)
	if err != nil || math.IsInf(f, 0) {
		p.pos = start
		return p.fail(ErrNumberRange)
	}
	return p.attach(appendNumber(nil, f))
}

// scanNumber advances over -?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)? and reports
// whether the text matched the RFC 8259 number grammar.
func (p *parser) scanNumber() bool {
	if p.peek() == '-' {
		p.pos++
	}
	switch c := p.peek(); {
	case c == '0':
		p.pos++
	case c >= '1' && c <= '9':
		p.digits()
	default:
		return false
	}
	return p.scanFraction() && p.scanExponent()
}

func (p *parser) scanFraction() bool {
	if p.peek() != '.' {
		return true
	}
	p.pos++
	return p.digits() > 0
}

func (p *parser) scanExponent() bool {
	if c := p.peek(); c != 'e' && c != 'E' {
		return true
	}
	p.pos++
	if c := p.peek(); c == '+' || c == '-' {
		p.pos++
	}
	return p.digits() > 0
}

func (p *parser) digits() int {
	start := p.pos
	for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
		p.pos++
	}
	return p.pos - start
}

func (p *parser) peek() byte {
	if p.pos < len(p.data) {
		return p.data[p.pos]
	}
	return 0
}

func (p *parser) skipSpace() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// appendNumber formats f with the ECMAScript Number-to-String algorithm:
// shortest round-trip digits, fixed notation for 1e-6 <= |f| < 1e21,
// exponent without leading zeros otherwise, and -0 as 0.
func appendNumber(dst []byte, f float64) []byte {
	if f == 0 {
		return append(dst, '0')
	}
	format := byte('f')
	if abs := math.Abs(f); abs < 1e-6 || abs >= 1e21 {
		format = 'e'
	}
	dst = strconv.AppendFloat(dst, f, format, -1, 64)
	// Go writes e-07; ECMAScript writes e-7. Large exponents are >= 21.
	if n := len(dst); format == 'e' && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
		dst[n-2] = dst[n-1]
		dst = dst[:n-1]
	}
	return dst
}
