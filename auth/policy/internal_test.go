package policy

import (
	"testing"
)

func TestWithDefaults_minLength(t *testing.T) {
	t.Parallel()
	opts := Options{}.withDefaults()
	if opts.MinLength != 12 {
		t.Errorf("MinLength = %d, want 12", opts.MinLength)
	}
}

func TestWithDefaults_minScore(t *testing.T) {
	t.Parallel()
	opts := Options{}.withDefaults()
	if opts.MinScore != 3 {
		t.Errorf("MinScore = %d, want 3", opts.MinScore)
	}
}

func TestWithDefaults_httpClientNonNil(t *testing.T) {
	t.Parallel()
	opts := Options{}.withDefaults()
	if opts.HTTPClient == nil {
		t.Error("HTTPClient = nil, want non-nil default")
	}
}

func TestWithDefaults_preservesExisting(t *testing.T) {
	t.Parallel()
	opts := Options{MinLength: 20, MinScore: 4}.withDefaults()
	if opts.MinLength != 20 {
		t.Errorf("MinLength = %d, want 20", opts.MinLength)
	}
	if opts.MinScore != 4 {
		t.Errorf("MinScore = %d, want 4", opts.MinScore)
	}
}

func TestScore_weakPassword(t *testing.T) {
	t.Parallel()
	p := New(Options{})
	score := p.Score("password")
	if score > 1 {
		t.Errorf("Score(%q) = %d, want ≤1 (weak)", "password", score)
	}
}

func TestScore_strongPassword(t *testing.T) {
	t.Parallel()
	p := New(Options{})
	score := p.Score("correct-horse-battery-staple-42!")
	if score < 3 {
		t.Errorf("Score(%q) = %d, want ≥3 (strong)", "correct-horse-battery-staple-42!", score)
	}
}
