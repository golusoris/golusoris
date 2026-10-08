// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package junit

import "encoding/xml"

// The wire types mirror junit-10.xsd; element and attribute names are fixed
// by that schema.

type xmlSuites struct {
	XMLName  xml.Name   `xml:"testsuites"`
	Name     string     `xml:"name,attr,omitempty"`
	Tests    int        `xml:"tests,attr"`
	Failures int        `xml:"failures,attr"`
	Errors   int        `xml:"errors,attr"`
	Time     string     `xml:"time,attr"`
	Suites   []xmlSuite `xml:"testsuite"`
}

type xmlSuite struct {
	Name       string         `xml:"name,attr"`
	Tests      int            `xml:"tests,attr"`
	Failures   int            `xml:"failures,attr"`
	Errors     int            `xml:"errors,attr"`
	Skipped    int            `xml:"skipped,attr"`
	Time       string         `xml:"time,attr"`
	Timestamp  string         `xml:"timestamp,attr,omitempty"`
	Hostname   string         `xml:"hostname,attr,omitempty"`
	Properties *xmlProperties `xml:"properties"`
	Cases      []xmlCase      `xml:"testcase"`
	SystemOut  string         `xml:"system-out,omitempty"` //nolint:tagliatelle // schema element name
	SystemErr  string         `xml:"system-err,omitempty"` //nolint:tagliatelle // schema element name
}

type xmlProperties struct {
	Items []xmlProperty `xml:"property"`
}

type xmlProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type xmlCase struct {
	Name      string     `xml:"name,attr"`
	Classname string     `xml:"classname,attr,omitempty"`
	Time      string     `xml:"time,attr"`
	Failure   *xmlResult `xml:"failure"`
	Error     *xmlResult `xml:"error"`
	Skipped   *xmlResult `xml:"skipped"`
	SystemOut string     `xml:"system-out,omitempty"` //nolint:tagliatelle // schema element name
	SystemErr string     `xml:"system-err,omitempty"` //nolint:tagliatelle // schema element name
}

type xmlResult struct {
	Message string `xml:"message,attr,omitempty"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"` //nolint:tagliatelle // chardata has no element name
}
