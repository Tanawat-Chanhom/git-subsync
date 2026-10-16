package syncer

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tanawat-Chanhom/git-subsync/internal/state"
)

var (
	now   = time.Date(2026, 10, 16, 16, 12, 0, 0, time.FixedZone("ICT", 7*3600))
	mtime = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
)

// scans builds inputs for one path from md5s; "" means missing.
func scans(path, l, r, b string) (Local, Remote, map[string]state.File) {
	local := Local{Files: map[string]LocalFile{}, Dirs: map[string]bool{}}
	remote := Remote{Files: map[string]RemoteFile{}, Dirs: map[string]string{}}
	base := map[string]state.File{}
	if l != "" {
		local.Files[path] = LocalFile{Size: 1, MTime: mtime, MD5: l}
	}
	if r != "" {
		remote.Files[path] = RemoteFile{ID: "id-" + path, MD5: r, Size: 1, ModifiedBy: "Somchai"}
	}
	if b != "" {
		base[path] = state.File{ID: "id-" + path, MD5: b, Size: 1, MTime: mtime}
	}
	return local, remote, base
}

// want is the expected single action, written compactly:
// "download", "upload", "record", "forget", "none", "skip: <note>",
// "conflict: keep-both" (and keep-local, keep-drive, unresolved).
type want map[Mode]string

func describe(actions []Action) string {
	if len(actions) == 0 {
		return "none"
	}
	var parts []string
	for _, a := range actions {
		var s string
		switch a.Kind {
		case KindDownload:
			s = "download"
		case KindUpload:
			s = "upload"
		case KindRecord:
			s = "record"
		case KindForget:
			s = "forget"
		case KindSkip:
			s = "skip: " + a.Note
		case KindConflict:
			s = "conflict: " + map[Resolution]string{
				Unresolved: "unresolved", KeepBoth: "keep-both", KeepLocalCopy: "keep-local", KeepDriveCopy: "keep-drive",
			}[a.Resolution]
		}
		if a.Kind != KindSkip && a.Note != "" {
			s += " (" + a.Note + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// The sync table, row by row, in every mode. --force changes only
// upload-mode conflicts.
func TestPlanTable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		l, r, b    string
		want       want
		wantForced string // upload mode with --force, if different
	}{
		{
			name: "new local file",
			l:    "x",
			want: want{Both: "upload", Download: "skip: " + noteLocalOnly, Upload: "upload"},
		},
		{
			name: "new Drive file",
			r:    "x",
			want: want{Both: "download", Download: "download", Upload: "skip: " + noteRemoteOnly},
		},
		{
			name: "on both sides, never synced, same",
			l:    "x", r: "x",
			want: want{Both: "record", Download: "record", Upload: "record"},
		},
		{
			name: "on both sides, never synced, different",
			l:    "x", r: "y",
			want: want{
				Both:     "conflict: keep-both (" + noteDiffers + ")",
				Download: "conflict: keep-local (" + noteDiffers + ")",
				Upload:   "conflict: unresolved (" + noteDiffers + ")",
			},
			wantForced: "conflict: keep-drive (" + noteDiffers + ")",
		},
		{
			name: "unchanged",
			l:    "x", r: "x", b: "x",
			want: want{Both: "none", Download: "none", Upload: "none"},
		},
		{
			name: "both changed the same way",
			l:    "y", r: "y", b: "x",
			want: want{Both: "record", Download: "record", Upload: "record"},
		},
		{
			name: "changed on Drive",
			l:    "x", r: "y", b: "x",
			want: want{Both: "download", Download: "download", Upload: "skip: " + noteRemoteChanged},
		},
		{
			name: "changed locally",
			l:    "y", r: "x", b: "x",
			want: want{Both: "upload", Download: "skip: " + noteLocalChanged, Upload: "upload"},
		},
		{
			name: "changed on both sides",
			l:    "y", r: "z", b: "x",
			want: want{
				Both:     "conflict: keep-both (" + noteBothChanged + ")",
				Download: "conflict: keep-local (" + noteBothChanged + ")",
				Upload:   "conflict: unresolved (" + noteBothChanged + ")",
			},
			wantForced: "conflict: keep-drive (" + noteBothChanged + ")",
		},
		{
			name: "deleted locally",
			r:    "x", b: "x",
			want: want{
				Both:     "download (" + noteDeletedLocally + ")",
				Download: "download (" + noteDeletedLocally + ")",
				Upload:   "skip: " + noteDeletedLocally,
			},
		},
		{
			name: "deleted locally, changed on Drive",
			r:    "y", b: "x",
			want: want{
				Both:     "download (" + noteDeletedLocally + ")",
				Download: "download (" + noteDeletedLocally + ")",
				Upload:   "skip: " + noteDeletedLocally,
			},
		},
		{
			name: "deleted on Drive",
			l:    "x", b: "x",
			want: want{
				Both:     "upload (" + noteDeletedOnDrive + ")",
				Download: "skip: " + noteDeletedOnDrive,
				Upload:   "upload (" + noteDeletedOnDrive + ")",
			},
		},
		{
			name: "deleted on Drive, changed locally",
			l:    "y", b: "x",
			want: want{
				Both:     "upload (" + noteDeletedOnDrive + ")",
				Download: "skip: " + noteDeletedOnDrive,
				Upload:   "upload (" + noteDeletedOnDrive + ")",
			},
		},
		{
			name: "deleted on both sides",
			b:    "x",
			want: want{Both: "forget", Download: "forget", Upload: "forget"},
		},
	} {
		for _, mode := range []Mode{Both, Download, Upload} {
			for _, force := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/force=%v", tc.name, mode, force), func(t *testing.T) {
					local, remote, base := scans("a.txt", tc.l, tc.r, tc.b)
					expected := tc.want[mode]
					if mode == Upload && force && tc.wantForced != "" {
						expected = tc.wantForced
					}
					if got := describe(Plan(local, remote, base, mode, force, now)); got != expected {
						t.Fatalf("got  %s\nwant %s", got, expected)
					}
				})
			}
		}
	}
}

