// Package auth handles Google OAuth for git-subsync: client configuration,
// the browser login flow, token storage and refresh. It has no CLI
// dependencies so future commands can reuse it.
package auth

import (
	"errors"
	"fmt"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// DriveScope grants full Drive access. Later milestones need to open existing
// folders by URL, which the narrower drive.file scope cannot do.
const DriveScope = "https://www.googleapis.com/auth/drive"

// Environment variables that override the build-time OAuth client.
const (
	EnvClientID     = "GIT_SUBSYNC_CLIENT_ID"
	EnvClientSecret = "GIT_SUBSYNC_CLIENT_SECRET"
)

// Injected at build time via -ldflags -X. Never commit real values.
var (
	buildClientID     string
	buildClientSecret string
)

// ErrNoClientConfig is returned when no OAuth client ID/secret is available.
var ErrNoClientConfig = errors.New("no OAuth client configured; set " + EnvClientID + " and " + EnvClientSecret)

// ClientConfig identifies the Google OAuth client (a "Desktop app" client).
type ClientConfig struct {
	ID     string
	Secret string
}

// ResolveClientConfig returns the OAuth client from the environment if set,
// otherwise the values injected at build time.
func ResolveClientConfig(getenv func(string) string) (ClientConfig, error) {
	return resolveClientConfig(getenv, buildClientID, buildClientSecret)
}

// resolveClientConfig treats ID and secret as a pair: if either env var is
// set, both must be, and values are never mixed across sources.
func resolveClientConfig(getenv func(string) string, buildID, buildSecret string) (ClientConfig, error) {
	id, secret := getenv(EnvClientID), getenv(EnvClientSecret)
	if id != "" || secret != "" {
		if id == "" || secret == "" {
			return ClientConfig{}, fmt.Errorf("both %s and %s must be set", EnvClientID, EnvClientSecret)
		}
		return ClientConfig{ID: id, Secret: secret}, nil
	}
	if buildID != "" && buildSecret != "" {
		return ClientConfig{ID: buildID, Secret: buildSecret}, nil
	}
	return ClientConfig{}, ErrNoClientConfig
}

// OAuthConfig returns an oauth2.Config for Google with the Drive scope.
// RedirectURL is left empty; Login sets it once the loopback port is known.
func OAuthConfig(cc ClientConfig) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cc.ID,
		ClientSecret: cc.Secret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{DriveScope},
	}
}
