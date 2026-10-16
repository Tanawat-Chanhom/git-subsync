package gdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// testClient returns a Client talking to handler, with fast retries.
func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "at"})
	svc, err := NewService(context.Background(), ts, fakeDrive(t, handler))
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(svc)
	c.delay = time.Millisecond
	return c
}

func writeJSON(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	io.WriteString(w, body)
}

func apiError(code int, reason string) string {
	return fmt.Sprintf(`{"error":{"code":%d,"message":"%s","errors":[{"reason":"%s","message":"%s"}]}}`, code, reason, reason, reason)
}

const fileJSON = `{"id":"F1","name":"a.txt","mimeType":"text/plain","md5Checksum":"abc","size":"42",
	"modifiedTime":"2026-10-16T08:02:03.456Z","lastModifyingUser":{"displayName":"Somchai"},
	"trashed":false,"capabilities":{"canAddChildren":true}}`

func TestClientGet(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/files/F1") {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("fields") != fileFields || q.Get("supportsAllDrives") != "true" {
			t.Errorf("query = %v", q)
		}
		writeJSON(w, 200, fileJSON)
	})
	f, err := c.Get(context.Background(), "F1")
	if err != nil {
		t.Fatal(err)
	}
	want := File{
		ID: "F1", Name: "a.txt", MimeType: "text/plain", MD5: "abc", Size: 42,
		ModifiedTime: time.Date(2026, 10, 16, 8, 2, 3, 456e6, time.UTC), ModifiedBy: "Somchai", CanAddChildren: true,
	}
	if !f.ModifiedTime.Equal(want.ModifiedTime) {
		t.Fatalf("ModifiedTime = %v", f.ModifiedTime)
	}
	f.ModifiedTime = want.ModifiedTime
	if f != want {
		t.Fatalf("Get = %+v\nwant  %+v", f, want)
	}
}

