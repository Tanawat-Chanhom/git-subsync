// Package syncer syncs a local folder with a Drive folder: it scans both
// sides, plans what to do with a pure function, and executes the plan.
package syncer

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/Tanawat-Chanhom/git-subsync/internal/state"
)

// Mode says which sides a folder's sync may write to.
type Mode string

const (
	Both     Mode = "both"     // two-way
	Download Mode = "download" // Drive → local; never writes to Drive
	Upload   Mode = "upload"   // local → Drive; never writes local files
)

// ParseMode validates a mode name.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case Both, Download, Upload:
		return m, nil
	}
	return "", fmt.Errorf("unknown mode %q (want both, download or upload)", s)
}

func (m Mode) writesLocal() bool { return m != Upload }
func (m Mode) writesDrive() bool { return m != Download }

// LocalFile is a regular file in the local folder.
type LocalFile struct {
	Size  int64
	MTime time.Time
	MD5   string
}

// RemoteFile is a file in the Drive folder.
type RemoteFile struct {
	ID           string
	MD5          string
	Size         int64
	ModifiedTime time.Time
	ModifiedBy   string // display name of the last person to change it
}

// Local is a scan of the local folder. Paths are slash-separated and
// relative to the folder.
type Local struct {
	Files   map[string]LocalFile
	Dirs    map[string]bool
	Skipped map[string]string // path → why it isn't synced
	Temps   []string          // leftover download temp files
}

// Remote is a scan of the Drive folder, with paths as in Local.
type Remote struct {
	Files   map[string]RemoteFile
	Dirs    map[string]string // folder path → Drive folder ID
	Skipped map[string]string // path → why it isn't synced
}

// Kind is what an Action does.
type Kind int

const (
	KindDownload Kind = iota + 1 // Drive → local file
	KindUpload                   // local → Drive: update Remote, or create it if nil
	KindConflict                 // both sides differ from the last sync; see Resolution
	KindSkip                     // reported only
	KindRecord                   // both sides already match; update the state only
	KindForget                   // gone from both sides; drop from the state
)

// Resolution says how a conflict is resolved.
type Resolution int

const (
	// Unresolved: upload mode without --force. Nothing changes.
	Unresolved Resolution = iota
	// KeepBoth (both mode): rename the local file to Copy, download the
	// Drive version, then upload Copy.
	KeepBoth
	// KeepLocalCopy (download mode): rename the local file to Copy, then
	// download the Drive version. Copy stays local.
	KeepLocalCopy
	// KeepDriveCopy (upload mode with --force): rename the Drive file to
	// Copy, then upload the local file under the original name.
	KeepDriveCopy
)

// Action is one step of a sync plan.
type Action struct {
	Kind       Kind
	Path       string
	Local      *LocalFile  // nil if missing locally
	Remote     *RemoteFile // nil if missing on Drive
	Base       *state.File // nil if never synced
	Note       string      // why, for skips and unusual downloads or uploads
	Resolution Resolution  // for conflicts
	Copy       string      // for resolved conflicts: path of the _copy file
}

// Notes for actions the sync table produces.
const (
	noteLocalOnly        = "local only; download mode doesn't upload"
	noteLocalChanged     = "changed locally; download mode doesn't upload"
	noteDeletedOnDrive   = "deleted on Drive; deletes aren't synced"
	noteRemoteOnly       = "only on Drive; upload mode doesn't download"
	noteRemoteChanged    = "changed on Drive; upload mode doesn't download"
	noteDeletedLocally   = "deleted locally; deletes aren't synced"
	noteBothChanged      = "changed locally and on Drive"
	noteDiffers          = "local and Drive versions differ"
	noteFileVsFolder     = "a file locally but a folder on Drive"
	noteFolderVsFile     = "a folder locally but a file on Drive"
	noteNameClashPattern = "name clashes with %s (same apart from letter case or accents)"
)

