package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"testing"

	"github.com/overnite22/relay-public/internal/store"
)

func TestAttachmentWorkflow(t *testing.T) {
	e := newEnv(t, "")
	makeEntry := func(name string) store.Entry {
		entry, err := e.st.SaveEntry(context.Background(), store.Entry{Name: name, Kind: "ssh", Host: "localhost"})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	entry, keep := makeEntry("Source"), makeEntry("Destination")
	base := "/api/entries/" + entry.ID + "/attachments"
	name := "Procédure #1.html"
	data := []byte("<script>alert(1)</script>")
	upload := func(body []byte) *http.Response {
		req, err := http.NewRequest(http.MethodPost, e.srv.URL+base+"?"+url.Values{"name": {name}}.Encode(), bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Relay", "1")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	tooLarge := upload(make([]byte, maxAttachmentSize+1))
	tooLarge.Body.Close()
	if tooLarge.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize: %d", tooLarge.StatusCode)
	}

	res := upload(data)
	var attachment store.Attachment
	err := json.NewDecoder(res.Body).Decode(&attachment)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || err != nil || attachment.Name != name || attachment.Size != int64(len(data)) {
		t.Fatalf("upload: %d %+v %v", res.StatusCode, attachment, err)
	}

	var listed []store.Attachment
	if code := e.do(http.MethodGet, base, nil, &listed); code != http.StatusOK || len(listed) != 1 || listed[0].ID != attachment.ID {
		t.Fatalf("list: %d %+v", code, listed)
	}
	dest := "/api/entries/" + keep.ID + "/attachments/" + attachment.ID
	if code := e.do(http.MethodGet, dest, nil, nil); code != http.StatusNotFound {
		t.Fatalf("wrong entry: %d", code)
	}
	if code := e.do(http.MethodPost, "/api/entries/"+keep.ID+"/merge", map[string]any{"ids": []string{entry.ID}}, nil); code != http.StatusOK {
		t.Fatalf("merge: %d", code)
	}

	download, err := http.Get(e.srv.URL + dest)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Body.Close()
	mode, params, err := mime.ParseMediaType(download.Header.Get("Content-Disposition"))
	if download.StatusCode != http.StatusOK || download.Header.Get("Content-Type") != "application/octet-stream" ||
		err != nil || mode != "attachment" || params["filename"] != name {
		t.Fatalf("download: %d %v", download.StatusCode, download.Header)
	}
	got, err := io.ReadAll(download.Body)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("body: %q %v", got, err)
	}
	if code := e.do(http.MethodDelete, dest, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if code := e.do(http.MethodGet, dest, nil, nil); code != http.StatusNotFound {
		t.Fatalf("download after deletion: %d", code)
	}
}
