// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package junit writes JUnit XML reports so CI systems show a gate's failing
// case by name.
//
// The output is the subset of the format that Jenkins, GitLab, GitHub and
// Surefire readers share, and validates against the Jenkins xUnit
// junit-10.xsd schema: suites with computed counts, cases that pass, fail,
// error or skip, messages, durations in seconds with millisecond precision,
// and properties. Characters that XML 1.0 forbids (most C0 controls, U+FFFE,
// U+FFFF, invalid UTF-8) are replaced by visible \xNN or \uNNNN escapes, so
// a message carrying terminal colour codes still yields a valid document.
//
//	r := junit.Report{Suites: []junit.Suite{{
//	    Name: "vmaf-gate",
//	    Cases: []junit.Case{{
//	        Name: "clip-01", Classname: "vmaf", Duration: d,
//	        Status: junit.Failed, Message: "VMAF 91.2 < 93.0", Type: "threshold",
//	    }},
//	}}}
//	err := junit.Write(f, r)
package junit

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Status is a test case outcome. The zero value is [Passed].
type Status uint8

// Test case outcomes.
const (
	Passed Status = iota
	Failed
	Errored
	Skipped
)

// String returns the lowercase outcome name.
func (s Status) String() string {
	switch s {
	case Passed:
		return "passed"
	case Failed:
		return "failed"
	case Errored:
		return "errored"
	case Skipped:
		return "skipped"
	}
	return "status(" + strconv.Itoa(int(s)) + ")"
}

// ErrInvalid is returned by [Write] for a report that cannot be serialised
// faithfully: a missing name, a negative duration or an unknown [Status].
var ErrInvalid = errors.New("junit: invalid report")

// Report is a <testsuites> document.
type Report struct {
	// Name is optional.
	Name   string
	Suites []Suite
}

// Suite is one <testsuite>. Counts are derived from Cases.
type Suite struct {
	// Name is required.
	Name string
	// Timestamp is written in UTC as 2006-01-02T15:04:05; zero omits it.
	Timestamp time.Time
	// Hostname is optional.
	Hostname string
	// Duration is the suite wall time; zero means the sum of case durations.
	Duration   time.Duration
	Properties []Property
	Cases      []Case
	SystemOut  string
	SystemErr  string
}

// Property is a name/value pair attached to a suite.
type Property struct {
	// Name is required.
	Name  string
	Value string
}

// Case is one <testcase>.
type Case struct {
	// Name is required.
	Name      string
	Classname string
	Duration  time.Duration
	Status    Status
	// Message and Type become the attributes of the <failure>, <error> or
	// <skipped> element; Type is ignored for [Skipped].
	Message string
	Type    string
	// Details is the element body, e.g. measured values or a stack trace.
	Details   string
	SystemOut string
	SystemErr string
}

