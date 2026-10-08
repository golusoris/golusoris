// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

const stsCredentialsXML = `<%[1]sResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
<%[1]sResult><Credentials><AccessKeyId>ASIAROLETEST</AccessKeyId>
<SecretAccessKey>role-secret</SecretAccessKey><SessionToken>role-session-token</SessionToken>
<Expiration>2099-01-01T00:00:00Z</Expiration></Credentials>
<AssumedRoleUser><Arn>arn:aws:sts::123456789012:assumed-role/app/s</Arn><AssumedRoleId>AROA:s</AssumedRoleId></AssumedRoleUser>
</%[1]sResult><ResponseMetadata><RequestId>r</RequestId></ResponseMetadata></%[1]sResponse>`

type stsRecorder struct {
	mu     sync.Mutex
	form   url.Values
	authz  string
	action string
}

func newSTSServer(t *testing.T, rec *stsRecorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		rec.mu.Lock()
		rec.form, rec.authz, rec.action = r.PostForm, r.Header.Get("Authorization"), r.PostForm.Get("Action")
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(strings.ReplaceAll(stsCredentialsXML, "%[1]s", r.PostForm.Get("Action"))))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newAuthRecordingS3 answers HeadObject and records the SigV4 credential used.
func newAuthRecordingS3(t *testing.T) (*httptest.Server, *string, *string) {
	t.Helper()
	var authz, token string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authz, token = r.Header.Get("Authorization"), r.Header.Get("X-Amz-Security-Token")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &authz, &token
}

func TestNewS3Bucket_WebIdentityRole(t *testing.T) {
	t.Parallel()
	rec := &stsRecorder{}
	sts := newSTSServer(t, rec)
	s3srv, authz, token := newAuthRecordingS3(t)
	tokenFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("projected-sa-jwt"), 0o600))

	b, err := NewS3Bucket(context.Background(), S3Options{
		Bucket: "b", Region: "eu-central-1", Endpoint: s3srv.URL, PathStyle: true,
		RoleARN: "arn:aws:iam::123456789012:role/app", WebIdentityTokenFile: tokenFile,
		RoleSessionName: "vmafx", STSEndpoint: sts.URL,
	})
	require.NoError(t, err)
	ok, err := b.Exists(context.Background(), "k")
	require.NoError(t, err)
	require.True(t, ok)

	require.Equal(t, "AssumeRoleWithWebIdentity", rec.action)
	require.Equal(t, "projected-sa-jwt", rec.form.Get("WebIdentityToken"))
	require.Equal(t, "arn:aws:iam::123456789012:role/app", rec.form.Get("RoleArn"))
	require.Equal(t, "vmafx", rec.form.Get("RoleSessionName"))
	require.Contains(t, *authz, "Credential=ASIAROLETEST/")
	require.Equal(t, "role-session-token", *token)
}

func TestNewS3Bucket_AssumeRoleFromStaticKeys(t *testing.T) {
	t.Parallel()
	rec := &stsRecorder{}
	sts := newSTSServer(t, rec)
	s3srv, authz, _ := newAuthRecordingS3(t)

	b, err := NewS3Bucket(context.Background(), S3Options{
		Bucket: "b", Region: "eu-central-1", Endpoint: s3srv.URL, PathStyle: true,
		AccessKey: "AKIABASE", SecretKey: "base-secret",
		RoleARN: "arn:aws:iam::123456789012:role/app", STSEndpoint: sts.URL,
	})
	require.NoError(t, err)
	_, err = b.Exists(context.Background(), "k")
	require.NoError(t, err)

	require.Equal(t, "AssumeRole", rec.action)
	require.Contains(t, rec.authz, "Credential=AKIABASE/")
	require.Contains(t, *authz, "Credential=ASIAROLETEST/")
}

func TestNewS3Bucket_StaticKeysWithoutRoleSkipSTS(t *testing.T) {
	t.Parallel()
	s3srv, authz, _ := newAuthRecordingS3(t)
	b, err := NewS3Bucket(context.Background(), S3Options{
		Bucket: "b", Region: "us-east-1", Endpoint: s3srv.URL, PathStyle: true,
		AccessKey: "AKIASTATIC", SecretKey: "s", STSEndpoint: "http://127.0.0.1:1",
	})
	require.NoError(t, err)
	_, err = b.Exists(context.Background(), "k")
	require.NoError(t, err)
	require.Contains(t, *authz, "Credential=AKIASTATIC/")
}
