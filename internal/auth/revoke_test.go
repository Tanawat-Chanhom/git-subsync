package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"
)

// fakeRevokeEndpoint points revokeURL at a test server that records the
// revoked token and replies with status.
func fakeRevokeEndpoint(t *testing.T, status int) <-chan string {
	t.Helper()
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		got <- r.PostForm.Get("token")
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)

	orig := revokeURL
	revokeURL = srv.URL
	t.Cleanup(func() { revokeURL = orig })
	return got
}

func TestRevokePrefersRefreshToken(t *testing.T) {
	got := fakeRevokeEndpoint(t, http.StatusOK)
	if err := Revoke(context.Background(), &oauth2.Token{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	if tok := <-got; tok != "r" {
		t.Fatalf("revoked %q, want refresh token", tok)
	}
}

func TestRevokeFallsBackToAccessToken(t *testing.T) {
	got := fakeRevokeEndpoint(t, http.StatusOK)
	if err := Revoke(context.Background(), &oauth2.Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	if tok := <-got; tok != "a" {
		t.Fatalf("revoked %q, want access token", tok)
	}
}

func TestRevokeErrorStatus(t *testing.T) {
	fakeRevokeEndpoint(t, http.StatusBadRequest)
	if err := Revoke(context.Background(), &oauth2.Token{RefreshToken: "r"}); err == nil {
		t.Fatal("want error for HTTP 400")
	}
}

func TestRevokeEmptyToken(t *testing.T) {
	if err := Revoke(context.Background(), &oauth2.Token{}); err == nil {
		t.Fatal("want error for empty token")
	}
}
