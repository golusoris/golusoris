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
	r.Register("hello", helloGreeter{})

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

func TestRegister_duplicatePanics(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	r.Register("dup", helloGreeter{})
	require.Panics(t, func() { r.Register("dup", helloGreeter{}) })
}

func TestMustRegister_replaces(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	r.MustRegister("g", helloGreeter{})
	r.MustRegister("g", hiGreeter{}) // replace — should not panic
	g, _ := r.Get("g")
	require.Equal(t, "hi", g.Greet())
}

func TestMustGet_panicsOnMissing(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	require.Panics(t, func() { r.MustGet("missing") })
}

func TestKeys(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	r.Register("a", helloGreeter{})
	r.Register("b", hiGreeter{})
	require.ElementsMatch(t, []string{"a", "b"}, r.Keys())
}

func TestAll(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	r.Register("hello", helloGreeter{})
	r.Register("hi", hiGreeter{})
	all := r.All()
	require.Len(t, all, 2)
	require.Equal(t, "hello", all["hello"].Greet())
}

func TestLen(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	require.Equal(t, 0, r.Len())
	r.Register("a", helloGreeter{})
	require.Equal(t, 1, r.Len())
}

func TestEntries(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.greeters")
	r.Register("hello", helloGreeter{})
	entries := r.Entries()
	require.Len(t, entries, 1)
	require.Equal(t, "hello", entries[0].Key)
	require.Equal(t, "hello", entries[0].Impl.Greet())
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()
	r := plugin.New[greeter]("test.concurrent")
	r.Register("base", helloGreeter{})

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
