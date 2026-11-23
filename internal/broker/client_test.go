package broker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRevokeAll(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/revoke-all" {
				t.Errorf("got %s %s, want POST /revoke-all", r.Method, r.URL.Path)
			}
			_, _ = io.WriteString(w, `{"tasks":3,"revoked":2}`)
		}),
	)
	defer server.Close()

	got, err := RevokeAll(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tasks != 3 || got.Revoked != 2 {
		t.Fatalf("RevokeAll = %+v, want tasks=3 revoked=2", got)
	}
}

func TestRevokeAllReportsError(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "broker is broken", http.StatusInternalServerError)
		}),
	)
	defer server.Close()

	if _, err := RevokeAll(context.Background(), server.URL); err == nil {
		t.Fatal("RevokeAll succeeded on a 500 response")
	}
}
