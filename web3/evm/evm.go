// Package evm provides Ethereum/EVM chain helpers using go-ethereum.
//
// This is a separate go.mod sub-module because go-ethereum's dependency graph
// is ~500 MB (C crypto, libsecp256k1 headers, etc.) and most apps don't need it.
// Import directly: github.com/golusoris/golusoris/web3/evm
//
// # Connection
//
//	client, err := evm.Dial(ctx, "https://mainnet.infura.io/v3/<key>")
//	balance, err := client.ETHBalance(ctx, "0x...")
//
// # Key management
//
//	key, err := evm.NewKey()          // generate random key
//	key, err := evm.KeyFromHex("...")  // load from private key hex
//	addr := key.Address()
package evm

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Client wraps an ethclient.Client with convenience methods.
type Client struct {
	ec *ethclient.Client
}

// Dial connects to an EVM-compatible JSON-RPC endpoint (HTTP, WS, or IPC).
func Dial(ctx context.Context, rawURL string) (*Client, error) {
	ec, err := ethclient.DialContext(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("evm: dial %s: %w", rawURL, err)
	}
	return &Client{ec: ec}, nil
}

// Close releases the connection.
func (c *Client) Close() { c.ec.Close() }

// ChainID returns the network chain ID.
func (c *Client) ChainID(ctx context.Context) (*big.Int, error) {
	id, err := c.ec.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("evm: chain id: %w", err)
	}
	return id, nil
}

// ETHBalance returns the ETH balance of addr in Wei.
func (c *Client) ETHBalance(ctx context.Context, addr string) (*big.Int, error) {
	a := common.HexToAddress(addr)
	bal, err := c.ec.BalanceAt(ctx, a, nil)
	if err != nil {
		return nil, fmt.Errorf("evm: balance %s: %w", addr, err)
	}
	return bal, nil
}

// BlockNumber returns the latest block number.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	n, err := c.ec.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("evm: block number: %w", err)
	}
	return n, nil
}

// Raw returns the underlying *ethclient.Client for advanced use.
func (c *Client) Raw() *ethclient.Client { return c.ec }

// ---------------------------------------------------------------------------
// Key management
// ---------------------------------------------------------------------------

// Key wraps an ECDSA private key for EVM signing.
type Key struct {
	priv *ecdsa.PrivateKey
}

// NewKey generates a fresh random Ethereum key.
func NewKey() (*Key, error) {
	priv, err := crypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("evm: generate key: %w", err)
	}
	return &Key{priv: priv}, nil
}

// KeyFromHex loads a private key from a 32-byte hex string (no 0x prefix required).
func KeyFromHex(hexKey string) (*Key, error) {
	priv, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		return nil, fmt.Errorf("evm: parse key: %w", err)
	}
	return &Key{priv: priv}, nil
}

// Address returns the Ethereum address derived from the key.
func (k *Key) Address() string {
	return crypto.PubkeyToAddress(k.priv.PublicKey).Hex()
}

// PrivateKey returns the raw ECDSA private key.
func (k *Key) PrivateKey() *ecdsa.PrivateKey { return k.priv }

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

// WeiToEther converts Wei to Ether (18 decimal places).
func WeiToEther(wei *big.Int) *big.Float {
	f := new(big.Float).SetPrec(256)
	f.SetInt(wei)
	return new(big.Float).Quo(f, big.NewFloat(1e18))
}

// EtherToWei converts an Ether amount string (e.g. "1.5") to Wei.
func EtherToWei(ether string) (*big.Int, error) {
	f, _, err := big.ParseFloat(ether, 10, 256, big.ToNearestEven)
	if err != nil {
		return nil, fmt.Errorf("evm: parse ether %q: %w", ether, err)
	}
	f.Mul(f, big.NewFloat(1e18))
	result := new(big.Int)
	f.Int(result)
	return result, nil
}
