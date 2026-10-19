package gdrive

import "testing"

func TestParseFolderID(t *testing.T) {
	const id = "1YCRTT34nq009SX1Ndxmb0ndgLzNEn0TD"
	for _, in := range []string{
		id,
		"https://drive.google.com/drive/folders/" + id,
		"https://drive.google.com/drive/folders/" + id + "/",
		"https://drive.google.com/drive/folders/" + id + "?usp=sharing",
		"https://drive.google.com/drive/folders/" + id + "?usp=drive_link#x",
		"https://drive.google.com/drive/u/0/folders/" + id,
		"https://drive.google.com/drive/u/3/folders/" + id + "?usp=sharing",
		"https://drive.google.com/open?id=" + id,
		"https://drive.google.com/open?id=" + id + "&usp=sharing",
		"drive.google.com/drive/folders/" + id,
		"http://drive.google.com/drive/folders/" + id,
	} {
		got, err := ParseFolderID(in)
		if err != nil {
			t.Errorf("ParseFolderID(%q): %v", in, err)
			continue
		}
		if got != id {
			t.Errorf("ParseFolderID(%q) = %q, want %q", in, got, id)
		}
	}
}

func TestParseFolderIDRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"short",
		"https://example.com/drive/folders/1YCRTT34nq009SX1Ndxmb0ndgLzNEn0TD",
		"https://drive.google.com/drive/folders/",
		"https://drive.google.com/drive/my-drive",
		"https://drive.google.com/file/d/1YCRTT34nq009SX1Ndxmb0ndgLzNEn0TD/view",
		"https://drive.google.com/open?id=bad id",
		"https://drive.google.com/drive/folders/has%20space",
	} {
		if got, err := ParseFolderID(in); err == nil {
			t.Errorf("ParseFolderID(%q) = %q, want error", in, got)
		}
	}
}
