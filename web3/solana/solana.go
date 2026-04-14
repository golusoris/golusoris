// Package solana provides Solana blockchain helpers using gagliardetto/solana-go.
//
// This is a separate go.mod sub-module because solana-go's dependency graph is
// large and specialised.
// Import directly: github.com/golusoris/golusoris/web3/solana
//
// # Connection
//
//	client := solana.NewClient("https://api.mainnet-beta.solana.com")
//	bal, err := client.SOLBalance(ctx, "...")
//
// # Key management
//
//	key, err := solana.NewKey()
//	addr := key.PublicKey()
package solana

import (
	"context"
	"fmt"

	bin "github.com/gagliardetto/binary"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

// Client wraps a Solana RPC client.
type Client struct {
	rpc *rpc.Client
}

// NewClient creates a Solana RPC client for endpoint (e.g. rpc.MainNetBeta_RPC).
func NewClient(endpoint string) *Client {
	return &Client{rpc: rpc.New(endpoint)}
}

// SOLBalance returns the SOL balance of address in lamports.
func (c *Client) SOLBalance(ctx context.Context, address string) (uint64, error) {
	pk, err := solanago.PublicKeyFromBase58(address)
	if err != nil {
		return 0, fmt.Errorf("solana: parse address %q: %w", address, err)
	}
	out, err := c.rpc.GetBalance(ctx, pk, rpc.CommitmentFinalized)
	if err != nil {
		return 0, fmt.Errorf("solana: get balance: %w", err)
	}
	return out.Value, nil
}

// SlotNumber returns the latest confirmed slot.
func (c *Client) SlotNumber(ctx context.Context) (uint64, error) {
	slot, err := c.rpc.GetSlot(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return 0, fmt.Errorf("solana: get slot: %w", err)
	}
	return slot, nil
}

// Raw returns the underlying *rpc.Client for advanced use.
func (c *Client) Raw() *rpc.Client { return c.rpc }

// ---------------------------------------------------------------------------
// Key management
// ---------------------------------------------------------------------------

// Key wraps a Solana Ed25519 keypair.
type Key struct {
	kp solanago.PrivateKey
}

// NewKey generates a fresh random Solana keypair.
func NewKey() (*Key, error) {
	kp, err := solanago.NewRandomPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("solana: generate key: %w", err)
	}
	return &Key{kp: kp}, nil
}

// KeyFromBase58 loads a key from a base58-encoded private key string.
func KeyFromBase58(b58 string) (*Key, error) {
	kp, err := solanago.PrivateKeyFromBase58(b58)
	if err != nil {
		return nil, fmt.Errorf("solana: parse key: %w", err)
	}
	return &Key{kp: kp}, nil
}

// PublicKey returns the base58-encoded public key (wallet address).
func (k *Key) PublicKey() string { return k.kp.PublicKey().String() }

// PrivateKey returns the raw private key.
func (k *Key) PrivateKey() solanago.PrivateKey { return k.kp }

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

// LamportsToSOL converts lamports (1e9 per SOL) to SOL as a float64.
func LamportsToSOL(lamports uint64) float64 {
	return float64(lamports) / 1e9
}

// SOLToLamports converts SOL to lamports.
func SOLToLamports(sol float64) uint64 {
	return uint64(sol * 1e9)
}

// ensure binary is used (it's an indirect dep that some linters require referenced)
var _ = bin.MarshalerDecoder(nil)
