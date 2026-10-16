package state

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoadMissing(t *testing.T) {
	st, err := NewStore(t.TempDir(), "a").Load()
	if err != nil || st != nil {
		t.Fatalf("Load = %v, %v; want nil, nil", st, err)
	}
}

func TestSaveLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subsync") // created on first save
	s := NewStore(dir, "docs/drive")
	if want := filepath.Join(dir, "docs%2Fdrive.json"); s.Path() != want {
		t.Fatalf("Path = %q, want %q", s.Path(), want)
	}

	mtime := time.Date(2026, 10, 16, 15, 2, 3, 123456789, time.FixedZone("ICT", 7*3600))
	st := New("F")
	st.FolderName = "Design"
	st.LastSync = mtime.Add(time.Minute)
	st.Files["a/b.txt"] = File{ID: "id1", MD5: "md5", Size: 3, MTime: mtime}
	st.Folders["a"] = "id2"
	if err := s.Save(st); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	f := got.Files["a/b.txt"]
	if got.Folder != "F" || got.FolderName != "Design" || !got.LastSync.Equal(st.LastSync) ||
		f.ID != "id1" || f.MD5 != "md5" || f.Size != 3 || !f.MTime.Equal(mtime) || got.Folders["a"] != "id2" {
		t.Fatalf("Load = %+v", got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want only the state file", len(entries))
	}

	if err := s.Remove(); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
	if got, _ := s.Load(); got != nil {
		t.Fatal("state still there after Remove")
	}
}

func TestNeverSyncedOmitsLastSync(t *testing.T) {
	s := NewStore(t.TempDir(), "a")
	if err := s.Save(New("F")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "lastSync") {
		t.Fatalf("state = %s", data)
	}
	st, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !st.LastSync.IsZero() || st.Files == nil || st.Folders == nil {
		t.Fatalf("Load = %+v", st)
	}
}

func TestLoadCorrupt(t *testing.T) {
	s := NewStore(t.TempDir(), "a")
	for _, content := range []string{"{corrupt", `{"version": 99}`} {
		if err := os.WriteFile(s.Path(), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Load(); err == nil {
			t.Errorf("Load(%s): want error", content)
		}
	}
}

func TestLock(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, "a")
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewStore(dir, "a").Lock()
	var locked *LockedError
	if !errors.As(err, &locked) || locked.PID != os.Getpid() {
		t.Fatalf("second Lock: err = %v, want LockedError with our PID", err)
	}
	if _, err := NewStore(dir, "b").Lock(); err != nil {
		t.Fatalf("other folder: %v", err)
	}

	unlock()
	unlock2, err := s.Lock()
	if err != nil {
		t.Fatalf("Lock after unlock: %v", err)
	}
	unlock2()
}

func TestLockTakesOverStaleLock(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadPID := cmd.Process.Pid

	s := NewStore(t.TempDir(), "a")
	for _, content := range []string{"", "garbage\n", strconv.Itoa(deadPID) + "\n"} {
		if err := os.WriteFile(s.lockPath(), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		unlock, err := s.Lock()
		if err != nil {
			t.Fatalf("stale lock %q: %v", content, err)
		}
		if got := readPID(s.lockPath()); got != os.Getpid() {
			t.Fatalf("lock PID = %d, want %d", got, os.Getpid())
		}
		unlock()
	}
}
