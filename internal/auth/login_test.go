package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func serveCallback(h http.Handler, rawQuery string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, callbackPath+"?"+rawQuery, nil))
	return rec
}

func TestCallbackHandler(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantCode   string
		wantErr    string
	}{
		{"valid", "state=good&code=abc", http.StatusOK, "abc", ""},
		{"state mismatch", "state=evil&code=abc", http.StatusBadRequest, "", "invalid state"},
		{"missing state", "code=abc", http.StatusBadRequest, "", "invalid state"},
		{"access denied", "state=good&error=access_denied", http.StatusBadRequest, "", "denied"},
		{"other error", "state=good&error=server_error", http.StatusBadRequest, "", "server_error"},
		{"missing code", "state=good", http.StatusBadRequest, "", "missing the authorization code"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCallbackHandler("good")
			rec := serveCallback(h, tt.query)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			res := <-h.result
			if tt.wantErr == "" {
				if res.err != nil || res.code != tt.wantCode {
					t.Fatalf("result = %+v, want code %q", res, tt.wantCode)
				}
				return
			}
			if res.err == nil || !strings.Contains(res.err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", res.err, tt.wantErr)
			}
		})
	}
}

func TestCallbackHandlerOnlyFirstRequestCounts(t *testing.T) {
	h := newCallbackHandler("good")
	if rec := serveCallback(h, "state=good&code=first"); rec.Code != http.StatusOK {
		t.Fatalf("first status = %d", rec.Code)
	}
	if rec := serveCallback(h, "state=good&code=second"); rec.Code != http.StatusBadRequest {
		t.Fatalf("second status = %d, want 400", rec.Code)
	}
	if res := <-h.result; res.code != "first" {
		t.Fatalf("code = %q, want first", res.code)
	}
	select {
	case res := <-h.result:
		t.Fatalf("unexpected second result %+v", res)
	default:
	}
}

func TestCallbackHandlerIgnoresOtherPaths(t *testing.T) {
	h := newCallbackHandler("good")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/favicon.ico", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	select {
	case res := <-h.result:
		t.Fatalf("unexpected result %+v", res)
	default:
	}
}

// TestLoginFlow drives the whole flow offline: a fake "browser" follows the
// auth URL straight to the loopback callback, and a fake token endpoint
// checks the PKCE exchange.
func TestLoginFlow(t *testing.T) {
	var (
		mu        sync.Mutex
		challenge string
		redirect  string
	)
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		mu.Lock()
		wantChallenge, wantRedirect := challenge, redirect
		mu.Unlock()
		if got := r.PostForm.Get("grant_type"); got != "authorization_code" {
			t.Errorf("grant_type = %q", got)
		}
		if got := r.PostForm.Get("code"); got != "the-code" {
			t.Errorf("code = %q", got)
		}
		if got := r.PostForm.Get("redirect_uri"); got != wantRedirect {
			t.Errorf("redirect_uri = %q, want %q", got, wantRedirect)
		}
		verifier := r.PostForm.Get("code_verifier")
		if verifier == "" || oauth2.S256ChallengeFromVerifier(verifier) != wantChallenge {
			t.Errorf("code_verifier %q does not match challenge %q", verifier, wantChallenge)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"at","token_type":"Bearer","refresh_token":"rt","expires_in":3600}`)
	}))
	defer tokenSrv.Close()

	conf := &oauth2.Config{
		ClientID:     "id",
		ClientSecret: "secret",
		Endpoint:     oauth2.Endpoint{AuthURL: "https://auth.example/auth", TokenURL: tokenSrv.URL},
		Scopes:       []string{DriveScope},
	}

	browser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		for k, want := range map[string]string{
			"client_id":             "id",
			"response_type":         "code",
			"access_type":           "offline",
			"prompt":                "consent",
			"code_challenge_method": "S256",
			"scope":                 DriveScope,
		} {
			if got := q.Get(k); got != want {
				t.Errorf("auth URL %s = %q, want %q", k, got, want)
			}
		}
		redir := q.Get("redirect_uri")
		if !strings.HasPrefix(redir, "http://127.0.0.1:") || !strings.HasSuffix(redir, callbackPath) {
			t.Errorf("redirect_uri = %q", redir)
		}
		mu.Lock()
		challenge, redirect = q.Get("code_challenge"), redir
		mu.Unlock()

		resp, err := http.Get(redir + "?" + url.Values{"state": {q.Get("state")}, "code": {"the-code"}}.Encode())
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Login successful") {
			t.Errorf("callback response %d: %s", resp.StatusCode, body)
		}
		return nil
	}

	var out strings.Builder
	tok, err := Login(context.Background(), conf, LoginOptions{Out: &out, OpenBrowser: browser, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" {
		t.Fatalf("token = %+v", tok)
	}
	if conf.RedirectURL != "" {
		t.Errorf("Login modified the caller's config: RedirectURL = %q", conf.RedirectURL)
	}
	if !strings.Contains(out.String(), "https://auth.example/auth?") {
		t.Errorf("output does not include the auth URL:\n%s", out.String())
	}
}

func TestLoginTimeout(t *testing.T) {
	conf := &oauth2.Config{Endpoint: oauth2.Endpoint{AuthURL: "https://auth.example/auth"}}
	_, err := Login(context.Background(), conf, LoginOptions{Timeout: 50 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
}

func TestLoginCancelled(t *testing.T) {
	conf := &oauth2.Config{Endpoint: oauth2.Endpoint{AuthURL: "https://auth.example/auth"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Login(ctx, conf, LoginOptions{Timeout: 10 * time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
