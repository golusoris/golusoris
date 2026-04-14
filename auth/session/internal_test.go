package session

import (
	"net/http"
	"testing"
	"time"

	gerr "github.com/golusoris/golusoris/errors"
)

func TestWithDefaults_cookieName(t *testing.T) {
	t.Parallel()
	opts := Options{}.withDefaults()
	if opts.CookieName != defaultCookieName {
		t.Errorf("CookieName = %q, want %q", opts.CookieName, defaultCookieName)
	}
}

func TestWithDefaults_ttl(t *testing.T) {
	t.Parallel()
	opts := Options{}.withDefaults()
	if opts.TTL != 24*time.Hour {
		t.Errorf("TTL = %v, want 24h", opts.TTL)
	}
}

func TestWithDefaults_sameSite(t *testing.T) {
	t.Parallel()
	opts := Options{}.withDefaults()
	if opts.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want SameSiteLaxMode", opts.SameSite)
	}
}

func TestWithDefaults_path(t *testing.T) {
	t.Parallel()
	opts := Options{}.withDefaults()
	if opts.Path != "/" {
		t.Errorf("Path = %q, want \"/\"", opts.Path)
	}
}

func TestWithDefaults_preservesExisting(t *testing.T) {
	t.Parallel()
	opts := Options{CookieName: "mysid", TTL: 1 * time.Hour, Path: "/app"}.withDefaults()
	if opts.CookieName != "mysid" {
		t.Errorf("CookieName = %q, want \"mysid\"", opts.CookieName)
	}
	if opts.TTL != 1*time.Hour {
		t.Errorf("TTL = %v, want 1h", opts.TTL)
	}
	if opts.Path != "/app" {
		t.Errorf("Path = %q, want \"/app\"", opts.Path)
	}
}

func TestIsNotFound_trueForNotFoundError(t *testing.T) {
	t.Parallel()
	err := gerr.NotFound("session not found")
	if !isNotFound(err) {
		t.Error("isNotFound(gerr.NotFound(...)) = false, want true")
	}
}

func TestIsNotFound_falseForOtherErrors(t *testing.T) {
	t.Parallel()
	err := gerr.BadRequest("bad input")
	if isNotFound(err) {
		t.Error("isNotFound(gerr.BadRequest(...)) = true, want false")
	}
}

func TestIsNotFound_falseForNil(t *testing.T) {
	t.Parallel()
	if isNotFound(nil) {
		t.Error("isNotFound(nil) = true, want false")
	}
}