func TestPlanCarriesSides(t *testing.T) {
	local, remote, base := scans("docs/a.txt", "y", "z", "x")
	actions := Plan(local, remote, base, Upload, false, now)
	if len(actions) != 1 {
		t.Fatalf("got %d actions", len(actions))
	}
	a := actions[0]
	if a.Path != "docs/a.txt" || a.Local.MD5 != "y" || a.Remote.MD5 != "z" || a.Remote.ModifiedBy != "Somchai" || a.Base.MD5 != "x" {
		t.Fatalf("action = %+v", a)
	}
	if a.Copy != "" {
		t.Fatalf("unresolved conflict has a copy name: %q", a.Copy)
	}
}

// The local md5 cache in the state is refreshed when only the mtime changed.
func TestPlanRecordsNewMtime(t *testing.T) {
	local, remote, base := scans("a.txt", "x", "x", "x")
	f := local.Files["a.txt"]
	f.MTime = mtime.Add(time.Hour)
	local.Files["a.txt"] = f
	if got := describe(Plan(local, remote, base, Both, false, now)); got != "record" {
		t.Fatalf("got %s, want record", got)
	}

	// A new Drive file ID with the same content is recorded too.
	local, remote, base = scans("a.txt", "x", "x", "x")
	r := remote.Files["a.txt"]
	r.ID = "new-id"
	remote.Files["a.txt"] = r
	if got := describe(Plan(local, remote, base, Both, false, now)); got != "record" {
		t.Fatalf("got %s, want record", got)
	}
}

func TestCopyName(t *testing.T) {
	for in, want := range map[string]string{
		"file1.txt":           "file1_copy_20261016-161200.txt",
		"docs/file1.txt":      "docs/file1_copy_20261016-161200.txt",
		"README":              "README_copy_20261016-161200",
		".env":                ".env_copy_20261016-161200",
		"archive.tar.gz":      "archive.tar_copy_20261016-161200.gz",
		"a.dir/noext":         "a.dir/noext_copy_20261016-161200",
		"ภาพ/รายงาน.pdf":      "ภาพ/รายงาน_copy_20261016-161200.pdf",
		"docs/.config.yaml":   "docs/.config_copy_20261016-161200.yaml",
		"trailing-dot.":       "trailing-dot_copy_20261016-161200.",
		"spaces in name.docx": "spaces in name_copy_20261016-161200.docx",
	} {
		if got := CopyName(in, now, nil); got != want {
			t.Errorf("CopyName(%q) = %q, want %q", in, got, want)
		}
	}

	taken := map[string]bool{"a_copy_20261016-161200.txt": true, "a_copy_20261016-161200-2.txt": true}
	if got := CopyName("a.txt", now, func(s string) bool { return taken[s] }); got != "a_copy_20261016-161200-3.txt" {
		t.Errorf("with taken names: %q", got)
	}
}