// Write serialises r as an indented, UTF-8 JUnit XML document to w.
func Write(w io.Writer, r Report) error {
	doc, err := build(r)
	if err != nil {
		return err
	}
	if _, err = io.WriteString(w, xml.Header); err != nil {
		return fmt.Errorf("junit: write: %w", err)
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err = enc.Encode(doc); err != nil {
		return fmt.Errorf("junit: write: %w", err)
	}
	if err = enc.Close(); err != nil {
		return fmt.Errorf("junit: write: %w", err)
	}
	if _, err = io.WriteString(w, "\n"); err != nil {
		return fmt.Errorf("junit: write: %w", err)
	}
	return nil
}

func build(r Report) (xmlSuites, error) {
	doc := xmlSuites{Name: clean(r.Name), Suites: make([]xmlSuite, 0, len(r.Suites))}
	var total time.Duration
	for i := range r.Suites {
		s, d, err := buildSuite(&r.Suites[i])
		if err != nil {
			return xmlSuites{}, fmt.Errorf("%w: suite %d: %w", ErrInvalid, i, err)
		}
		doc.Suites = append(doc.Suites, s)
		doc.Tests += s.Tests
		doc.Failures += s.Failures
		doc.Errors += s.Errors
		total += d
	}
	doc.Time = seconds(total)
	return doc, nil
}

func buildSuite(s *Suite) (xmlSuite, time.Duration, error) {
	if s.Name == "" {
		return xmlSuite{}, 0, errors.New("name is empty")
	}
	if s.Duration < 0 {
		return xmlSuite{}, 0, fmt.Errorf("%q: negative duration", s.Name)
	}
	out := xmlSuite{
		Name: clean(s.Name), Hostname: clean(s.Hostname), Tests: len(s.Cases),
		SystemOut: clean(s.SystemOut), SystemErr: clean(s.SystemErr),
	}
	if !s.Timestamp.IsZero() {
		out.Timestamp = s.Timestamp.UTC().Format("2006-01-02T15:04:05")
	}
	props, err := buildProperties(s.Properties)
	if err != nil {
		return xmlSuite{}, 0, fmt.Errorf("%q: %w", s.Name, err)
	}
	out.Properties = props
	var sum time.Duration
	for i := range s.Cases {
		c, cerr := buildCase(&s.Cases[i])
		if cerr != nil {
			return xmlSuite{}, 0, fmt.Errorf("%q: case %d: %w", s.Name, i, cerr)
		}
		out.Cases = append(out.Cases, c)
		sum += s.Cases[i].Duration
		out.count(s.Cases[i].Status)
	}
	if s.Duration > 0 {
		sum = s.Duration
	}
	out.Time = seconds(sum)
	return out, sum, nil
}

func (s *xmlSuite) count(st Status) {
	switch st {
	case Failed:
		s.Failures++
	case Errored:
		s.Errors++
	case Skipped:
		s.Skipped++
	case Passed:
	}
}

func buildProperties(props []Property) (*xmlProperties, error) {
	if len(props) == 0 {
		return nil, nil //nolint:nilnil // nil omits the empty <properties> element
	}
	out := &xmlProperties{Items: make([]xmlProperty, 0, len(props))}
	for i, p := range props {
		if p.Name == "" {
			return nil, fmt.Errorf("property %d: name is empty", i)
		}
		out.Items = append(out.Items, xmlProperty{Name: clean(p.Name), Value: clean(p.Value)})
	}
	return out, nil
}

func buildCase(c *Case) (xmlCase, error) {
	if c.Name == "" {
		return xmlCase{}, errors.New("name is empty")
	}
	if c.Duration < 0 {
		return xmlCase{}, fmt.Errorf("%q: negative duration", c.Name)
	}
	out := xmlCase{
		Name: clean(c.Name), Classname: clean(c.Classname), Time: seconds(c.Duration),
		SystemOut: clean(c.SystemOut), SystemErr: clean(c.SystemErr),
	}
	result := &xmlResult{Message: clean(c.Message), Type: clean(c.Type), Body: clean(c.Details)}
	switch c.Status {
	case Passed:
	case Failed:
		out.Failure = result
	case Errored:
		out.Error = result
	case Skipped:
		result.Type = ""
		out.Skipped = result
	default:
		return xmlCase{}, fmt.Errorf("%q: unknown %s", c.Name, c.Status)
	}
	return out, nil
}

// seconds renders d as seconds with millisecond precision, matching the
// schema's SUREFIRE_TIME pattern; integer math keeps it exact.
func seconds(d time.Duration) string {
	ms := d.Round(time.Millisecond).Milliseconds()
	return strconv.FormatInt(ms/1000, 10) + "." + fmt.Sprintf("%03d", ms%1000)
}

// clean replaces what XML 1.0 cannot carry with a visible escape: C0
// controls other than tab, LF and CR, U+FFFE, U+FFFF, and invalid UTF-8.
func clean(s string) string {
	if xmlSafe(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case !xmlChar(r):
			if r < 0x100 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

func xmlSafe(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !xmlChar(r) {
			return false
		}
	}
	return true
}

// xmlChar reports whether r is in the XML 1.0 Char production (section 2.2).
func xmlChar(r rune) bool {
	switch {
	case r == '\t' || r == '\n' || r == '\r':
		return true
	case r < 0x20:
		return false
	case r >= 0xD800 && r <= 0xDFFF:
		return false
	case r == 0xFFFE || r == 0xFFFF:
		return false
	}
	return r <= utf8.MaxRune
}
