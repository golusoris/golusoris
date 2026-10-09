// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package aws_test

import (
	"crypto"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/container/registry/sign/kms/aws"
)

const (
	roleARN     = "arn:aws:iam::111122223333:role/signer"
	saToken     = "eyJhbGciOiJSUzI1NiJ9.sa-token.sig" // #nosec G101 -- a fake web identity token.
	podToken    = "pod-identity-token"                // #nosec G101 -- a fake agent token.
	webIdentity = "ASIAWEBIDENTITY"
	podIdentity = "ASIAPODIDENTITY"
)

// isolateChain points the default chain at nothing but what the test sets:
// no shared files, profile, IMDS or endpoint override from the host.
func isolateChain(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for k, v := range map[string]string{
		"AWS_CONFIG_FILE":                        filepath.Join(dir, "config"),
		"AWS_SHARED_CREDENTIALS_FILE":            filepath.Join(dir, "credentials"),
		"AWS_EC2_METADATA_DISABLED":              "true",
		"AWS_REGION":                             "eu-central-1",
		"AWS_DEFAULT_REGION":                     "",
		"AWS_PROFILE":                            "",
		"AWS_ACCESS_KEY_ID":                      "",
		"AWS_SECRET_ACCESS_KEY":                  "",
		"AWS_SESSION_TOKEN":                      "",
		"AWS_ROLE_ARN":                           "",
		"AWS_WEB_IDENTITY_TOKEN_FILE":            "",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI":     "",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI": "",
		"AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE": "",
		"AWS_ENDPOINT_URL":                       "",
		"AWS_ENDPOINT_URL_STS":                   "",
		"AWS_ENDPOINT_URL_KMS":                   "",
	} {
		t.Setenv(k, v)
	}
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeSTS answers AssumeRoleWithWebIdentity for roleARN and saToken.
func fakeSTS(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.PostForm.Get("Action") != "AssumeRoleWithWebIdentity" ||
			r.PostForm.Get("RoleArn") != roleARN || r.PostForm.Get("WebIdentityToken") != saToken {
			http.Error(w, "<ErrorResponse><Error><Code>InvalidIdentityToken</Code></Error></ErrorResponse>", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		if _, err := io.WriteString(w, `<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
<AssumeRoleWithWebIdentityResult><Credentials><AccessKeyId>`+webIdentity+`</AccessKeyId>
<SecretAccessKey>secret</SecretAccessKey><SessionToken>session</SessionToken>
<Expiration>2099-01-01T00:00:00Z</Expiration></Credentials></AssumeRoleWithWebIdentityResult>
</AssumeRoleWithWebIdentityResponse>`); err != nil {
			t.Errorf("fake STS: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// fakePodIdentity answers the EKS Pod Identity agent credential request.
func fakePodIdentity(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != podToken {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]string{
			"AccessKeyId": podIdentity, "SecretAccessKey": "secret", "Token": "session",
			"Expiration": "2099-01-01T00:00:00Z",
		}); err != nil {
			t.Errorf("fake pod identity: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1/credentials"
}

// TestNew_DefaultChain loads credentials through config.LoadDefaultConfig:
// static environment keys, IRSA web identity and EKS Pod Identity. Not
// parallel: the chain reads process environment.
func TestNew_DefaultChain(t *testing.T) {
	for _, tc := range []struct {
		name, akid, region string
		env                func(t *testing.T) map[string]string
	}{
		{"environment", "AKIDENVIRONMENT", "", func(*testing.T) map[string]string {
			return map[string]string{"AWS_ACCESS_KEY_ID": "AKIDENVIRONMENT", "AWS_SECRET_ACCESS_KEY": "secret"}
		}},
		{"irsa web identity", webIdentity, "", func(t *testing.T) map[string]string {
			t.Helper()
			return map[string]string{
				"AWS_ROLE_ARN": roleARN, "AWS_WEB_IDENTITY_TOKEN_FILE": writeFile(t, saToken),
				"AWS_ENDPOINT_URL_STS": fakeSTS(t),
			}
		}},
		{"irsa with config region", webIdentity, "eu-central-1", func(t *testing.T) map[string]string {
			t.Helper()
			// The web identity STS client gets its region from Config.Region only.
			return map[string]string{
				"AWS_REGION": "", "AWS_ROLE_ARN": roleARN, "AWS_WEB_IDENTITY_TOKEN_FILE": writeFile(t, saToken),
				"AWS_ENDPOINT_URL_STS": fakeSTS(t),
			}
		}},
		{"eks pod identity", podIdentity, "", func(t *testing.T) map[string]string {
			t.Helper()
			return map[string]string{
				"AWS_CONTAINER_CREDENTIALS_FULL_URI":     fakePodIdentity(t),
				"AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE": writeFile(t, podToken),
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateChain(t)
			for k, v := range tc.env(t) {
				t.Setenv(k, v)
			}
			f, addr := newFake(t)
			f.addKeyIn("eu-central-1", "k", "ECC_NIST_P256")
			s := newSigner(t, aws.Config{Key: "k", Endpoint: addr, Region: tc.region})
			if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
				t.Fatalf("Sign: %v", err)
			}
			auth := f.get().auth
			if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="+tc.akid+"/") || scope(auth) != "eu-central-1/kms" {
				t.Fatalf("Authorization = %q, want %s in eu-central-1", auth, tc.akid)
			}
		})
	}
}

func TestNew_DefaultChainFailures(t *testing.T) {
	isolateChain(t)
	f, addr := newFake(t)
	f.addKeyIn("eu-central-1", "k", "ECC_NIST_P256")
	if _, err := aws.New(t.Context(), aws.Config{Key: "k", Endpoint: addr}); err == nil || !strings.Contains(err.Error(), "get public key k") {
		t.Fatalf("no credentials: New error = %v", err)
	}
	t.Setenv("AWS_ROLE_ARN", roleARN)
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", writeFile(t, "other-token"))
	t.Setenv("AWS_ENDPOINT_URL_STS", fakeSTS(t))
	if _, err := aws.New(t.Context(), aws.Config{Key: "k", Endpoint: addr}); err == nil || !strings.Contains(err.Error(), "InvalidIdentityToken") {
		t.Fatalf("rejected web identity: New error = %v", err)
	}
	t.Setenv("AWS_REGION", "")
	if _, err := aws.New(t.Context(), aws.Config{Key: "k", Endpoint: addr}); !strings.Contains(errString(err), "no region") {
		t.Fatalf("no region: New error = %v", err)
	}
	t.Setenv("AWS_PROFILE", "absent")
	if _, err := aws.New(t.Context(), aws.Config{Key: "k", Endpoint: addr, Region: "eu-central-1"}); !strings.Contains(errString(err), "load AWS config") {
		t.Fatalf("absent profile: New error = %v", err)
	}
	if n := f.get().signs; n != 0 {
		t.Fatalf("failed credentials signed %d times", n)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
