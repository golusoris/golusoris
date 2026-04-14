package apidocs

import (
	"encoding/json"
	"net/http/httptest"
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

func TestWriteRPCError(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	writeRPCError(w, json.RawMessage(`1`), -32600, "invalid")
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Error("expected non-empty response body")
	}
}