func TestClientEmptyFileMD5(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"files":[
			{"id":"1","name":".gitkeep","mimeType":"application/octet-stream","size":"0"},
			{"id":"2","name":"Doc","mimeType":"application/vnd.google-apps.document"},
			{"id":"3","name":"dir","mimeType":"application/vnd.google-apps.folder"}]}`)
	})
	files, err := c.List(context.Background(), "F1")
	if err != nil {
		t.Fatal(err)
	}
	if files[0].MD5 != emptyMD5 || files[1].MD5 != "" || files[2].MD5 != "" {
		t.Fatalf("files = %+v", files)
	}
}

func TestClientGetNoAccess(t *testing.T) {
	for _, tc := range []struct {
		code   int
		reason string
		want   bool
	}{
		{404, "notFound", true},
		{403, "insufficientFilePermissions", true},
		{400, "badRequest", false},
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, tc.code, apiError(tc.code, tc.reason))
		})
		_, err := c.Get(context.Background(), "F1")
		if err == nil || errors.Is(err, ErrNoAccess) != tc.want {
			t.Errorf("%d %s: err = %v, want ErrNoAccess = %v", tc.code, tc.reason, err, tc.want)
		}
	}
}

func TestClientListPaginates(t *testing.T) {
	var calls int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if q.Get("q") != `'F\'1' in parents and trashed = false` {
			t.Errorf("q = %q", q.Get("q"))
		}
		if q.Get("supportsAllDrives") != "true" || q.Get("includeItemsFromAllDrives") != "true" {
			t.Errorf("shared drive flags missing: %v", q)
		}
		if !strings.Contains(q.Get("fields"), "files(") || !strings.Contains(q.Get("fields"), "md5Checksum") {
			t.Errorf("fields = %q", q.Get("fields"))
		}
		switch q.Get("pageToken") {
		case "":
			writeJSON(w, 200, `{"nextPageToken":"p2","files":[{"id":"1","name":"a"}]}`)
		case "p2":
			writeJSON(w, 200, `{"files":[{"id":"2","name":"b"},{"id":"3","name":"c"}]}`)
		default:
			t.Errorf("pageToken = %q", q.Get("pageToken"))
		}
	})
	files, err := c.List(context.Background(), "F'1")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(files) != 3 || files[2].ID != "3" {
		t.Fatalf("calls = %d, files = %+v", calls, files)
	}
}

func TestClientRetries(t *testing.T) {
	responses := []struct {
		code int
		body string
	}{
		{429, apiError(429, "rateLimitExceeded")},
		{503, apiError(503, "backendError")},
		{403, apiError(403, "userRateLimitExceeded")},
		{200, fileJSON},
	}
	var calls int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		resp := responses[calls]
		calls++
		writeJSON(w, resp.code, resp.body)
	})
	if _, err := c.Get(context.Background(), "F1"); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("calls = %d, want 4", calls)
	}
}

func TestClientDoesNotRetryOtherErrors(t *testing.T) {
	for _, code := range []int{400, 401, 404} {
		var calls int
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			writeJSON(w, code, apiError(code, "x"))
		})
		if _, err := c.Get(context.Background(), "F1"); err == nil {
			t.Fatalf("%d: want error", code)
		}
		if calls != 1 {
			t.Errorf("%d: calls = %d, want 1", code, calls)
		}
	}
}

func TestClientGivesUpAfterAttempts(t *testing.T) {
	var calls int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeJSON(w, 500, apiError(500, "backendError"))
	})
	if _, err := c.Get(context.Background(), "F1"); err == nil {
		t.Fatal("want error")
	}
	if calls != c.attempts {
		t.Fatalf("calls = %d, want %d", calls, c.attempts)
	}
}

// readMultipart returns the metadata and media parts of a multipart upload.
func readMultipart(t *testing.T, r *http.Request) (map[string]any, []byte, textproto.MIMEHeader) {
	t.Helper()
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	meta, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.NewDecoder(meta).Decode(&m); err != nil {
		t.Fatal(err)
	}
	media, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(media)
	if err != nil {
		t.Fatal(err)
	}
	return m, data, media.Header
}

func TestClientCreateRewindsOnRetry(t *testing.T) {
	var calls int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/upload/drive/v3/files" || r.URL.Query().Get("uploadType") != "multipart" {
			t.Errorf("%s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if r.URL.Query().Get("supportsAllDrives") != "true" {
			t.Error("supportsAllDrives missing")
		}
		meta, data, header := readMultipart(t, r)
		if meta["name"] != "notes.md" || fmt.Sprint(meta["parents"]) != "[P1]" {
			t.Errorf("metadata = %v", meta)
		}
		if string(data) != "hello" {
			t.Errorf("attempt %d: media = %q", calls, data)
		}
		if ct := header["Content-Type"]; len(ct) != 0 {
			t.Errorf("media Content-Type = %q, want none", ct)
		}
		if calls == 1 {
			writeJSON(w, 500, apiError(500, "backendError"))
			return
		}
		writeJSON(w, 200, `{"id":"N1","name":"notes.md","md5Checksum":"5d41402abc4b2a76b9719d911017c592"}`)
	})
	f, err := c.Create(context.Background(), "P1", "notes.md", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || f.ID != "N1" || f.MD5 != "5d41402abc4b2a76b9719d911017c592" {
		t.Fatalf("calls = %d, file = %+v", calls, f)
	}
}

func TestClientUpdate(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/upload/drive/v3/files/F1" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		meta, data, _ := readMultipart(t, r)
		if len(meta) != 0 || string(data) != "new" {
			t.Errorf("metadata = %v, media = %q", meta, data)
		}
		writeJSON(w, 200, fileJSON)
	})
	if _, err := c.Update(context.Background(), "F1", strings.NewReader("new")); err != nil {
		t.Fatal(err)
	}
}

func TestClientRenameAndCreateFolder(t *testing.T) {
	var got []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+strings.TrimSpace(string(body)))
		writeJSON(w, 200, `{"id":"X"}`)
	})
	ctx := context.Background()
	if _, err := c.Rename(ctx, "F1", "a_copy.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateFolder(ctx, "P1", "docs"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`PATCH /files/F1 {"name":"a_copy.txt"}`,
		`POST /files {"mimeType":"application/vnd.google-apps.folder","name":"docs","parents":["P1"]}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestClientDownload(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/files/F1" || r.URL.Query().Get("alt") != "media" {
			t.Errorf("%s?%s", r.URL.Path, r.URL.RawQuery)
		}
		io.WriteString(w, "content")
	})
	rc, err := c.Download(context.Background(), "F1")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "content" {
		t.Fatalf("data = %q", data)
	}
}

// Files larger than one chunk go through a resumable upload session.
func TestClientLargeUploadIsResumable(t *testing.T) {
	content := bytes.Repeat([]byte("0123456789abcdef"), (uploadChunkSize+uploadChunkSize/2)/16)
	var (
		mu       sync.Mutex
		received bytes.Buffer
		srvURL   string
	)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "POST" && r.URL.Query().Get("uploadType") == "resumable":
			w.Header().Set("Location", srvURL+"/session")
			w.WriteHeader(200)
		case r.URL.Path == "/session":
			n, _ := io.Copy(&received, r.Body)
			cr := r.Header.Get("Content-Range") // bytes start-end/total or */total
			if strings.HasSuffix(cr, "/*") {
				// The client asks for 200 plus this header instead of a 308.
				w.Header().Set("X-Http-Status-Code-Override", "308")
				w.Header().Set("Range", "bytes=0-"+strconv.Itoa(received.Len()-1))
				w.WriteHeader(200)
				return
			}
			if n == 0 {
				t.Errorf("empty final chunk, Content-Range %q", cr)
			}
			writeJSON(w, 200, `{"id":"BIG"}`)
		default:
			t.Errorf("unexpected %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			w.WriteHeader(500)
		}
	})
	srvURL = strings.TrimSuffix(c.svc.BasePath, "/")

	f, err := c.Create(context.Background(), "P1", "big.bin", bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if f.ID != "BIG" || !bytes.Equal(received.Bytes(), content) {
		t.Fatalf("id = %q, received %d of %d bytes", f.ID, received.Len(), len(content))
	}
}
