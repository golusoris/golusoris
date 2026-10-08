// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package junit_test

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/junit"
)

// schema is the Jenkins xUnit plugin's junit-10.xsd, pinned at
// jenkinsci/xunit-plugin@0afa700702a6617f483cfd9407711976dc0bd74a (MIT).
const schema = "testdata/junit-10.xsd"

// golden is the expected Write output for gateReport; regenerate with
// GOLUSORIS_UPDATE_GOLDEN=1 go test ./junit/.
const golden = "testdata/gate.golden.xml"

// gateReport is the #631 acceptance fixture: one pass, one failure, one
// skip, plus properties and a message carrying an ANSI colour escape.
func gateReport() junit.Report {
	return junit.Report{
		Name: "vmafx",
		Suites: []junit.Suite{{
			Name:       "score-gate",
			Timestamp:  time.Date(2026, 10, 7, 12, 30, 0, 0, time.FixedZone("CEST", 2*60*60)),
			Hostname:   "runner-1",
			Properties: []junit.Property{{Name: "threshold", Value: "93.0"}, {Name: "model", Value: "vmaf_v0.6.1"}},
			Cases: []junit.Case{
				{Name: "clip-01", Classname: "vmaf", Duration: 1500 * time.Millisecond},
				{
					Name: "clip-02", Classname: "vmaf", Duration: 2250 * time.Millisecond, Status: junit.Failed,
					Message: "VMAF 91.2 < 93.0 \x1b[31m(red)\x1b[0m", Type: "threshold",
					Details: "frames: 240\nworst frame: 117 <score=71.4> & \"quoted\"",
				},
				{Name: "clip-03", Classname: "vmaf", Status: junit.Skipped, Message: "reference missing"},
			},
			SystemOut: "scored 2 of 3 clips",
		}},
	}
}

func write(t *testing.T, r junit.Report) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := junit.Write(&buf, r); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}

func TestWriteGolden(t *testing.T) {
	t.Parallel()
	got := write(t, gateReport())
	if os.Getenv("GOLUSORIS_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("output differs from %s (GOLUSORIS_UPDATE_GOLDEN=1 regenerates):\n%s", golden, got)
	}
}

func TestWriteCountsAndTimes(t *testing.T) {
	t.Parallel()
	doc := decode(t, write(t, gateReport()))
	if doc.Tests != 3 || doc.Failures != 1 || doc.Errors != 0 || doc.Time != "3.750" {
		t.Errorf("testsuites = %+v", doc)
	}
	s := doc.Suites[0]
	if s.Tests != 3 || s.Failures != 1 || s.Skipped != 1 || s.Errors != 0 || s.Time != "3.750" {
		t.Errorf("testsuite counts = %+v", s)
	}
	if s.Timestamp != "2026-10-07T10:30:00" {
		t.Errorf("timestamp = %q, want UTC without zone", s.Timestamp)
	}
	if s.Cases[0].Time != "1.500" || s.Cases[2].Time != "0.000" {
		t.Errorf("case times = %q, %q", s.Cases[0].Time, s.Cases[2].Time)
	}
}

// TestWriteRoundTrip decodes the output again: escaping must be lossless for
// legal text and visible for what XML cannot carry.
func TestWriteRoundTrip(t *testing.T) {
	t.Parallel()
	fail := decode(t, write(t, gateReport())).Suites[0].Cases[1].Failure
	if fail == nil {
		t.Fatal("failure element missing")
	}
	if want := `VMAF 91.2 < 93.0 \x1b[31m(red)\x1b[0m`; fail.Message != want {
		t.Errorf("message = %q, want %q", fail.Message, want)
	}
	if want := "frames: 240\nworst frame: 117 <score=71.4> & \"quoted\""; fail.Body != want {
		t.Errorf("details = %q, want %q", fail.Body, want)
	}
}

func TestWriteIllegalCharacters(t *testing.T) {
	t.Parallel()
	nasty := "nul\x00 bel\x07 esc\x1b nonchar\uFFFE bad-utf8\xff\xfe tab\t ok\u00e9\U0001F600"
	r := junit.Report{Suites: []junit.Suite{{
		Name:  nasty,
		Cases: []junit.Case{{Name: nasty, Status: junit.Errored, Message: nasty, Type: nasty, Details: nasty, SystemErr: nasty}},
	}}}
	out := write(t, r)
	doc := decode(t, out)
	want := `nul\x00 bel\x07 esc\x1b nonchar\ufffe bad-utf8\xff\xfe tab` + "\t ok\u00e9\U0001F600"
	if got := doc.Suites[0].Cases[0].Error.Body; got != want {
		t.Errorf("details = %q, want %q", got, want)
	}
	if got := doc.Suites[0].Name; got != want {
		t.Errorf("suite name = %q, want %q", got, want)
	}
}

// TestDecoderRejectsRawControls proves decode is a real well-formedness
// check: the same bytes unescaped do not parse.
func TestDecoderRejectsRawControls(t *testing.T) {
	t.Parallel()
	raw := []byte("<testsuites><testsuite name=\"a\x1b\"></testsuite></testsuites>")
	if err := xml.Unmarshal(raw, &wireSuites{}); err == nil {
		t.Fatal("encoding/xml accepted a raw ESC; the round-trip tests would prove nothing")
	}
}

