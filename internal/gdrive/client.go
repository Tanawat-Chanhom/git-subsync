package gdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

// FolderMimeType is the MIME type of Drive folders.
const FolderMimeType = "application/vnd.google-apps.folder"

// ErrNoAccess means a file doesn't exist or the account can't see it.
// Drive answers 404 for both.
var ErrNoAccess = errors.New("not found or no access")

// File is a Drive file or folder.
type File struct {
	ID             string
	Name           string
	MimeType       string
	MD5            string // empty for folders and Google-native files
	Size           int64
	ModifiedTime   time.Time
	ModifiedBy     string // display name of the last person to change it
	Trashed        bool
	CanAddChildren bool // for folders: whether the account can add files
}

// IsFolder reports whether f is a folder.
func (f File) IsFolder() bool { return f.MimeType == FolderMimeType }

// Drive is the Drive access a sync needs. Client implements it with the
// Drive API; drivetest.Fake implements it in memory.
type Drive interface {
	// Get returns a file's metadata, or an error wrapping ErrNoAccess.
	Get(ctx context.Context, id string) (File, error)
	// List returns the folder's children that aren't in the trash.
	List(ctx context.Context, folderID string) ([]File, error)
	// Download returns a file's content.
	Download(ctx context.Context, id string) (io.ReadCloser, error)
	// Create uploads content as a new file named name in the parent folder.
	Create(ctx context.Context, parentID, name string, content io.ReadSeeker) (File, error)
	// Update replaces a file's content, keeping its ID.
	Update(ctx context.Context, id string, content io.ReadSeeker) (File, error)
	// Rename changes a file's name without touching its content.
	Rename(ctx context.Context, id, name string) (File, error)
	// CreateFolder creates a folder named name in the parent folder.
	CreateFolder(ctx context.Context, parentID, name string) (File, error)
}

const (
	fileFields      = "id,name,mimeType,md5Checksum,size,modifiedTime,lastModifyingUser(displayName),trashed,capabilities(canAddChildren)"
	uploadChunkSize = 8 << 20 // larger uploads are resumable
)

// Client is a Drive backed by the Drive API. It retries rate limits and
// server errors with exponential backoff, and supports shared drives.
type Client struct {
	svc      *drive.Service
	attempts int
	delay    time.Duration // before the first retry; doubles each time
}

var _ Drive = (*Client)(nil)

// NewClient returns a Client that uses svc.
func NewClient(svc *drive.Service) *Client {
	return &Client{svc: svc, attempts: 6, delay: time.Second}
}

// WhoAmI returns the account the client acts as.
func (c *Client) WhoAmI(ctx context.Context) (User, error) {
	var u User
	err := c.retry(ctx, nil, func() error {
		var err error
		u, err = WhoAmI(ctx, c.svc)
		return err
	})
	return u, err
}

func (c *Client) Get(ctx context.Context, id string) (File, error) {
	var f *drive.File
	err := c.retry(ctx, nil, func() error {
		var err error
		f, err = c.svc.Files.Get(id).Fields(fileFields).SupportsAllDrives(true).Context(ctx).Do()
		return err
	})
	if err != nil {
		if isNoAccess(err) {
			return File{}, fmt.Errorf("getting Drive file %s: %w: %w", id, ErrNoAccess, err)
		}
		return File{}, fmt.Errorf("getting Drive file %s: %w", id, err)
	}
	return fromAPI(f), nil
}

