// Package gdrive is a thin wrapper around the Google Drive v3 API. It has no
// CLI dependencies so future commands can reuse it.
package gdrive

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// NewService returns a Drive client authorized by ts. Extra options are
// mainly for tests (e.g. option.WithEndpoint).
func NewService(ctx context.Context, ts oauth2.TokenSource, opts ...option.ClientOption) (*drive.Service, error) {
	opts = append([]option.ClientOption{option.WithTokenSource(ts)}, opts...)
	svc, err := drive.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating Drive client: %w", err)
	}
	return svc, nil
}

// User is the Google account a Drive client acts as.
type User struct {
	DisplayName string
	Email       string
}

// WhoAmI returns the authenticated user, via Drive's about.get.
func WhoAmI(ctx context.Context, svc *drive.Service) (User, error) {
	about, err := svc.About.Get().Fields("user(displayName,emailAddress)").Context(ctx).Do()
	if err != nil {
		return User{}, fmt.Errorf("getting Drive account info: %w", err)
	}
	if about.User == nil {
		return User{}, errors.New("getting Drive account info: no user in response")
	}
	return User{DisplayName: about.User.DisplayName, Email: about.User.EmailAddress}, nil
}
