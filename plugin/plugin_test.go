// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package plugin_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/plugin"
)

type greeter interface{ Greet() string }

type helloGreeter struct{}

func (h helloGreeter) Greet() string { return "hello" }

type hiGreeter struct{}

func (h hiGreeter) Greet() string { return "hi" }

func TestRegister_and_Get(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	require.NoError(t, r.Register("hello", helloGreeter{}))

	g, ok := r.Get("hello")
	require.True(t, ok)
	require.Equal(t, "hello", g.Greet())
}

func TestGet_missing(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	_, ok := r.Get("nope")
	require.False(t, ok)
}

func TestRegister_duplicateErrors(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	require.NoError(t, r.Register("dup", helloGreeter{}))
	require.ErrorIs(t, r.Register("dup", helloGreeter{}), plugin.ErrDuplicate)
}

func TestMustRegister_replaces(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	r.MustRegister("g", helloGreeter{})
	r.MustRegister("g", hiGreeter{}) // replace — should not panic
	g, _ := r.Get("g")
	require.Equal(t, "hi", g.Greet())
}

func TestLookup(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	_, err := r.Lookup("missing")
	require.ErrorIs(t, err, plugin.ErrNotRegistered)
	require.NoError(t, r.Register("hello", helloGreeter{}))
	g, err := r.Lookup("hello")
	require.NoError(t, err)
	require.Equal(t, "hello", g.Greet())
}

func TestKeys(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	require.NoError(t, r.Register("a", helloGreeter{}))
	require.NoError(t, r.Register("b", hiGreeter{}))
	require.ElementsMatch(t, []string{"a", "b"}, r.Keys())
}

func TestAll(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	require.NoError(t, r.Register("hello", helloGreeter{}))
	require.NoError(t, r.Register("hi", hiGreeter{}))
	all := r.All()
	require.Len(t, all, 2)
	require.Equal(t, "hello", all["hello"].Greet())
}

func TestLen(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	require.Equal(t, 0, r.Len())
	require.NoError(t, r.Register("a", helloGreeter{}))
	require.Equal(t, 1, r.Len())
}

func TestEntries(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	require.NoError(t, r.Register("hello", helloGreeter{}))
	entries := r.Entries()
	require.Len(t, entries, 1)
	require.Equal(t, "hello", entries[0].Key)
	require.Equal(t, "hello", entries[0].Impl.Greet())
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.concurrent")
	require.NoError(t, r.Register("base", helloGreeter{}))

	done := make(chan struct{})
	for range 50 {
		go func() {
			_, _ = r.Get("base")
			_ = r.Keys()
			_ = r.Len()
			done <- struct{}{}
		}()
	}
	for range 50 {
		<-done
	}
}
