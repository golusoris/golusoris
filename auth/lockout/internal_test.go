package lockout

import (
	"testing"
	"time"
)

func TestWithDefaults_zero(t *testing.T) {
	t.Parallel()
	o := Options{}.withDefaults()
	if o.MaxFails != 5 {
		t.Errorf("MaxFails = %d, want 5", o.MaxFails)
	}
	if o.Window != 15*time.Minute {
		t.Errorf("Window = %v, want 15m", o.Window)
	}
	if o.Cooldown != 30*time.Minute {
		t.Errorf("Cooldown = %v, want 30m", o.Cooldown)
	}
}

func TestWithDefaults_preserves(t *testing.T) {
	t.Parallel()
	o := Options{MaxFails: 10, Window: time.Hour, Cooldown: 2 * time.Hour}.withDefaults()
	if o.MaxFails != 10 {
		t.Errorf("MaxFails = %d, want 10", o.MaxFails)
	}
	if o.Window != time.Hour {
		t.Errorf("Window = %v, want 1h", o.Window)
	}
	if o.Cooldown != 2*time.Hour {
		t.Errorf("Cooldown = %v, want 2h", o.Cooldown)
	}
}
