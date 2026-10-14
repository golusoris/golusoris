// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package solana_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/web3/solana"
)

// systemProgram is a valid base58 public key.
const systemProgram = "11111111111111111111111111111111"

type rpcCall struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
}

// rpcServer answers JSON-RPC calls from results, records each call, and
// returns a JSON-RPC error for a method missing from results.
func rpcServer(t *testing.T, results map[string]any, calls chan<- rpcCall) *solana.Client {
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
	return solana.NewClient(srv.URL)
}

func TestClientReadsChainState(t *testing.T) {
	t.Parallel()
	calls := make(chan rpcCall, 2)
	c := rpcServer(t, map[string]any{
		"getBalance": map[string]any{"context": map[string]any{"slot": 7}, "value": 2_500_000_000},
		"getSlot":    123,
	}, calls)

	bal, err := c.SOLBalance(t.Context(), systemProgram)
	require.NoError(t, err)
	require.Equal(t, uint64(2_500_000_000), bal)
	call := <-calls
	require.Equal(t, "getBalance", call.Method)
	require.Equal(t, systemProgram, call.Params[0])

	slot, err := c.SlotNumber(t.Context())
	require.NoError(t, err)
	require.Equal(t, uint64(123), slot)
	require.Equal(t, "getSlot", (<-calls).Method)
}

func TestClientWrapsRPCErrors(t *testing.T) {
	t.Parallel()
	c := rpcServer(t, map[string]any{}, make(chan rpcCall, 2))

	_, err := c.SOLBalance(t.Context(), systemProgram)
	require.ErrorContains(t, err, "solana: get balance:")
	require.ErrorContains(t, err, "boom getBalance")
	_, err = c.SlotNumber(t.Context())
	require.ErrorContains(t, err, "solana: get slot:")
	require.ErrorContains(t, err, "boom getSlot")
}