// Plan decides what to do with every path, given the two scans, the state
// of the last sync (path → file), the mode and --force. It is pure; now only
// names conflict copies. Actions are sorted by path.
func Plan(local Local, remote Remote, base map[string]state.File, mode Mode, force bool, now time.Time) []Action {
	p := &planner{mode: mode, force: force, now: now, fold: cases.Fold(), blocked: map[string]bool{}, taken: map[string]bool{}}
	var actions []Action
	actions = append(actions, p.block(local, remote)...)

	all := map[string]bool{}
	for k := range local.Files {
		all[k] = true
	}
	for k := range remote.Files {
		all[k] = true
	}
	for k := range base {
		all[k] = true
	}
	for _, k := range allNames(local, remote) {
		p.taken[p.key(k)] = true
	}
	for k := range base {
		p.taken[p.key(k)] = true
	}

	for _, k := range sortedKeys(all) {
		if p.isBlocked(k) {
			continue
		}
		a := Action{Path: k}
		if f, ok := local.Files[k]; ok {
			a.Local = &f
		}
		if f, ok := remote.Files[k]; ok {
			a.Remote = &f
		}
		if f, ok := base[k]; ok {
			a.Base = &f
		}
		if p.classify(&a) {
			actions = append(actions, a)
		}
	}
	sort.SliceStable(actions, func(i, j int) bool { return actions[i].Path < actions[j].Path })
	return actions
}

type planner struct {
	mode    Mode
	force   bool
	now     time.Time
	fold    cases.Caser
	blocked map[string]bool // keys of blocked paths; everything under them is skipped too
	taken   map[string]bool // keys of every name in use, for conflict copies
}

// key is the name a case-insensitive, normalization-insensitive file system
// (like macOS's) sees.
func (p *planner) key(name string) string {
	return norm.NFC.String(p.fold.String(norm.NFC.String(name)))
}

// isBlocked reports whether path or one of its parent folders is blocked.
func (p *planner) isBlocked(name string) bool {
	for {
		if p.blocked[p.key(name)] {
			return true
		}
		i := strings.LastIndexByte(name, '/')
		if i < 0 {
			return false
		}
		name = name[:i]
	}
}

// block finds paths that can't be synced and returns a skip for each:
// unsyncable items from the scans, paths that are a file on one side and a
// folder on the other, and names that differ only in case or Unicode form.
// Everything under a blocked path is left alone.
func (p *planner) block(local Local, remote Remote) []Action {
	reasons := map[string][]string{}
	for _, m := range []map[string]string{local.Skipped, remote.Skipped} {
		for k, why := range m {
			reasons[k] = append(reasons[k], why)
		}
	}
	for k := range local.Files {
		if _, ok := remote.Dirs[k]; ok {
			reasons[k] = append(reasons[k], noteFileVsFolder)
		}
	}
	for k := range local.Dirs {
		if _, ok := remote.Files[k]; ok {
			reasons[k] = append(reasons[k], noteFolderVsFile)
		}
	}

	// Names in one group look the same to a case-insensitive file system.
	// The first name of a group is reported for all of them; the others
	// share its key, so blocking it blocks them too.
	groups := map[string]map[string]bool{} // key → exact names
	for _, k := range allNames(local, remote) {
		key := p.key(k)
		if groups[key] == nil {
			groups[key] = map[string]bool{}
		}
		groups[key][k] = true
	}
	clashes := map[string][]string{} // first name of a group → the others
	for _, names := range groups {
		if len(names) > 1 {
			sorted := sortedKeys(names)
			clashes[sorted[0]] = sorted[1:]
		}
	}

	// Report shallow paths first so nothing under a blocked folder is
	// reported separately.
	var names []string
	for k := range reasons {
		names = append(names, k)
	}
	for k := range clashes {
		if _, ok := reasons[k]; !ok {
			names = append(names, k)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		di, dj := strings.Count(names[i], "/"), strings.Count(names[j], "/")
		if di != dj {
			return di < dj
		}
		return names[i] < names[j]
	})
	var skips []Action
	for _, k := range names {
		if p.isBlocked(k) {
			continue
		}
		why := reasons[k]
		if others, ok := clashes[k]; ok {
			quoted := make([]string, len(others))
			for i, o := range others {
				quoted[i] = strconv.Quote(path.Base(o))
			}
			why = append(why, fmt.Sprintf(noteNameClashPattern, strings.Join(quoted, ", ")))
		}
		p.blocked[p.key(k)] = true
		skips = append(skips, Action{Kind: KindSkip, Path: k, Note: strings.Join(why, "; ")})
	}
	return skips
}

