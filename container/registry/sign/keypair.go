// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sign

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"

	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	sgsign "github.com/sigstore/sigstore-go/pkg/sign"
	"github.com/sigstore/sigstore/pkg/signature"
)

// keyPair adapts a caller-held [crypto.Signer] to sigstore-go's Keypair, the
// way cosign wraps its --key signers: default algorithm for the key type,
// hint = base64(sha256(PKIX public key)).
type keyPair struct {
	signer crypto.Signer
	alg    signature.AlgorithmDetails
	der    []byte
	hint   []byte
}

var _ sgsign.Keypair = (*keyPair)(nil)

// keypairFor wraps key, or generates an ephemeral ECDSA P-256 key when key is
// nil.
func keypairFor(key crypto.Signer) (sgsign.Keypair, error) {
	if key == nil {
		kp, err := sgsign.NewEphemeralKeypair(nil)
		if err != nil {
			return nil, fmt.Errorf("sign: ephemeral key: %w", err)
		}
		return kp, nil
	}
	pub := key.Public()
	alg, err := signature.GetDefaultAlgorithmDetails(pub)
	if err != nil {
		return nil, fmt.Errorf("%w: key: %w", ErrInvalidOptions, err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("%w: key: %w", ErrInvalidOptions, err)
	}
	sum := sha256.Sum256(der)
	return &keyPair{signer: key, alg: alg, der: der, hint: []byte(base64.StdEncoding.EncodeToString(sum[:]))}, nil
}

func (k *keyPair) GetHashAlgorithm() protocommon.HashAlgorithm {
	return k.alg.GetProtoHashType()
}

func (k *keyPair) GetSigningAlgorithm() protocommon.PublicKeyDetails {
	return k.alg.GetSignatureAlgorithm()
}

func (k *keyPair) GetHint() []byte { return k.hint }

// GetKeyAlgorithm names the key family in Fulcio certificate requests.
func (k *keyPair) GetKeyAlgorithm() string {
	switch k.alg.GetKeyType() {
	case signature.ECDSA:
		return "ECDSA"
	case signature.RSA:
		return "RSA"
	case signature.ED25519:
		return "ED25519"
	default:
		return ""
	}
}

func (k *keyPair) GetPublicKey() crypto.PublicKey { return k.signer.Public() }

func (k *keyPair) GetPublicKeyPem() (string, error) {
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: k.der})), nil
}

// SignData hashes data with the algorithm's hash (none for pure Ed25519) and
// signs the digest. It returns the signature and the signed bytes.
func (k *keyPair) SignData(_ context.Context, data []byte) ([]byte, []byte, error) {
	hf := k.alg.GetHashType()
	toSign := data
	if hf != crypto.Hash(0) {
		h := hf.New()
		h.Write(data)
		toSign = h.Sum(nil)
	}
	sig, err := k.signer.Sign(rand.Reader, toSign, hf)
	if err != nil {
		return nil, nil, fmt.Errorf("sign: key: %w", err)
	}
	return sig, toSign, nil
}
