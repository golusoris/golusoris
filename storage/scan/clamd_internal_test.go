// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build unix

package scan

import (
	"testing"

	"github.com/baruwa-enterprise/clamd"
)

// canned builds a single-element clamd response slice for mapResponses tests.
func canned(status, signature string) []*clamd.Response {
	return []*clamd.Response{{
		Filename:  "stream",
		Status:    status,
		Signature: signature,
		Raw:       "stream: " + signature + " " + status,
	}}
}

func TestMapResponses(t *testing.T) {
	t.Parallel()
	v, err := mapResponses(canned("OK", ""))
	if err != nil || !v.Clean {
		t.Fatalf("OK -> %+v, %v; want Clean", v, err)
	}
	v, err = mapResponses(canned("FOUND", "Eicar-Test-Signature"))
	if err != nil {
		t.Fatalf("FOUND err: %v", err)
	}
	if v.Clean || v.Signature != "Eicar-Test-Signature" {
		t.Fatalf("FOUND -> %+v, want infected with signature", v)
	}
	if _, err = mapResponses(nil); err == nil {
		t.Fatal("mapResponses(nil) = nil error, want ErrUnavailable")
	}
	if _, err = mapResponses(canned("ERROR", "")); err == nil {
		t.Fatal("mapResponses(ERROR status) = nil error, want error")
	}
}