func TestCopyPattern(t *testing.T) {
	for in, want := range map[string]string{
		"docs/file1.txt": "file1_copy_<YYYYMMDD-HHMMSS>.txt",
		".env":           ".env_copy_<YYYYMMDD-HHMMSS>",
		"README":         "README_copy_<YYYYMMDD-HHMMSS>",
	} {
		if got := CopyPattern(in); got != want {
			t.Errorf("CopyPattern(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCopyNameUsesGivenTime(t *testing.T) {
	utc := time.Date(2026, 10, 16, 9, 12, 0, 0, time.UTC)
	if got := CopyName("a.txt", utc, nil); got != "a_copy_20261016-091200.txt" {
		t.Fatalf("got %q", got)
	}
}

// A copy name must be free on both sides, ignoring case, and two conflicts
// in one run must not get the same name.
func TestPlanCopyNamesAvoidTakenNames(t *testing.T) {
	local, remote, base := scans("a.txt", "y", "z", "x")
	local.Files["A_COPY_20261016-161200.TXT"] = LocalFile{MD5: "q", MTime: mtime} // taken locally, different case
	remote.Files["a_copy_20261016-161200-2.txt"] = RemoteFile{ID: "c2", MD5: "q2"}
	local.Files["b.txt"] = LocalFile{MD5: "y", MTime: mtime}
	remote.Files["b.txt"] = RemoteFile{ID: "b", MD5: "z"}

	copies := map[string]string{}
	for _, a := range Plan(local, remote, base, Both, false, now) {
		if a.Kind == KindConflict {
			copies[a.Path] = a.Copy
		}
	}
	if copies["a.txt"] != "a_copy_20261016-161200-3.txt" || copies["b.txt"] != "b_copy_20261016-161200.txt" {
		t.Fatalf("copies = %v", copies)
	}

	// Upload --force puts the copy on Drive; it must avoid local names too.
	for _, a := range Plan(local, remote, base, Upload, true, now) {
		if a.Path == "a.txt" && a.Copy != "a_copy_20261016-161200-3.txt" {
			t.Fatalf("upload --force copy = %q", a.Copy)
		}
	}
}

func TestPlanSkipsUnsyncableItems(t *testing.T) {
	local := Local{
		Files: map[string]LocalFile{
			"Notes":        {MD5: "n", MTime: mtime}, // a Google Doc of that name exists on Drive
			"dup/x.txt":    {MD5: "x", MTime: mtime}, // under a duplicate Drive folder
			"link-target":  {MD5: "t", MTime: mtime},
			"ok.txt":       {MD5: "o", MTime: mtime},
			"thing":        {MD5: "f", MTime: mtime}, // a folder on Drive
			"dir/file.txt": {MD5: "d", MTime: mtime},
		},
		Dirs:    map[string]bool{"dup": true, "dir": true},
		Skipped: map[string]string{"link": "symbolic link"},
	}
	remote := Remote{
		Files: map[string]RemoteFile{
			"link":     {ID: "l", MD5: "l"}, // must not overwrite the local symlink
			"dir":      {ID: "d", MD5: "d"}, // a file on Drive, a folder locally
			"thing/in": {ID: "i", MD5: "i"},
		},
		Dirs:    map[string]string{"thing": "T"},
		Skipped: map[string]string{"Notes": "Google Docs file", "dup": "2 items with this name in the Drive folder"},
	}
	base := map[string]state.File{"dup/old.txt": {ID: "o", MD5: "o"}} // kept: dup/ is blocked

	got := describe(Plan(local, remote, base, Both, false, now))
	want := strings.Join([]string{
		"skip: Google Docs file",                           // Notes
		"skip: " + noteFolderVsFile,                        // dir
		"skip: 2 items with this name in the Drive folder", // dup
		"skip: symbolic link",                              // link
		"upload",                                           // link-target
		"upload",                                           // ok.txt
		"skip: " + noteFileVsFolder,                        // thing
	}, ", ")
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestPlanNameClashes(t *testing.T) {
	nfc, nfd := "café.txt", "café.txt"
	local := Local{
		Files: map[string]LocalFile{
			"A.txt":   {MD5: "a", MTime: mtime},
			"Docs/x":  {MD5: "x", MTime: mtime},
			nfd:       {MD5: "c", MTime: mtime},
			"same.md": {MD5: "s", MTime: mtime},
		},
		Dirs: map[string]bool{"Docs": true},
	}
	remote := Remote{
		Files: map[string]RemoteFile{
			"a.txt":   {ID: "1", MD5: "b"},
			"docs/x":  {ID: "2", MD5: "x"},
			"docs/y":  {ID: "3", MD5: "y"},
			nfc:       {ID: "4", MD5: "c"},
			"README":  {ID: "5", MD5: "r"},
			"readme":  {ID: "6", MD5: "r2"}, // two Drive names that clash with each other
			"same.md": {ID: "7", MD5: "s"},
		},
		Dirs: map[string]string{"docs": "D"},
	}
	base := map[string]state.File{"a.txt": {ID: "1", MD5: "a"}}

	var lines []string
	for _, a := range Plan(local, remote, base, Both, false, now) {
		lines = append(lines, a.Path+" → "+describe([]Action{a}))
	}
	want := []string{
		`A.txt → skip: name clashes with "a.txt" (same apart from letter case or accents)`,
		`Docs → skip: name clashes with "docs" (same apart from letter case or accents)`,
		`README → skip: name clashes with "readme" (same apart from letter case or accents)`,
		nfd + ` → skip: name clashes with "` + nfc + `" (same apart from letter case or accents)`,
		`same.md → record`,
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestParseMode(t *testing.T) {
	for _, s := range []string{"both", "download", "upload"} {
		if m, err := ParseMode(s); err != nil || string(m) != s {
			t.Errorf("ParseMode(%q) = %q, %v", s, m, err)
		}
	}
	if _, err := ParseMode("Both"); err == nil {
		t.Error("ParseMode(Both): want error")
	}
}
