// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package passkeys_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/passkeys"
)

type ceremonyUser struct {
	id          []byte
	name        string
	displayName string
	credentials []passkeys.Credential
}

func (u *ceremonyUser) WebAuthnID() []byte                         { return u.id }
func (u *ceremonyUser) WebAuthnName() string                       { return u.name }
func (u *ceremonyUser) WebAuthnDisplayName() string                { return u.displayName }
func (u *ceremonyUser) WebAuthnCredentials() []passkeys.Credential { return u.credentials }

type softwareAuthenticator struct {
	key    *ecdsa.PrivateKey
	credID []byte
}

func newSoftwareAuthenticator(t *testing.T) *softwareAuthenticator {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	credID := make([]byte, 16)
	_, err = rand.Read(credID)
	require.NoError(t, err)

	return &softwareAuthenticator{
		key:    k,
		credID: credID,
	}
}

func (sa *softwareAuthenticator) makeRegistrationRequest(
	t *testing.T,
	challenge string,
	origin string,
	rpID string,
) *http.Request {
	t.Helper()

	clientData, err := json.Marshal(map[string]any{
		"type":        "webauthn.create",
		"challenge":   challenge,
		"origin":      origin,
		"crossOrigin": false,
	})
	require.NoError(t, err)

	x := make([]byte, 32)
	y := make([]byte, 32)
	sa.key.X.FillBytes(x)
	sa.key.Y.FillBytes(y)

	coseKey, err := cbor.Marshal(map[int64]any{
		1:  2,  // kty: EC2
		3:  -7, // alg: ES256
		-1: 1,  // crv: P-256
		-2: x,
		-3: y,
	})
	require.NoError(t, err)

	rpHash := sha256.Sum256([]byte(rpID))
	aaguid := make([]byte, 16) // all zeroes
	credIDLen := make([]byte, 2)
	binary.BigEndian.PutUint16(credIDLen, uint16(len(sa.credID)))

	authData := make([]byte, 0, len(rpHash)+1+4+len(aaguid)+len(credIDLen)+len(sa.credID)+len(coseKey))
	authData = append(authData, rpHash[:]...)
	authData = append(authData, 0x41)       // FlagUserPresent | FlagAttestedCredentialData
	authData = append(authData, 0, 0, 0, 0) // SignCount = 0
	authData = append(authData, aaguid...)
	authData = append(authData, credIDLen...)
	authData = append(authData, sa.credID...)
	authData = append(authData, coseKey...)

	attObj, err := cbor.Marshal(map[string]any{
		"fmt":      "none",
		"attStmt":  map[string]any{},
		"authData": authData,
	})
	require.NoError(t, err)

	body, err := json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(sa.credID),
		"rawId": base64.RawURLEncoding.EncodeToString(sa.credID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			"attestationObject": base64.RawURLEncoding.EncodeToString(attObj),
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, origin+"/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func (sa *softwareAuthenticator) makeAssertionRequest(
	t *testing.T,
	challenge string,
	origin string,
	rpID string,
	userHandle []byte,
	signCount uint32,
	corruptSignature bool,
) *http.Request {
	t.Helper()

	clientData, err := json.Marshal(map[string]any{
		"type":        "webauthn.get",
		"challenge":   challenge,
		"origin":      origin,
		"crossOrigin": false,
	})
	require.NoError(t, err)

	rpHash := sha256.Sum256([]byte(rpID))
	countBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(countBytes, signCount)

	authData := make([]byte, 0, len(rpHash)+1+len(countBytes))
	authData = append(authData, rpHash[:]...)
	authData = append(authData, 0x01) // FlagUserPresent
	authData = append(authData, countBytes...)

	clientDataHash := sha256.Sum256(clientData)
	sigData := append(append([]byte(nil), authData...), clientDataHash[:]...)
	sigHash := sha256.Sum256(sigData)

	sig, err := ecdsa.SignASN1(rand.Reader, sa.key, sigHash[:])
	require.NoError(t, err)

	if corruptSignature {
		sig[len(sig)-1] ^= 0xff
	}

	body, err := json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(sa.credID),
		"rawId": base64.RawURLEncoding.EncodeToString(sa.credID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			"authenticatorData": base64.RawURLEncoding.EncodeToString(authData),
			"signature":         base64.RawURLEncoding.EncodeToString(sig),
			"userHandle":        base64.RawURLEncoding.EncodeToString(userHandle),
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, origin+"/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// assertPasskeysRegistrationCeremony executes the registration ceremony and returns the session and request for negative tests.
func assertPasskeysRegistrationCeremony(t *testing.T, svc *passkeys.Service, auth *softwareAuthenticator, user *ceremonyUser, rpOrigin, rpID string) (*passkeys.SessionData, *http.Request) {
	t.Helper()
	creation, regSess, err := svc.BeginRegistration(user)
	require.NoError(t, err)
	require.NotNil(t, creation)
	require.NotNil(t, regSess)
	require.NotEmpty(t, regSess.Challenge)

	regReq := auth.makeRegistrationRequest(t, regSess.Challenge, rpOrigin, rpID)
	cred, err := svc.FinishRegistration(user, *regSess, regReq)
	require.NoError(t, err)
	require.NotNil(t, cred)
	require.Equal(t, auth.credID, cred.ID)
	require.Equal(t, "none", cred.AttestationType)

	user.credentials = append(user.credentials, *cred)
	return regSess, regReq
}

// assertPasskeysLoginCeremony executes the login ceremony and returns the login session for negative tests.
func assertPasskeysLoginCeremony(t *testing.T, svc *passkeys.Service, auth *softwareAuthenticator, user *ceremonyUser, rpOrigin, rpID string) *passkeys.SessionData {
	t.Helper()
	assertion, loginSess, err := svc.BeginLogin(user)
	require.NoError(t, err)
	require.NotNil(t, assertion)
	require.NotNil(t, loginSess)
	require.NotEmpty(t, loginSess.Challenge)

	loginReq := auth.makeAssertionRequest(t, loginSess.Challenge, rpOrigin, rpID, user.id, 1, false)
	authCred, err := svc.FinishLogin(user, *loginSess, loginReq)
	require.NoError(t, err)
	require.NotNil(t, authCred)
	require.Equal(t, auth.credID, authCred.ID)

	user.credentials[0] = *authCred
	return loginSess
}

// assertPasskeysRegistrationNegatives verifies invalid registration attempts are rejected.
func assertPasskeysRegistrationNegatives(t *testing.T, svc *passkeys.Service, auth *softwareAuthenticator, user *ceremonyUser, regSess *passkeys.SessionData, regReq *http.Request, rpOrigin, rpID string) {
	t.Helper()
	// a. Wrong challenge
	badChallengeReq := auth.makeRegistrationRequest(t, "wrong-challenge", rpOrigin, rpID)
	_, err := svc.FinishRegistration(user, *regSess, badChallengeReq)
	require.Error(t, err)

	// b. Wrong origin
	badOriginReq := auth.makeRegistrationRequest(t, regSess.Challenge, "https://evil.invalid", rpID)
	_, err = svc.FinishRegistration(user, *regSess, badOriginReq)
	require.Error(t, err)

	// c. Wrong RP ID hash
	badRPHashReq := auth.makeRegistrationRequest(t, regSess.Challenge, rpOrigin, "evil.invalid")
	_, err = svc.FinishRegistration(user, *regSess, badRPHashReq)
	require.Error(t, err)

	// d. Stale / replayed session
	staleSess := *regSess
	staleSess.Challenge = "stale-challenge-value"
	_, err = svc.FinishRegistration(user, staleSess, regReq)
	require.Error(t, err)
}

// assertPasskeysLoginNegatives verifies invalid login attempts and boundaries are rejected.
func assertPasskeysLoginNegatives(t *testing.T, svc *passkeys.Service, auth *softwareAuthenticator, user *ceremonyUser, loginSess *passkeys.SessionData, rpOrigin, rpID string) {
	t.Helper()
	// a. Wrong challenge
	badLoginChallengeReq := auth.makeAssertionRequest(t, "wrong-challenge", rpOrigin, rpID, user.id, 2, false)
	_, err := svc.FinishLogin(user, *loginSess, badLoginChallengeReq)
	require.Error(t, err)

	// b. Wrong origin
	badLoginOriginReq := auth.makeAssertionRequest(t, loginSess.Challenge, "https://evil.invalid", rpID, user.id, 2, false)
	_, err = svc.FinishLogin(user, *loginSess, badLoginOriginReq)
	require.Error(t, err)

	// c. Wrong RP ID hash
	badLoginRPReq := auth.makeAssertionRequest(t, loginSess.Challenge, rpOrigin, "evil.invalid", user.id, 2, false)
	_, err = svc.FinishLogin(user, *loginSess, badLoginRPReq)
	require.Error(t, err)

	// d. Bad signature
	badSigReq := auth.makeAssertionRequest(t, loginSess.Challenge, rpOrigin, rpID, user.id, 2, true)
	_, err = svc.FinishLogin(user, *loginSess, badSigReq)
	require.Error(t, err)

	// 5. Boundary: Sign-count rollback flags CloneWarning on the credential
	rollbackReq := auth.makeAssertionRequest(t, loginSess.Challenge, rpOrigin, rpID, user.id, 0, false)
	clonedCred, err := svc.FinishLogin(user, *loginSess, rollbackReq)
	require.NoError(t, err)
	require.True(t, clonedCred.Authenticator.CloneWarning)

	// 6. Boundary: Empty credential list on BeginLogin
	emptyUser := &ceremonyUser{
		id:   []byte("empty-user"),
		name: "empty@example.com",
	}
	_, _, emptyErr := svc.BeginLogin(emptyUser)
	require.Error(t, emptyErr)
}

func TestWebAuthn_FullCeremonyAndSecurityChecks(t *testing.T) {
	t.Parallel()

	const rpID = "example.com"
	const rpOrigin = "https://example.com"

	svc, err := passkeys.New(passkeys.Options{
		RPID:      rpID,
		RPName:    "Example Corp",
		RPOrigins: []string{rpOrigin},
	})
	require.NoError(t, err)

	auth := newSoftwareAuthenticator(t)
	user := &ceremonyUser{
		id:          []byte("user-12345678"),
		name:        "alice@example.com",
		displayName: "Alice",
	}

	regSess, regReq := assertPasskeysRegistrationCeremony(t, svc, auth, user, rpOrigin, rpID)
	loginSess := assertPasskeysLoginCeremony(t, svc, auth, user, rpOrigin, rpID)
	assertPasskeysRegistrationNegatives(t, svc, auth, user, regSess, regReq, rpOrigin, rpID)
	assertPasskeysLoginNegatives(t, svc, auth, user, loginSess, rpOrigin, rpID)
}
