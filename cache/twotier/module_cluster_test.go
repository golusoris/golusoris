// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package twotier

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/stretchr/testify/require"
)

type prefixClient struct {
	rueidis.Client
	nodes      map[string]rueidis.Client
	pages      []rueidis.RedisResult
	scans      [][]string
	unlinks    []string
	unlinkErrs map[string]error
}

func (c *prefixClient) Nodes() map[string]rueidis.Client { return c.nodes }

func (c *prefixClient) Do(_ context.Context, cmd rueidis.Completed) rueidis.RedisResult {
	if args := cmd.Commands(); args[0] == "UNLINK" && len(args) > 2 {
		return rueidis.NewErrorResult(errors.New("CROSSSLOT keys in request do not hash to the same slot"))
	}
	c.scans = append(c.scans, append([]string(nil), cmd.Commands()...))
	if len(c.pages) == 0 {
		return rueidis.NewErrorResult(errors.New("unexpected node command"))
	}
	result := c.pages[0]
	c.pages = c.pages[1:]
	return result
}

func (c *prefixClient) DoMulti(_ context.Context, commands ...rueidis.Completed) []rueidis.RedisResult {
	results := make([]rueidis.RedisResult, len(commands))
	for i, cmd := range commands {
		args := cmd.Commands()
		if len(args) != 2 || args[0] != "UNLINK" {
			results[i] = rueidis.NewErrorResult(errors.New("expected single-key UNLINK"))
			continue
		}
		c.unlinks = append(c.unlinks, args[1])
		results[i] = rueidis.NewErrorResult(c.unlinkErrs[args[1]])
	}
	return results
}

// handlePipeExchanges serves HELLO and PING exchanges over the mock pipe.
func handlePipeExchanges(t *testing.T, serverConn net.Conn, response string) {
	t.Helper()
	defer serverConn.Close()
	if deadlineErr := serverConn.SetDeadline(time.Now().Add(5 * time.Second)); deadlineErr != nil {
		t.Error(deadlineErr)
		return
	}
	for _, exchange := range [][2]string{
		{"*2\r\n$5\r\nHELLO\r\n$1\r\n2\r\n", "*0\r\n"},
		{"*1\r\n$4\r\nPING\r\n", response},
	} {
		request := make([]byte, len(exchange[0]))
		if _, readErr := io.ReadFull(serverConn, request); readErr != nil {
			t.Error(readErr)
			return
		}
		if string(request) != exchange[0] {
			t.Errorf("request = %q, want %q", request, exchange[0])
			return
		}
		if _, writeErr := io.WriteString(serverConn, exchange[1]); writeErr != nil {
			t.Error(writeErr)
			return
		}
	}
}

// Decode through the pinned client because RedisMessage has no exported value constructors.
func prefixResult(t *testing.T, response string) (rueidis.Client, rueidis.RedisResult) {
	t.Helper()
	client, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress: []string{"fake:6379"}, ForceSingleClient: true,
		DisableCache: true, AlwaysRESP2: true, DisableRetry: true,
		ClientSetInfo: rueidis.DisableClientSetInfo,
		DialFn: func(string, *net.Dialer, *tls.Config) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			go handlePipeExchanges(t, serverConn, response)
			return clientConn, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := client.Do(ctx, client.B().Ping().Build())
	require.NoError(t, result.Error())
	return client, result
}

func prefixPage(cursor uint64, keys ...string) string {
	var response strings.Builder
	c := strconv.FormatUint(cursor, 10)
	fmt.Fprintf(&response, "*2\r\n$%d\r\n%s\r\n*%d\r\n", len(c), c, len(keys))
	for _, key := range keys {
		fmt.Fprintf(&response, "$%d\r\n%s\r\n", len(key), key)
	}
	return response.String()
}

