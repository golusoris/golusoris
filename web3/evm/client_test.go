// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package evm_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/web3/evm"
)

const balanceAddr = "0x360A54983438826b16C921Eabd3f5517AD874F9F"

type rpcCall struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
}

// rpcServer answers JSON-RPC calls from results, records each call, and
// returns a JSON-RPC error for a method missing from results.
func rpcServer(t *testing.T, results map[string]any, calls chan<- rpcCall) *evm.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			rpcCall

			ID json.RawMessage `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		calls <- req.rpcCall
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if res, ok := results[req.Method]; ok {
			resp["result"] = res
		} else {
			resp["error"] = map[string]any{"code": -32601, "message": "boom " + req.Method}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	c, err := evm.Dial(t.Context(), srv.URL)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

func TestClientReadsChainState(t *testing.T) {
	t.Parallel()
	calls := make(chan rpcCall, 3)
	c := rpcServer(t, map[string]any{
		"eth_chainId":     "0xaa36a7",
		"eth_getBalance":  "0xde0b6b3a7640000",
		"eth_blockNumber": "0x10",
	}, calls)

	id, err := c.ChainID(t.Context())
	require.NoError(t, err)
	require.Equal(t, int64(11155111), id.Int64())
	require.Equal(t, "eth_chainId", (<-calls).Method)

	bal, err := c.ETHBalance(t.Context(), balanceAddr)
	require.NoError(t, err)
	require.Equal(t, "1000000000000000000", bal.String())
	call := <-calls
	require.Equal(t, "eth_getBalance", call.Method)
	require.Equal(t, []any{"0x360a54983438826b16c921eabd3f5517ad874f9f", "latest"}, call.Params)

	n, err := c.BlockNumber(t.Context())
	require.NoError(t, err)
	require.Equal(t, uint64(16), n)
	require.Equal(t, "eth_blockNumber", (<-calls).Method)
	require.NotNil(t, c.Raw())
}

func TestClientWrapsRPCErrors(t *testing.T) {
	t.Parallel()
	c := rpcServer(t, map[string]any{}, make(chan rpcCall, 3))

	_, err := c.ChainID(t.Context())
	require.ErrorContains(t, err, "evm: chain id: boom eth_chainId")
	_, err = c.ETHBalance(t.Context(), balanceAddr)
	require.ErrorContains(t, err, "evm: balance "+balanceAddr+": boom eth_getBalance")
	_, err = c.BlockNumber(t.Context())
	require.ErrorContains(t, err, "evm: block number: boom eth_blockNumber")
}
