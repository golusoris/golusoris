package apidocs

import (
	"testing"
)

func TestSanitizePath(t *testing.T) {
	t.Parallel()
	got := sanitizePath("/users/{id}/posts")
	want := "_users_id_posts"
	if got != want {
		t.Errorf("sanitizePath = %q, want %q", got, want)
	}
}

func TestSafeResolveURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		base    string
		path    string
		want    string
		wantErr bool
	}{
		{"simple join", "http://api.test", "/echo/abc", "http://api.test/echo/abc", false},
		{"query preserved", "https://api.test", "/x?q=1", "https://api.test/x?q=1", false},
		{"scheme-relative escapes origin", "http://api.test", "//evil.test/x", "", true},
		{"invalid base", "not-a-url", "/x", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := safeResolveURL(tt.base, tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("safeResolveURL err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("safeResolveURL = %q, want %q", got, tt.want)
			}
		})
	}
}