// classify fills in a's kind following the sync table, filtered by mode.
// It returns false if nothing needs doing.
func (p *planner) classify(a *Action) bool {
	l, r, b := a.Local, a.Remote, a.Base
	switch {
	case l != nil && r != nil:
		switch {
		case l.MD5 == r.MD5:
			if b != nil && b.ID == r.ID && b.MD5 == l.MD5 && b.Size == l.Size && b.MTime.Equal(l.MTime) {
				return false // in sync, and the state is up to date
			}
			a.Kind = KindRecord
		case b == nil:
			p.conflict(a, noteDiffers)
		case l.MD5 != b.MD5 && r.MD5 != b.MD5:
			p.conflict(a, noteBothChanged)
		case r.MD5 != b.MD5:
			p.toLocal(a, "", noteRemoteChanged)
		default:
			p.toDrive(a, "", noteLocalChanged)
		}
	case l != nil:
		if b == nil {
			p.toDrive(a, "", noteLocalOnly)
		} else {
			p.toDrive(a, noteDeletedOnDrive, noteDeletedOnDrive)
		}
	case r != nil:
		if b == nil {
			p.toLocal(a, "", noteRemoteOnly)
		} else {
			p.toLocal(a, noteDeletedLocally, noteDeletedLocally)
		}
	default:
		a.Kind = KindForget
	}
	return true
}

// toLocal downloads a if the mode allows writing local files, and skips it
// otherwise.
func (p *planner) toLocal(a *Action, note, skipNote string) {
	if p.mode.writesLocal() {
		a.Kind, a.Note = KindDownload, note
	} else {
		a.Kind, a.Note = KindSkip, skipNote
	}
}

// toDrive uploads a if the mode allows writing to Drive, and skips it
// otherwise.
func (p *planner) toDrive(a *Action, note, skipNote string) {
	if p.mode.writesDrive() {
		a.Kind, a.Note = KindUpload, note
	} else {
		a.Kind, a.Note = KindSkip, skipNote
	}
}

func (p *planner) conflict(a *Action, note string) {
	a.Kind, a.Note = KindConflict, note
	switch {
	case p.mode == Both:
		a.Resolution = KeepBoth
	case p.mode == Download:
		a.Resolution = KeepLocalCopy
	case p.force:
		a.Resolution = KeepDriveCopy
	default:
		a.Resolution = Unresolved
		return
	}
	a.Copy = p.copyName(a.Path)
}

// copyName returns <name>_copy_<YYYYMMDD-HHMMSS><ext> in the same folder,
// with -2, -3, ... added if that name is in use on either side.
func (p *planner) copyName(name string) string {
	c := CopyName(name, p.now, func(c string) bool { return p.taken[p.key(c)] })
	p.taken[p.key(c)] = true
	return c
}

// CopyName returns the conflict copy name for the slash-separated path
// name: <name>_copy_<YYYYMMDD-HHMMSS><ext>, using now as given (callers pass
// local time). If taken reports the name is in use, -2, -3, ... is added
// before the extension.
func CopyName(name string, now time.Time, taken func(string) bool) string {
	dir, stem, ext := splitName(name)
	stem += "_copy_" + now.Format("20060102-150405")
	for n := 1; ; n++ {
		c := dir + stem
		if n > 1 {
			c += "-" + strconv.Itoa(n)
		}
		c += ext
		if taken == nil || !taken(c) {
			return c
		}
	}
}

// CopyPattern describes the conflict copy name for the file name, as
// <name>_copy_<YYYYMMDD-HHMMSS><ext>, for messages.
func CopyPattern(name string) string {
	_, stem, ext := splitName(path.Base(name))
	return stem + "_copy_<YYYYMMDD-HHMMSS>" + ext
}

// splitName splits a slash-separated path into its folder (with a trailing
// slash), the file name without extension, and the extension. A dotfile
// like .env has no extension.
func splitName(name string) (dir, stem, ext string) {
	dir, base := path.Split(name)
	ext = path.Ext(base)
	if ext == base {
		ext = ""
	}
	return dir, strings.TrimSuffix(base, ext), ext
}

// allNames returns every path either scan saw: files, folders and skipped
// items. It may contain duplicates.
func allNames(local Local, remote Remote) []string {
	var names []string
	for k := range local.Files {
		names = append(names, k)
	}
	for k := range local.Dirs {
		names = append(names, k)
	}
	for k := range remote.Files {
		names = append(names, k)
	}
	for _, m := range []map[string]string{remote.Dirs, local.Skipped, remote.Skipped} {
		for k := range m {
			names = append(names, k)
		}
	}
	return names
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
