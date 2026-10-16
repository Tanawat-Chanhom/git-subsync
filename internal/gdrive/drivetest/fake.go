// Package drivetest provides an in-memory gdrive.Drive for tests.
package drivetest

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive"
)

// Fake is an in-memory Drive. Setup helpers (AddFile, SetContent, ...) act
// as another person editing Drive; only calls through the gdrive.Drive
// methods count as writes.
type Fake struct {
	// User is the display name recorded on files changed through the
	// gdrive.Drive methods.
	User string
	// Hook, if set, runs before every gdrive.Drive call (op is "get",
	// "list", "download", "create", "update", "rename" or "mkdir"; name is
	// the file ID, or the new name for create, rename and mkdir). A non-nil
	// error fails the call. It may call the setup helpers.
	Hook func(op, name string) error

	mu     sync.Mutex
	items  map[string]*item
	nextID int
	clock  time.Time
	writes int
}

type item struct {
	file    gdrive.File
	parent  string
	content []byte
}

var _ gdrive.Drive = (*Fake)(nil)

// New returns an empty Fake.
func New() *Fake {
	return &Fake{User: "Me", items: map[string]*item{}, clock: time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)}
}

// AddFolder creates a folder; parentID "" makes a top-level folder.
func (f *Fake) AddFolder(parentID, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.add(parentID, gdrive.File{Name: name, MimeType: gdrive.FolderMimeType, CanAddChildren: true}, nil, "")
}

// AddFile creates a file changed last by the person named by.
func (f *Fake) AddFile(parentID, name, content, by string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.add(parentID, gdrive.File{Name: name, MimeType: "text/plain"}, []byte(content), by)
}

// AddGoogleDoc creates a Google-native file, which has no content or MD5.
func (f *Fake) AddGoogleDoc(parentID, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.add(parentID, gdrive.File{Name: name, MimeType: "application/vnd.google-apps.document"}, nil, "")
	f.items[id].file.MD5 = ""
	return id
}

// SetContent replaces a file's content as the person named by.
func (f *Fake) SetContent(id, content, by string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setContent(f.items[id], []byte(content), by)
}

// SetMD5 overwrites the MD5 Drive reports without changing the content.
func (f *Fake) SetMD5(id, sum string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[id].file.MD5 = sum
}

// Trash moves a file or folder to the trash.
func (f *Fake) Trash(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[id].file.Trashed = true
}

// SetCanAddChildren sets whether the account may add files to a folder.
func (f *Fake) SetCanAddChildren(id string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[id].file.CanAddChildren = ok
}

// Writes returns how many gdrive.Drive calls changed Drive.
func (f *Fake) Writes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

// Find returns the ID of the file or folder at the slash-separated path
// under folderID.
func (f *Fake) Find(folderID, p string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := folderID
	for _, name := range strings.Split(p, "/") {
		found := ""
		for _, it := range f.children(id) {
			if it.file.Name == name {
				found = it.file.ID
			}
		}
		if found == "" {
			return "", false
		}
		id = found
	}
	return id, true
}

// Tree returns every file under folderID that isn't in the trash, as path →
// content. Folders appear as "path/" → "". Google-native files appear with
// content "<google doc>".
func (f *Fake) Tree(folderID string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	tree := map[string]string{}
	var walk func(id, prefix string)
	walk = func(id, prefix string) {
		for _, it := range f.children(id) {
			p := path.Join(prefix, it.file.Name)
			switch {
			case it.file.IsFolder():
				tree[p+"/"] = ""
				walk(it.file.ID, p)
			case strings.HasPrefix(it.file.MimeType, "application/vnd.google-apps."):
				tree[p] = "<google doc>"
			default:
				tree[p] = string(it.content)
			}
		}
	}
	walk(folderID, "")
	return tree
}

// File returns the metadata of a file, even if it's in the trash.
func (f *Fake) File(id string) gdrive.File {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.items[id].file
}

