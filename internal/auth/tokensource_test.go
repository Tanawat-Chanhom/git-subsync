package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type staticSource struct {
	tok *oauth2.Token
	err error
}

func (s *staticSource) Token() (*oauth2.Token, error) { return s.tok, s.err }

func TestPersistingTokenSourceSavesOnlyNewTokens(t *testing.T) {
	store := NewStore(t.TempDir())
	current := &oauth2.Token{AccessToken: "a1", RefreshToken: "r1"}
	base := &staticSource{tok: current}
	ts := newPersistingTokenSource(base, store, current)

	// Unchanged access token: nothing is written.
	if _, err := ts.Token(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("token was saved although it did not change (err = %v)", err)
	}

	// Refreshed token without a refresh token: saved, old refresh token kept.
	base.tok = &oauth2.Token{AccessToken: "a2"}
	got, err := ts.Token()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "a2" || got.RefreshToken != "r1" {
		t.Fatalf("Token() = %+v, want a2/r1", got)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "a2" || saved.RefreshToken != "r1" {
		t.Fatalf("saved = %+v, want a2/r1", saved)
	}

	// Same token again: not re-saved.
	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Token(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("token was re-saved although it did not change (err = %v)", err)
	}
}

func TestPersistingTokenSourcePassesErrorsThrough(t *testing.T) {
	want := &oauth2.RetrieveError{ErrorCode: "invalid_grant"}
	ts := newPersistingTokenSource(&staticSource{err: want}, NewStore(t.TempDir()), nil)
	if _, err := ts.Token(); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestTokenSourceNotLoggedIn(t *testing.T) {
	_, err := TokenSource(context.Background(), &oauth2.Config{}, NewStore(t.TempDir()))
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("err = %v, want ErrNotLoggedIn", err)
	}
}

// fakeTokenEndpoint answers refresh requests with status and body.
func fakeTokenEndpoint(t *testing.T, status int, body string) *oauth2.Config {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return &oauth2.Config{ClientID: "id", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: srv.URL}}
}

func saveExpired(t *testing.T, store *Store) {
	t.Helper()
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "r1", Expiry: time.Now().Add(-time.Hour)}
	if err := store.Save(expired); err != nil {
		t.Fatal(err)
	}
}

func TestTokenSourceRefreshIsWrittenBack(t *testing.T) {
	conf := fakeTokenEndpoint(t, http.StatusOK, `{"access_token":"new","token_type":"Bearer","expires_in":3600}`)
	store := NewStore(t.TempDir())
	saveExpired(t, store)

	ts, err := TokenSource(context.Background(), conf, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Token(); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "new" || saved.RefreshToken != "r1" {
		t.Fatalf("saved = %+v, want new/r1", saved)
	}
}

func TestTokenSourceInvalidGrantIsSessionExpired(t *testing.T) {
	conf := fakeTokenEndpoint(t, http.StatusBadRequest, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
	store := NewStore(t.TempDir())
	saveExpired(t, store)

	ts, err := TokenSource(context.Background(), conf, store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ts.Token()
	if !IsSessionExpired(err) {
		t.Fatalf("IsSessionExpired(%v) = false, want true", err)
	}
}

func TestIsSessionExpired(t *testing.T) {
	invalidGrant := &oauth2.RetrieveError{ErrorCode: "invalid_grant"}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("boom"), false},
		{"invalid_grant", invalidGrant, true},
		{"wrapped in url.Error", &url.Error{Op: "Get", URL: "https://x", Err: invalidGrant}, true},
		{"wrapped with fmt", fmt.Errorf("calling drive: %w", invalidGrant), true},
		{"other oauth error", &oauth2.RetrieveError{ErrorCode: "invalid_client"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSessionExpired(tt.err); got != tt.want {
				t.Fatalf("IsSessionExpired = %v, want %v", got, tt.want)
			}
		})
	}
}
