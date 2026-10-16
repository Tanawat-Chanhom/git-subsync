package gdrive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
	"google.golang.org/api/option"

	"github.com/Tanawat-Chanhom/git-subsync/internal/auth"
)

type errSource struct{ err error }

func (s errSource) Token() (*oauth2.Token, error) { return nil, s.err }

func fakeDrive(t *testing.T, handler http.HandlerFunc) option.ClientOption {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return option.WithEndpoint(srv.URL + "/")
}

func TestWhoAmI(t *testing.T) {
	endpoint := fakeDrive(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/about") {
			t.Errorf("path = %s, want .../about", r.URL.Path)
		}
		if got := r.URL.Query().Get("fields"); got != "user(displayName,emailAddress)" {
			t.Errorf("fields = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer at" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"user":{"displayName":"Ada Lovelace","emailAddress":"ada@example.com"}}`)
	})

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "at"})
	svc, err := NewService(context.Background(), ts, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	user, err := WhoAmI(context.Background(), svc)
	if err != nil {
		t.Fatal(err)
	}
	if want := (User{DisplayName: "Ada Lovelace", Email: "ada@example.com"}); user != want {
		t.Fatalf("user = %+v, want %+v", user, want)
	}
}

// An invalid_grant from the token source must survive the Drive client's
// error wrapping so the CLI can print the "session expired" hint.
func TestWhoAmISessionExpired(t *testing.T) {
	endpoint := fakeDrive(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("Drive should not be called without a token")
	})
	ts := errSource{&oauth2.RetrieveError{ErrorCode: "invalid_grant"}}
	svc, err := NewService(context.Background(), ts, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_, err = WhoAmI(context.Background(), svc)
	if !auth.IsSessionExpired(err) {
		t.Fatalf("IsSessionExpired(%v) = false, want true", err)
	}
}