func (f *Fake) Get(ctx context.Context, id string) (gdrive.File, error) {
	if err := f.hook("get", id); err != nil {
		return gdrive.File{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	it, ok := f.items[id]
	if !ok {
		return gdrive.File{}, fmt.Errorf("getting Drive file %s: %w", id, gdrive.ErrNoAccess)
	}
	return it.file, nil
}

func (f *Fake) List(ctx context.Context, folderID string) ([]gdrive.File, error) {
	if err := f.hook("list", folderID); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.folder(folderID); err != nil {
		return nil, err
	}
	var files []gdrive.File
	for _, it := range f.children(folderID) {
		files = append(files, it.file)
	}
	return files, nil
}

func (f *Fake) Download(ctx context.Context, id string) (io.ReadCloser, error) {
	if err := f.hook("download", id); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	it, ok := f.items[id]
	if !ok || it.file.IsFolder() || it.file.Trashed {
		return nil, fmt.Errorf("downloading %s: %w", id, gdrive.ErrNoAccess)
	}
	return io.NopCloser(strings.NewReader(string(it.content))), nil
}

func (f *Fake) Create(ctx context.Context, parentID, name string, content io.ReadSeeker) (gdrive.File, error) {
	if err := f.hook("create", name); err != nil {
		return gdrive.File{}, err
	}
	data, err := io.ReadAll(content)
	if err != nil {
		return gdrive.File{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.folder(parentID); err != nil {
		return gdrive.File{}, err
	}
	f.writes++
	id := f.add(parentID, gdrive.File{Name: name, MimeType: "application/octet-stream"}, data, f.User)
	return f.items[id].file, nil
}

func (f *Fake) Update(ctx context.Context, id string, content io.ReadSeeker) (gdrive.File, error) {
	if err := f.hook("update", id); err != nil {
		return gdrive.File{}, err
	}
	data, err := io.ReadAll(content)
	if err != nil {
		return gdrive.File{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	it, ok := f.items[id]
	if !ok || it.file.IsFolder() {
		return gdrive.File{}, fmt.Errorf("uploading %s: %w", id, gdrive.ErrNoAccess)
	}
	f.writes++
	f.setContent(it, data, f.User)
	return it.file, nil
}

func (f *Fake) Rename(ctx context.Context, id, name string) (gdrive.File, error) {
	if err := f.hook("rename", name); err != nil {
		return gdrive.File{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	it, ok := f.items[id]
	if !ok {
		return gdrive.File{}, fmt.Errorf("renaming %s: %w", id, gdrive.ErrNoAccess)
	}
	f.writes++
	it.file.Name = name
	return it.file, nil
}

func (f *Fake) CreateFolder(ctx context.Context, parentID, name string) (gdrive.File, error) {
	if err := f.hook("mkdir", name); err != nil {
		return gdrive.File{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.folder(parentID); err != nil {
		return gdrive.File{}, err
	}
	f.writes++
	id := f.add(parentID, gdrive.File{Name: name, MimeType: gdrive.FolderMimeType, CanAddChildren: true}, nil, f.User)
	return f.items[id].file, nil
}

func (f *Fake) hook(op, name string) error {
	if f.Hook == nil {
		return nil
	}
	return f.Hook(op, name)
}

// add stores a new item; f.mu must be held.
func (f *Fake) add(parentID string, file gdrive.File, content []byte, by string) string {
	f.nextID++
	file.ID = fmt.Sprintf("id%03d", f.nextID)
	it := &item{file: file, parent: parentID}
	f.items[file.ID] = it
	if file.IsFolder() {
		f.tick(it, by)
	} else {
		f.setContent(it, content, by)
	}
	return file.ID
}

func (f *Fake) setContent(it *item, content []byte, by string) {
	sum := md5.Sum(content)
	it.content = content
	it.file.MD5 = hex.EncodeToString(sum[:])
	it.file.Size = int64(len(content))
	f.tick(it, by)
}

func (f *Fake) tick(it *item, by string) {
	f.clock = f.clock.Add(time.Minute)
	it.file.ModifiedTime = f.clock
	it.file.ModifiedBy = by
}

func (f *Fake) folder(id string) (*item, error) {
	it, ok := f.items[id]
	if !ok || !it.file.IsFolder() || it.file.Trashed {
		return nil, fmt.Errorf("folder %s: %w", id, gdrive.ErrNoAccess)
	}
	return it, nil
}

// children returns the items in a folder that aren't in the trash, sorted
// by name then ID.
func (f *Fake) children(id string) []*item {
	var out []*item
	for _, it := range f.items {
		if it.parent == id && !it.file.Trashed {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].file.Name != out[j].file.Name {
			return out[i].file.Name < out[j].file.Name
		}
		return out[i].file.ID < out[j].file.ID
	})
	return out
}
