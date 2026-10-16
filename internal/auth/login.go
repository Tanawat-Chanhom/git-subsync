package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// DefaultLoginTimeout bounds how long Login waits for the browser callback.
const DefaultLoginTimeout = 3 * time.Minute

const callbackPath = "/callback"

// LoginOptions configures Login.
type LoginOptions struct {
	// Out receives the authorization URL and status messages.
	Out io.Writer
	// OpenBrowser opens the authorization URL. If nil, the URL is only printed.
	OpenBrowser func(url string) error
	// Timeout bounds the wait for the browser callback. Zero means DefaultLoginTimeout.
	Timeout time.Duration
}

// Login runs the OAuth authorization-code flow with PKCE, using a loopback
// redirect on 127.0.0.1, and returns the resulting token. conf is not modified.
func Login(ctx context.Context, conf *oauth2.Config, opts LoginOptions) (*oauth2.Token, error) {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultLoginTimeout
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("starting local callback server: %w", err)
	}
	c := *conf
	c.RedirectURL = "http://" + ln.Addr().String() + callbackPath

	state := rand.Text()
	verifier := oauth2.GenerateVerifier()
	authURL := c.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.S256ChallengeOption(verifier),
	)

	h := newCallbackHandler(state)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln) // returns ErrServerClosed after Shutdown
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx) // best effort
	}()

	if opts.OpenBrowser != nil {
		fmt.Fprintf(out, "Opening your browser to sign in to Google.\nIf it doesn't open, visit this URL:\n\n  %s\n\n", authURL)
		if err := opts.OpenBrowser(authURL); err != nil {
			fmt.Fprintf(out, "Could not open a browser (%v). Open the URL above manually.\n", err)
		}
	} else {
		fmt.Fprintf(out, "Visit this URL to sign in to Google:\n\n  %s\n\n", authURL)
	}
	fmt.Fprintln(out, "Waiting for authorization...")

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var res callbackResult
	select {
	case res = <-h.result:
	case <-waitCtx.Done():
		if ctx.Err() != nil {
			return nil, fmt.Errorf("login cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("timed out after %s waiting for browser login", timeout)
	}
	if res.err != nil {
		return nil, res.err
	}

	exchangeCtx, cancelExchange := context.WithTimeout(ctx, 30*time.Second)
	defer cancelExchange()
	tok, err := c.Exchange(exchangeCtx, res.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("exchanging authorization code: %w", err)
	}
	return tok, nil
}

type callbackResult struct {
	code string
	err  error
}

// callbackHandler accepts exactly one request on callbackPath, validates it,
// and reports the outcome on result.
type callbackHandler struct {
	state  string
	once   sync.Once
	result chan callbackResult
}

func newCallbackHandler(state string) *callbackHandler {
	return &callbackHandler{state: state, result: make(chan callbackResult, 1)}
}

func (h *callbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != callbackPath {
		http.NotFound(w, r)
		return
	}
	handled := false
	h.once.Do(func() {
		handled = true
		res := h.parse(r)
		if res.err != nil {
			writePage(w, http.StatusBadRequest, "Login failed", res.err.Error()+". Return to the terminal and try again.")
		} else {
			writePage(w, http.StatusOK, "Login successful", "You can close this tab and return to the terminal.")
		}
		h.result <- res
	})
	if !handled {
		writePage(w, http.StatusBadRequest, "Login already handled", "Return to the terminal.")
	}
}

func (h *callbackHandler) parse(r *http.Request) callbackResult {
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(h.state)) != 1 {
		return callbackResult{err: errors.New("invalid state in OAuth callback")}
	}
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			return callbackResult{err: errors.New("access was denied in the browser")}
		}
		return callbackResult{err: fmt.Errorf("authorization failed: %s", e)}
	}
	code := q.Get("code")
	if code == "" {
		return callbackResult{err: errors.New("OAuth callback is missing the authorization code")}
	}
	return callbackResult{code: code}
}

const pageHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>git-subsync: %s</title></head>
<body style="font-family: system-ui, sans-serif; margin: 4em auto; max-width: 32em; text-align: center">
<h1>%s</h1><p>%s</p>
</body></html>
`

func writePage(w http.ResponseWriter, status int, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	t := html.EscapeString(title)
	fmt.Fprintf(w, pageHTML, t, t, html.EscapeString(msg))
}

// OpenBrowser opens url in the user's default browser.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("opening a browser is not supported on %s", runtime.GOOS)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap the child process
	return nil
}
