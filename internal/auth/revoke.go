package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
)

// revokeURL is Google's token revocation endpoint. It is a variable for tests.
var revokeURL = "https://oauth2.googleapis.com/revoke"

// Revoke asks Google to revoke tok. It prefers the refresh token, since
// revoking it also invalidates the access tokens issued from it.
func Revoke(ctx context.Context, tok *oauth2.Token) error {
	value := tok.RefreshToken
	if value == "" {
		value = tok.AccessToken
	}
	if value == "" {
		return errors.New("no token to revoke")
	}

	form := url.Values{"token": {value}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, strings.NewReader(form))
	if err != nil {
		return fmt.Errorf("revoking token: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoking token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("revoking token: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
