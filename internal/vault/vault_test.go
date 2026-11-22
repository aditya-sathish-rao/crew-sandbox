package vault

import (
	"context"
	"errors"
	"testing"
)

func TestStaticGet(t *testing.T) {
	s := Static{"TOKEN": "abc"}
	got, err := s.Get(context.Background(), "TOKEN")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abc" {
		t.Fatalf("got %q, want %q", got, "abc")
	}
}

func TestStaticMissing(t *testing.T) {
	_, err := Static{}.Get(context.Background(), "TOKEN")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestChainFallsThrough(t *testing.T) {
	c := Chain(Static{}, Static{"TOKEN": "second"})
	got, err := c.Get(context.Background(), "TOKEN")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "second" {
		t.Fatalf("got %q, want %q", got, "second")
	}
}

func TestChainStopsOnRealError(t *testing.T) {
	broken := errProvider{err: errors.New("vault is down")}
	c := Chain(broken, Static{"TOKEN": "second"})
	_, err := c.Get(context.Background(), "TOKEN")
	if err == nil {
		t.Fatal("expected the chain to stop on a real error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("a real error must not be reported as not-found")
	}
}

func TestChainAllMissing(t *testing.T) {
	c := Chain(Static{}, Static{})
	_, err := c.Get(context.Background(), "TOKEN")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

type errProvider struct{ err error }

func (e errProvider) Get(context.Context, string) (string, error) { return "", e.err }