func TestWriteBoundaries(t *testing.T) {
	t.Parallel()
	empty := decode(t, write(t, junit.Report{}))
	if empty.Tests != 0 || len(empty.Suites) != 0 || empty.Time != "0.000" {
		t.Errorf("empty report = %+v", empty)
	}
	r := junit.Report{Suites: []junit.Suite{{
		Name:     "s",
		Duration: 9 * time.Second,
		Cases:    []junit.Case{{Name: "a", Duration: 1499 * time.Microsecond}, {Name: "b", Duration: 1500 * time.Microsecond}},
	}}}
	s := decode(t, write(t, r)).Suites[0]
	if s.Time != "9.000" {
		t.Errorf("explicit suite duration ignored: %q", s.Time)
	}
	if s.Cases[0].Time != "0.001" || s.Cases[1].Time != "0.002" {
		t.Errorf("millisecond rounding: %q, %q", s.Cases[0].Time, s.Cases[1].Time)
	}
	if s.Properties != nil {
		t.Error("empty properties element written")
	}
}

func TestWriteRejectsInvalid(t *testing.T) {
	t.Parallel()
	cases := map[string]junit.Report{
		"suite without name":    {Suites: []junit.Suite{{}}},
		"case without name":     {Suites: []junit.Suite{{Name: "s", Cases: []junit.Case{{}}}}},
		"negative case time":    {Suites: []junit.Suite{{Name: "s", Cases: []junit.Case{{Name: "c", Duration: -time.Second}}}}},
		"negative suite time":   {Suites: []junit.Suite{{Name: "s", Duration: -1}}},
		"unknown status":        {Suites: []junit.Suite{{Name: "s", Cases: []junit.Case{{Name: "c", Status: 9}}}}},
		"property without name": {Suites: []junit.Suite{{Name: "s", Properties: []junit.Property{{Value: "v"}}}}},
	}
	for name, r := range cases {
		var buf bytes.Buffer
		if err := junit.Write(&buf, r); !errors.Is(err, junit.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
		if buf.Len() != 0 {
			t.Errorf("%s: wrote %d bytes before failing", name, buf.Len())
		}
	}
}

func TestWritePropagatesWriterError(t *testing.T) {
	t.Parallel()
	if err := junit.Write(failingWriter{}, gateReport()); err == nil || errors.Is(err, junit.ErrInvalid) {
		t.Fatalf("err = %v, want the writer error", err)
	}
}

func TestStatusString(t *testing.T) {
	t.Parallel()
	for st, want := range map[junit.Status]string{
		junit.Passed: "passed", junit.Failed: "failed", junit.Errored: "errored", junit.Skipped: "skipped", 7: "status(7)",
	} {
		if got := st.String(); got != want {
			t.Errorf("Status(%d).String() = %q, want %q", st, got, want)
		}
	}
}

// TestSchemaValidation is the #631 acceptance: the golden document and a
// document full of illegal characters validate against junit-10.xsd. A
// planted invalid document proves xmllint enforces the schema.
func TestSchemaValidation(t *testing.T) {
	t.Parallel()
	xmllint, err := exec.LookPath("xmllint")
	if testing.Short() || err != nil {
		t.Skip("needs xmllint (libxml2) on PATH and no -short")
	}
	dir := t.TempDir()
	planted := filepath.Join(dir, "planted.xml")
	if err := os.WriteFile(planted, []byte(`<testsuites skipped="1"><testsuite tests="0" failures="0" errors="0"/></testsuites>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg, ok := validate(t, xmllint, planted); ok {
		t.Fatalf("xmllint accepted a document without testsuite@name:\n%s", msg)
	}
	nasty := junit.Report{Suites: []junit.Suite{{Name: "\x01", Cases: []junit.Case{
		{Name: "\x02", Status: junit.Errored, Message: "\x03", Details: "\xff"},
		{Name: "skip", Status: junit.Skipped},
	}}}}
	for name, data := range map[string][]byte{"golden.xml": write(t, gateReport()), "nasty.xml": write(t, nasty)} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if msg, ok := validate(t, xmllint, path); !ok {
			t.Errorf("%s does not validate:\n%s", name, msg)
		}
	}
}

func validate(t *testing.T, xmllint, path string) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, xmllint, "--noout", "--nonet", "--schema", schema, path).CombinedOutput()
	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			t.Fatalf("xmllint: %v", err)
		}
		return string(out), false
	}
	return string(out), !strings.Contains(string(out), "fails to validate")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// wire* decode the output independently of the package's own types.
type wireSuites struct {
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Time     string      `xml:"time,attr"`
	Suites   []wireSuite `xml:"testsuite"`
}

type wireSuite struct {
	Name       string     `xml:"name,attr"`
	Tests      int        `xml:"tests,attr"`
	Failures   int        `xml:"failures,attr"`
	Errors     int        `xml:"errors,attr"`
	Skipped    int        `xml:"skipped,attr"`
	Time       string     `xml:"time,attr"`
	Timestamp  string     `xml:"timestamp,attr"`
	Properties *struct{}  `xml:"properties"`
	Cases      []wireCase `xml:"testcase"`
}

type wireCase struct {
	Time    string      `xml:"time,attr"`
	Failure *wireResult `xml:"failure"`
	Error   *wireResult `xml:"error"`
}

type wireResult struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"` //nolint:tagliatelle // chardata has no element name
}

func decode(t *testing.T, data []byte) wireSuites {
	t.Helper()
	var doc wireSuites
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("output is not well-formed XML: %v\n%s", err, data)
	}
	return doc
}
