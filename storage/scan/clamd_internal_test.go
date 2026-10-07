// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build unix

package scan

import (
	"errors"
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

func TestMapResponses_AllLinesAreValidated(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		responses []*clamd.Response
		infected  bool
		wantErr   bool
	}{
		{name: "nil only", responses: []*clamd.Response{nil}, wantErr: true},
		{name: "ok then nil", responses: append(canned("OK", ""), nil), wantErr: true},
		{name: "nil then ok", responses: append([]*clamd.Response{nil}, canned("OK", "")...), wantErr: true},
		{name: "ok then error", responses: append(canned("OK", ""), canned("ERROR", "")...), wantErr: true},
		{name: "error then ok", responses: append(canned("ERROR", ""), canned("OK", "")...), wantErr: true},
		{name: "all ok", responses: append(canned("OK", ""), canned("OK", "")...)},
		{
			name:      "found after error",
			responses: append(canned("ERROR", ""), canned("FOUND", "Eicar-Test-Signature")...),
			infected:  true,
		},
		{
			name:      "found after nil",
			responses: append([]*clamd.Response{nil}, canned("FOUND", "Eicar-Test-Signature")...),
			infected:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			verdict, err := mapResponses(tc.responses)
			if tc.wantErr {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("mapResponses() error = %v, want ErrUnavailable", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("mapResponses() error = %v", err)
			}
			if tc.infected == verdict.Clean {
				t.Fatalf("mapResponses() verdict = %+v, infected = %t", verdict, tc.infected)
			}
		})
	}
}