func TestRedisL2DelPrefixClusterNodesAndSlots(t *testing.T) {
	t.Parallel()
	builder, first := prefixResult(t, prefixPage(7, "victim:{one}:1", "victim:{two}:2"))
	_, last := prefixResult(t, prefixPage(0, "victim:{one}:1"))
	_, other := prefixResult(t, prefixPage(0, "victim:{three}:3"))
	nodeA := &prefixClient{Client: builder, pages: []rueidis.RedisResult{first, last}}
	nodeB := &prefixClient{Client: builder, pages: []rueidis.RedisResult{other}}
	replica := &prefixClient{Client: builder, pages: []rueidis.RedisResult{first, last}}
	client := &prefixClient{Client: builder, nodes: map[string]rueidis.Client{
		"primary-a": nodeA, "primary-b": nodeB, "replica-a": replica,
	}}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, (redisL2{client: client}).DelPrefix(ctx, "victim:"))
	require.Empty(t, client.scans, "SCAN must target individual nodes")
	require.Equal(t, [][]string{
		{"SCAN", "0", "MATCH", "victim:*", "COUNT", "256"},
		{"SCAN", "7", "MATCH", "victim:*", "COUNT", "256"},
	}, nodeA.scans)
	require.Equal(t, nodeA.scans, replica.scans)
	require.Equal(t, [][]string{{"SCAN", "0", "MATCH", "victim:*", "COUNT", "256"}}, nodeB.scans)
	require.ElementsMatch(t, []string{"victim:{one}:1", "victim:{two}:2", "victim:{three}:3"}, client.unlinks)
}

func TestRedisL2DelPrefixClusterSingleKeyUnlink(t *testing.T) {
	t.Parallel()
	builder, page := prefixResult(t, prefixPage(0, "victim:{one}:1", "victim:{two}:2"))
	node := &prefixClient{Client: builder, pages: []rueidis.RedisResult{page}}
	client := &prefixClient{
		Client: builder, pages: []rueidis.RedisResult{page},
		nodes: map[string]rueidis.Client{"primary": node},
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, (redisL2{client: client}).DelPrefix(ctx, "victim:"))
	require.ElementsMatch(t, []string{"victim:{one}:1", "victim:{two}:2"}, client.unlinks)
}

func TestRedisL2DelPrefixClusterErrors(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"scan", "unlink"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			builder, page := prefixResult(t, prefixPage(0, "victim:{one}:1", "victim:{two}:2"))
			firstErr, lastErr := errors.New("first failure"), errors.New("last failure")
			node := &prefixClient{Client: builder, pages: []rueidis.RedisResult{page}}
			client := &prefixClient{Client: builder, nodes: map[string]rueidis.Client{"node": node}}
			if operation == "scan" {
				node.pages[0] = rueidis.NewErrorResult(firstErr)
			} else {
				client.unlinkErrs = map[string]error{"victim:{one}:1": firstErr, "victim:{two}:2": lastErr}
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err := (redisL2{client: client}).DelPrefix(ctx, "victim:")
			require.ErrorIs(t, err, firstErr)
			require.ErrorContains(t, err, "cache/twotier: redis "+operation)
			if operation == "unlink" {
				require.ErrorIs(t, err, lastErr)
			} else {
				require.Empty(t, client.unlinks)
			}
		})
	}
}

func TestRedisL2DelPrefixClusterEmpty(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"empty node", "no nodes", "empty prefix"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			builder, page := prefixResult(t, prefixPage(0))
			node := &prefixClient{Client: builder, pages: []rueidis.RedisResult{page}}
			client := &prefixClient{Client: builder}
			if boundary != "no nodes" {
				client.nodes = map[string]rueidis.Client{"empty": node}
			}
			prefix := "victim:"
			if boundary == "empty prefix" {
				prefix = ""
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err := (redisL2{client: client}).DelPrefix(ctx, prefix)
			if boundary == "empty prefix" {
				require.ErrorContains(t, err, "empty prefix")
				require.Empty(t, node.scans)
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, client.scans)
			require.Empty(t, client.unlinks)
		})
	}
}
