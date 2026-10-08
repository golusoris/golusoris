// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ecr

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/container/registry/credentials"
)

func TestRegion(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"123456789012.dkr.ecr.eu-central-1.amazonaws.com":       "eu-central-1",
		"123456789012.dkr.ecr-fips.us-gov-west-1.amazonaws.com": "us-gov-west-1",
		"123456789012.dkr.ecr.cn-north-1.amazonaws.com.cn":      "cn-north-1",
		"123456789012.dkr-ecr.us-east-1.on.aws":                 "us-east-1",
		"12345678901.dkr.ecr.us-east-1.amazonaws.com":           "",
		"123456789012.dkr.ecr.us-east-1.amazonaws.com.evil.io":  "",
		"public.ecr.aws": "",
		"ghcr.io":        "",
	}
	for host, want := range cases {
		got, ok := Region(host)
		if got != want || ok != (want != "") {
			t.Errorf("Region(%q) = %q, %v; want %q", host, got, ok, want)
		}
	}
}

// fakeECR answers GetAuthorizationToken (awsJson1_1) with body and status.
func fakeECR(t *testing.T, status int, body string) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.Header.Get("X-Amz-Target"), ".GetAuthorizationToken") {
			t.Errorf("unexpected target %q", r.Header.Get("X-Amz-Target"))
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	cfg := aws.Config{Region: "us-east-1", Credentials: aws.AnonymousCredentials{}}
	return New(cfg, func(o *ecr.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
		o.RetryMaxAttempts = 1
	})
}

const testHost = "123456789012.dkr.ecr.eu-central-1.amazonaws.com"

func TestCredential(t *testing.T) {
	t.Parallel()
	p := fakeECR(t, http.StatusOK,
		`{"authorizationData":[{"authorizationToken":"QVdTOnNlY3JldA==","expiresAt":1900000000,"proxyEndpoint":"https://`+testHost+`"}]}`)
	got, err := p.Credential(t.Context(), testHost)
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if got.Username != "AWS" || got.Password != "secret" || !got.ExpiresAt.Equal(time.Unix(1900000000, 0)) {
		t.Fatalf("credential = %+v", got)
	}
	if _, err = p.Credential(t.Context(), "ghcr.io"); !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("non-ECR host err = %v", err)
	}
}

func TestCredential_Failures(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		status int
		body   string
		want   error
	}{
		"denied":      {http.StatusBadRequest, `{"__type":"AccessDeniedException","message":"no"}`, nil},
		"empty data":  {http.StatusOK, `{"authorizationData":[]}`, ErrToken},
		"bad base64":  {http.StatusOK, `{"authorizationData":[{"authorizationToken":"%%%"}]}`, ErrToken},
		"no password": {http.StatusOK, `{"authorizationData":[{"authorizationToken":"QVdT"}]}`, ErrToken},
	}
	for name, tc := range cases {
		_, err := fakeECR(t, tc.status, tc.body).Credential(t.Context(), testHost)
		if err == nil || errors.Is(err, credentials.ErrNoCredential) {
			t.Errorf("%s: err = %v, want hard error", name, err)
			continue
		}
		if tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

// fakeAWS serves STS AssumeRoleWithWebIdentity and ECR GetAuthorizationToken
// on one endpoint, the way AWS_ENDPOINT_URL routes every service. ECR calls
// must be signed with the key STS issued, proving the IRSA path ran.
func fakeAWS(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.Header.Get("X-Amz-Target"), ".GetAuthorizationToken") {
			if !strings.Contains(r.Header.Get("Authorization"), "Credential=ASIAWEBIDENTITY/") {
				http.Error(w, "unsigned", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/x-amz-json-1.1")
			_, _ = w.Write([]byte(`{"authorizationData":[{"authorizationToken":"QVdTOmlyc2E=","expiresAt":1900000000}]}`))
			return
		}
		if err := r.ParseForm(); err != nil || r.PostForm.Get("Action") != "AssumeRoleWithWebIdentity" ||
			r.PostForm.Get("WebIdentityToken") != "projected-sa-token" {
			http.Error(w, "bad sts request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
<AssumeRoleWithWebIdentityResult><Credentials><AccessKeyId>ASIAWEBIDENTITY</AccessKeyId>
<SecretAccessKey>secret</SecretAccessKey><SessionToken>session</SessionToken>
<Expiration>2030-01-01T00:00:00Z</Expiration></Credentials>
<AssumedRoleUser><Arn>arn:aws:sts::123456789012:assumed-role/vmafx/s</Arn><AssumedRoleId>ARO:s</AssumedRoleId></AssumedRoleUser>
</AssumeRoleWithWebIdentityResult><ResponseMetadata><RequestId>1</RequestId></ResponseMetadata>
</AssumeRoleWithWebIdentityResponse>`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// loopbackOnly fails every request that would leave the host, so the IRSA
// test can never reach real AWS endpoints.
type loopbackOnly struct{}

func (loopbackOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() != "127.0.0.1" {
		return nil, errors.New("test egress blocked: " + r.URL.Host)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func irsaEnv(t *testing.T, endpoint string) {
	t.Helper()
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(token, []byte("projected-sa-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setEnv(t, map[string]string{
		"AWS_ROLE_ARN": "arn:aws:iam::123456789012:role/vmafx", "AWS_WEB_IDENTITY_TOKEN_FILE": token,
		"AWS_REGION": "eu-central-1", "AWS_ENDPOINT_URL_STS": endpoint, "AWS_ENDPOINT_URL_ECR": endpoint,
		"AWS_CONFIG_FILE": empty, "AWS_SHARED_CREDENTIALS_FILE": empty, "AWS_EC2_METADATA_DISABLED": "true",
		"AWS_PROFILE": "", "AWS_ACCESS_KEY_ID": "", "AWS_SECRET_ACCESS_KEY": "", "AWS_SESSION_TOKEN": "",
	})
}

//nolint:paralleltest // t.Setenv drives the AWS default chain into IRSA mode.
func TestNewDefault_IRSA(t *testing.T) {
	irsaEnv(t, fakeAWS(t).URL)
	p, err := NewDefault(t.Context(), config.WithHTTPClient(&http.Client{Transport: loopbackOnly{}}))
	if err != nil {
		t.Fatalf("NewDefault: %v", err)
	}
	got, err := p.Credential(t.Context(), testHost)
	if err != nil || got.Username != "AWS" || got.Password != "irsa" {
		t.Fatalf("credential = %+v, %v", got, err)
	}
}

//nolint:paralleltest // t.Setenv keeps the module's AWS config hermetic.
func TestModule(t *testing.T) {
	irsaEnv(t, "http://127.0.0.1:1")
	var providers []credentials.Provider
	app := fxtest.New(t, Module, fx.Invoke(fx.Annotate(
		func(ps []credentials.Provider) { providers = ps }, fx.ParamTags(credentials.Group))))
	app.RequireStart()
	defer app.RequireStop()
	if len(providers) != 1 {
		t.Fatalf("group has %d providers, want 1", len(providers))
	}
	if _, err := providers[0].Credential(t.Context(), "ghcr.io"); !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("non-ECR host err = %v", err)
	}
}
