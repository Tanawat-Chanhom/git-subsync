package syncer

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive"
	"github.com/Tanawat-Chanhom/git-subsync/internal/state"
)

// ignoredNames are left alone on both sides without a word.
var ignoredNames = map[string]bool{".DS_Store": true, "Thumbs.db": true}

// tempPrefix starts the names of partial downloads.
const tempPrefix = ".git-subsync-tmp-"

const googleAppsPrefix = "application/vnd.google-apps."

// ScanLocal lists the files under dir. A file's MD5 is taken from base when
// its size and mtime match the last sync; otherwise the file is hashed. A
// missing dir is empty.
func ScanLocal(dir string, base map[string]state.File) (Local, error) {
	local := Local{Files: map[string]LocalFile{}, Dirs: map[string]bool{}, Skipped: map[string]string{}}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if p == dir {
			if errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if err != nil {
			local.Skipped[rel] = "can't read: " + errText(err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		name := d.Name()
		switch {
		case ignoredNames[name]:
			if d.IsDir() {
				return fs.SkipDir
			}
		case strings.HasPrefix(name, tempPrefix):
			if d.Type().IsRegular() {
				local.Temps = append(local.Temps, rel)
			}
		case d.Type()&fs.ModeSymlink != 0:
			local.Skipped[rel] = "symbolic link"
		case d.IsDir():
			local.Dirs[rel] = true
		case d.Type().IsRegular():
			f, err := scanFile(p, base[rel])
			if err != nil {
				local.Skipped[rel] = "can't read: " + errText(err)
				return nil
			}
			local.Files[rel] = f
		default:
			local.Skipped[rel] = "not a regular file"
		}
		return nil
	})
	return local, err
}

func scanFile(p string, last state.File) (LocalFile, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return LocalFile{}, err
	}
	f := LocalFile{Size: fi.Size(), MTime: fi.ModTime()}
	if last.MD5 != "" && last.Size == f.Size && last.MTime.Equal(f.MTime) {
		f.MD5 = last.MD5
		return f, nil
	}
	f.MD5, err = hashFile(p)
	return f, err
}

func hashFile(p string) (string, error) {
	file, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := md5.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// errText is err's message without the path, which the caller already
// reports.
func errText(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// ScanRemote lists the files under the Drive folder rootID.
func ScanRemote(ctx context.Context, d gdrive.Drive, rootID string) (Remote, error) {
	remote := Remote{Files: map[string]RemoteFile{}, Dirs: map[string]string{}, Skipped: map[string]string{}}
	var walk func(id, prefix string) error
	walk = func(id, prefix string) error {
		children, err := d.List(ctx, id)
		if err != nil {
			return err
		}
		count := map[string]int{}
		for _, c := range children {
			count[c.Name]++
		}
		for _, c := range children {
			if ignoredNames[c.Name] {
				continue
			}
			if why := invalidName(c.Name); why != "" {
				remote.Skipped[path.Join(prefix, strings.ReplaceAll(c.Name, "/", "∕"))] = why
				continue
			}
			rel := path.Join(prefix, c.Name)
			switch {
			case count[c.Name] > 1:
				remote.Skipped[rel] = fmt.Sprintf("%d items with this name in the Drive folder", count[c.Name])
			case c.IsFolder():
				remote.Dirs[rel] = c.ID
				if err := walk(c.ID, rel); err != nil {
					return err
				}
			case strings.HasPrefix(c.MimeType, googleAppsPrefix):
				remote.Skipped[rel] = googleKind(c.MimeType)
			case c.MD5 == "":
				remote.Skipped[rel] = "Drive has no checksum for it"
			default:
				remote.Files[rel] = RemoteFile{
					ID: c.ID, MD5: c.MD5, Size: c.Size, ModifiedTime: c.ModifiedTime, ModifiedBy: c.ModifiedBy,
				}
			}
		}
		return nil
	}
	return remote, walk(rootID, "")
}

// invalidName says why a Drive name can't be a local file name, or returns
// "" if it can.
func invalidName(name string) string {
	switch {
	case name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00"):
		return "name isn't valid for a local file"
	case len(name) > 255:
		return "name is too long for a local file"
	case strings.HasPrefix(name, tempPrefix):
		return "name is reserved for git-subsync's temporary files"
	}
	return ""
}

func googleKind(mimeType string) string {
	switch kind := strings.TrimPrefix(mimeType, googleAppsPrefix); kind {
	case "document":
		return "Google Docs file"
	case "spreadsheet":
		return "Google Sheets file"
	case "presentation":
		return "Google Slides file"
	case "shortcut":
		return "Drive shortcut"
	default:
		return "Google " + kind + " file"
	}
}
