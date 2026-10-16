package auth

import (
	"errors"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveClientConfig(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		buildID     string
		buildSecret string
		want        ClientConfig
		wantErr     bool
		wantErrIs   error
	}{
		{
			name: "env pair",
			env:  map[string]string{EnvClientID: "env-id", EnvClientSecret: "env-secret"},
			want: ClientConfig{ID: "env-id", Secret: "env-secret"},
		},
		{
			name:        "env pair wins over build values",
			env:         map[string]string{EnvClientID: "env-id", EnvClientSecret: "env-secret"},
			buildID:     "build-id",
			buildSecret: "build-secret",
			want:        ClientConfig{ID: "env-id", Secret: "env-secret"},
		},
		{
			name:        "build values when env unset",
			buildID:     "build-id",
			buildSecret: "build-secret",
			want:        ClientConfig{ID: "build-id", Secret: "build-secret"},
		},
		{
			name:        "env id without secret is an error, not mixed with build",
			env:         map[string]string{EnvClientID: "env-id"},
			buildID:     "build-id",
			buildSecret: "build-secret",
			wantErr:     true,
		},
		{
			name:        "env secret without id is an error",
			env:         map[string]string{EnvClientSecret: "env-secret"},
			buildID:     "build-id",
			buildSecret: "build-secret",
			wantErr:     true,
		},
		{
			name:      "nothing configured",
			wantErr:   true,
			wantErrIs: ErrNoClientConfig,
		},
		{
			name:      "build id without build secret",
			buildID:   "build-id",
			wantErr:   true,
			wantErrIs: ErrNoClientConfig,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveClientConfig(envMap(tt.env), tt.buildID, tt.buildSecret)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("got %+v, want error", got)
				}
				if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("err = %v, want %v", err, tt.wantErrIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestOAuthConfig(t *testing.T) {
	conf := OAuthConfig(ClientConfig{ID: "id", Secret: "secret"})
	if conf.ClientID != "id" || conf.ClientSecret != "secret" {
		t.Fatalf("client = %q/%q", conf.ClientID, conf.ClientSecret)
	}
	if len(conf.Scopes) != 1 || conf.Scopes[0] != DriveScope {
		t.Fatalf("scopes = %v, want [%s]", conf.Scopes, DriveScope)
	}
}
