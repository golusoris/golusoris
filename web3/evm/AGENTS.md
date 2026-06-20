# Agent guide — web3/evm/

Ethereum/EVM JSON-RPC helpers over [go-ethereum]. `Client` wraps
`ethclient.Client` with convenience reads; `Key` wraps an ECDSA signing key.
Stateless library — **no fx wiring**; apps import it directly.

## API

```go
client, err := evm.Dial(ctx, "https://mainnet.infura.io/v3/<key>") // HTTP, WS, or IPC
defer client.Close()
id,  err := client.ChainID(ctx)
bal, err := client.ETHBalance(ctx, "0x...")  // Wei
n,   err := client.BlockNumber(ctx)
ec       := client.Raw()                     // *ethclient.Client for advanced use

key, err := evm.NewKey()              // random
key, err  = evm.KeyFromHex("...")     // load (no 0x prefix needed)
addr     := key.Address()
eth      := evm.WeiToEther(bal)       // *big.Float
wei, err := evm.EtherToWei("1.5")     // *big.Int
```

## Why go-ethereum

Canonical Go Ethereum implementation — the only full-coverage EVM client lib.

## Notes

- **Own go.mod sub-module.** go-ethereum's graph is ~500 MB (C crypto,
  libsecp256k1) so it is split out; most apps never pull it. Import path is the
  full module path, it is not part of the root module.
- **Security-critical:** `Key`/`KeyFromHex` hold raw private keys — never log
  them; load from a secret store, not config.
- Reads only (balance/chain/block); signing & tx submission go through
  `key.PrivateKey()` + `client.Raw()`.
