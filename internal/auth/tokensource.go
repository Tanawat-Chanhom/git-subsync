package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/oauth2"
)

// TokenSource returns a token source for the saved token that writes
// refreshed tokens back to store. It returns ErrNotLoggedIn if no token is
// saved.
func TokenSource(ctx context.Context, conf *oauth2.Config, store *Store) (oauth2.TokenSource, error) {
	tok, err := store.Load()
	if err != nil {
		return nil, err
	}
	return newPersistingTokenSource(conf.TokenSource(ctx, tok), store, tok), nil
}

// persistingTokenSource saves a token to the store whenever the underlying
// source hands out a new access token (i.e. after a refresh).
type persistingTokenSource struct {
	base  oauth2.TokenSource
	store *Store

	mu   sync.Mutex
	last *oauth2.Token
}

func newPersistingTokenSource(base oauth2.TokenSource, store *Store, current *oauth2.Token) *persistingTokenSource {
	return &persistingTokenSource{base: base, store: store, last: current}
}

func (s *persistingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := s.base.Token()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last != nil && tok.AccessToken == s.last.AccessToken {
		return tok, nil
	}
	if tok.RefreshToken == "" && s.last != nil {
		// Refresh responses may omit the refresh token; keep the one we have.
		withRefresh := *tok
		withRefresh.RefreshToken = s.last.RefreshToken
		tok = &withRefresh
	}
	if err := s.store.Save(tok); err != nil {
		return nil, fmt.Errorf("saving refreshed token: %w", err)
	}
	s.last = tok
	return tok, nil
}

// IsSessionExpired reports whether err means the refresh token is no longer
// valid: it was revoked, or it expired (Google expires refresh tokens after
// 7 days while the OAuth app is in "Testing" mode).
func IsSessionExpired(err error) bool {
	var re *oauth2.RetrieveError
	return errors.As(err, &re) && re.ErrorCode == "invalid_grant"
}
