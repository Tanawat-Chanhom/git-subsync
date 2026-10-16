package gdrive

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// idPattern matches Drive file and folder IDs.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{10,}$`)

// ParseFolderID returns the folder ID in a Drive folder link or a raw ID.
// It accepts .../drive/folders/<id>, .../drive/u/<n>/folders/<id> (with
// any query string) and .../open?id=<id>.
func ParseFolderID(s string) (string, error) {
	s = strings.TrimSpace(s)
	if idPattern.MatchString(s) {
		return s, nil
	}
	raw := s
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || !isDriveHost(u.Hostname()) {
		return "", notFolderURL(s)
	}

	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, seg := range segs {
		if seg == "folders" && i+1 < len(segs) && idPattern.MatchString(segs[i+1]) {
			return segs[i+1], nil
		}
	}
	if u.Path == "/open" {
		if id := u.Query().Get("id"); idPattern.MatchString(id) {
			return id, nil
		}
	}
	return "", notFolderURL(s)
}

func isDriveHost(host string) bool {
	return host == "drive.google.com" || host == "docs.google.com"
}

func notFolderURL(s string) error {
	return fmt.Errorf("%q is not a Google Drive folder link or ID", s)
}