func (c *Client) List(ctx context.Context, folderID string) ([]File, error) {
	q := fmt.Sprintf("'%s' in parents and trashed = false", escapeQuery(folderID))
	var files []File
	page := ""
	for {
		var resp *drive.FileList
		err := c.retry(ctx, nil, func() error {
			call := c.svc.Files.List().Q(q).
				Fields(googleapi.Field("nextPageToken,files(" + fileFields + ")")).
				PageSize(1000).SupportsAllDrives(true).IncludeItemsFromAllDrives(true).Context(ctx)
			if page != "" {
				call = call.PageToken(page)
			}
			var err error
			resp, err = call.Do()
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("listing Drive folder %s: %w", folderID, err)
		}
		for _, f := range resp.Files {
			files = append(files, fromAPI(f))
		}
		if resp.NextPageToken == "" {
			return files, nil
		}
		page = resp.NextPageToken
	}
}

func (c *Client) Download(ctx context.Context, id string) (io.ReadCloser, error) {
	var resp *http.Response
	err := c.retry(ctx, nil, func() error {
		var err error
		resp, err = c.svc.Files.Get(id).SupportsAllDrives(true).Context(ctx).Download()
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("downloading Drive file %s: %w", id, err)
	}
	return resp.Body, nil
}

func (c *Client) Create(ctx context.Context, parentID, name string, content io.ReadSeeker) (File, error) {
	var f *drive.File
	err := c.retry(ctx, content, func() error {
		var err error
		f, err = c.svc.Files.Create(&drive.File{Name: name, Parents: []string{parentID}}).
			Media(content, mediaOptions()...).
			Fields(fileFields).SupportsAllDrives(true).Context(ctx).Do()
		return err
	})
	if err != nil {
		return File{}, fmt.Errorf("uploading %s: %w", name, err)
	}
	return fromAPI(f), nil
}

func (c *Client) Update(ctx context.Context, id string, content io.ReadSeeker) (File, error) {
	var f *drive.File
	err := c.retry(ctx, content, func() error {
		var err error
		f, err = c.svc.Files.Update(id, &drive.File{}).
			Media(content, mediaOptions()...).
			Fields(fileFields).SupportsAllDrives(true).Context(ctx).Do()
		return err
	})
	if err != nil {
		return File{}, fmt.Errorf("uploading Drive file %s: %w", id, err)
	}
	return fromAPI(f), nil
}

func (c *Client) Rename(ctx context.Context, id, name string) (File, error) {
	var f *drive.File
	err := c.retry(ctx, nil, func() error {
		var err error
		f, err = c.svc.Files.Update(id, &drive.File{Name: name}).
			Fields(fileFields).SupportsAllDrives(true).Context(ctx).Do()
		return err
	})
	if err != nil {
		return File{}, fmt.Errorf("renaming Drive file %s: %w", id, err)
	}
	return fromAPI(f), nil
}

func (c *Client) CreateFolder(ctx context.Context, parentID, name string) (File, error) {
	var f *drive.File
	err := c.retry(ctx, nil, func() error {
		var err error
		f, err = c.svc.Files.Create(&drive.File{Name: name, MimeType: FolderMimeType, Parents: []string{parentID}}).
			Fields(fileFields).SupportsAllDrives(true).Context(ctx).Do()
		return err
	})
	if err != nil {
		return File{}, fmt.Errorf("creating Drive folder %s: %w", name, err)
	}
	return fromAPI(f), nil
}

// mediaOptions sends no Content-Type, so Drive picks the type from the
// file name instead of the client guessing from the first bytes.
func mediaOptions() []googleapi.MediaOption {
	return []googleapi.MediaOption{googleapi.ContentType(""), googleapi.ChunkSize(uploadChunkSize)}
}

// retry calls fn until it succeeds, fails with an error that isn't worth
// retrying, or runs out of attempts. If content is set, it is rewound
// before each attempt.
func (c *Client) retry(ctx context.Context, content io.Seeker, fn func() error) error {
	delay := c.delay
	for attempt := 1; ; attempt++ {
		if content != nil {
			if _, err := content.Seek(0, io.SeekStart); err != nil {
				return err
			}
		}
		err := fn()
		if err == nil || attempt == c.attempts || !retryable(err) {
			return err
		}
		wait := delay + rand.N(delay/2+1)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
		delay *= 2
	}
}

// retryable reports whether err is a rate limit (429, or 403 with a rate
// limit reason) or a server error (5xx).
func retryable(err error) bool {
	var ge *googleapi.Error
	if !errors.As(err, &ge) {
		return false
	}
	if ge.Code == http.StatusTooManyRequests || ge.Code >= 500 {
		return true
	}
	return ge.Code == http.StatusForbidden && isRateLimited(ge)
}

func isRateLimited(ge *googleapi.Error) bool {
	for _, item := range ge.Errors {
		if item.Reason == "rateLimitExceeded" || item.Reason == "userRateLimitExceeded" {
			return true
		}
	}
	return false
}

func isNoAccess(err error) bool {
	var ge *googleapi.Error
	if !errors.As(err, &ge) {
		return false
	}
	return ge.Code == http.StatusNotFound || ge.Code == http.StatusForbidden && !isRateLimited(ge)
}

func escapeQuery(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}

func fromAPI(f *drive.File) File {
	out := File{
		ID:       f.Id,
		Name:     f.Name,
		MimeType: f.MimeType,
		MD5:      f.Md5Checksum,
		Size:     f.Size,
		Trashed:  f.Trashed,
	}
	if t, err := time.Parse(time.RFC3339, f.ModifiedTime); err == nil {
		out.ModifiedTime = t
	}
	if f.LastModifyingUser != nil {
		out.ModifiedBy = f.LastModifyingUser.DisplayName
	}
	if f.Capabilities != nil {
		out.CanAddChildren = f.Capabilities.CanAddChildren
	}
	if out.MD5 == "" && out.Size == 0 && !strings.HasPrefix(out.MimeType, "application/vnd.google-apps.") {
		out.MD5 = emptyMD5 // in case Drive leaves it out for empty files
	}
	return out
}

// emptyMD5 is the MD5 of no bytes.
const emptyMD5 = "d41d8cd98f00b204e9800998ecf8427e"
