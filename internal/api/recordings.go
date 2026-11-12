package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/guac"
	"github.com/overnite22/relay-public/internal/store"
)

const recordingLimit = 64 << 20

type recordingMeta struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	At      time.Time `json:"at"`
	Size    int64     `json:"size"`
	Limited bool      `json:"limited"`
}

type sessionCapture struct {
	dir       string
	file      *os.File
	meta      recordingMeta
	recording *guac.Recording
}

func (s *Server) recordingsPath(user string) string {
	sum := sha256.Sum256([]byte(user))
	return filepath.Join(s.opt.RecordingsDir, hex.EncodeToString(sum[:]))
}

func (s *Server) startRecording(p pendingSession, user string, at time.Time) (*sessionCapture, error) {
	root := s.recordingsPath(user)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(root, ".capture-")
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "session.guac"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.Remove(dir)
		return nil, err
	}
	return &sessionCapture{
		dir: dir, file: file,
		meta:      recordingMeta{ID: store.NewID(), Name: p.name, Kind: p.cfg.Protocol, At: at.UTC()},
		recording: guac.NewRecording(file, recordingLimit),
	}, nil
}

func (c *sessionCapture) discard() {
	_ = c.file.Close()
	_ = os.RemoveAll(c.dir)
}

func (c *sessionCapture) finish() error {
	size, limited, err := c.recording.Result()
	if err != nil || size == 0 {
		return err
	}
	if err := c.file.Truncate(size); err != nil {
		return err
	}
	if err := c.file.Close(); err != nil {
		return err
	}
	c.meta.Size, c.meta.Limited = size, limited
	data, err := json.Marshal(c.meta)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(c.dir, "metadata.json"), data, 0o600); err != nil {
		return err
	}
	return os.Rename(c.dir, filepath.Join(filepath.Dir(c.dir), c.meta.ID))
}

func validRecordingID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func (s *Server) listRecordings(w http.ResponseWriter, r *http.Request) {
	items := []recordingMeta{}
	if s.opt.RecordingsDir == "" {
		writeJSON(w, http.StatusOK, items)
		return
	}
	root := s.recordingsPath(auth.User(r.Context()))
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.fail(w, r, err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validRecordingID(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name(), "metadata.json"))
		var meta recordingMeta
		if err == nil && json.Unmarshal(data, &meta) == nil && meta.ID == entry.Name() {
			items = append(items, meta)
		}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) recordingDir(r *http.Request) (string, error) {
	id := chi.URLParam(r, "id")
	if s.opt.RecordingsDir == "" || !validRecordingID(id) {
		return "", store.ErrNotFound
	}
	return filepath.Join(s.recordingsPath(auth.User(r.Context())), id), nil
}

func (s *Server) readRecording(w http.ResponseWriter, r *http.Request) {
	dir, err := s.recordingDir(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	file, err := os.Open(filepath.Join(dir, "session.guac"))
	if errors.Is(err, os.ErrNotExist) {
		err = store.ErrNotFound
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "session.guac", info.ModTime(), file)
}

func (s *Server) deleteRecording(w http.ResponseWriter, r *http.Request) {
	dir, err := s.recordingDir(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "recording.delete", filepath.Base(dir), "")
	w.WriteHeader(http.StatusNoContent)
}
