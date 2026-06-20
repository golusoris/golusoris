# Agent guide — web3/solana/

Solana RPC helpers over [gagliardetto/solana-go]. `Client` wraps the RPC client
with convenience reads; `Key` wraps an Ed25519 keypair. Stateless library —
**no fx wiring**; apps import it directly.

## API

```go
client := solana.NewClient("https://api.mainnet-beta.solana.com") // or rpc.MainNetBeta_RPC
bal,  err := client.SOLBalance(ctx, "<base58 addr>") // lamports
slot, err := client.SlotNumber(ctx)
rc        := client.Raw()                            // *rpc.Client for advanced use

key, err := solana.NewKey()              // random keypair
key, err  = solana.KeyFromBase58("...")  // load private key
addr     := key.PublicKey()              // base58 wallet address
sol      := solana.LamportsToSOL(bal)    // float64
lamports := solana.SOLToLamports(1.5)
```

## Why gagliardetto/solana-go

The most complete Go Solana SDK — RPC + websocket + transaction building.

## Notes

- **Own go.mod sub-module** (large, specialised dep graph); import via the full
  module path, it is not part of the root module.
- **Security-critical:** `Key`/`KeyFromBase58` hold raw Ed25519 private keys —
  never log them; load from a secret store, not config.
- Lamport↔SOL helpers use `float64` — fine for display, not for exact accounting
  (use the raw `uint64` lamport values for balances).
