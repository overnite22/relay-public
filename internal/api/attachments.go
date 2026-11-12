package api

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/overnite22/relay-public/internal/store"
)

const maxAttachmentSize = 10 << 20

func (s *Server) listAttachments(w http.ResponseWriter, r *http.Request) {
	entryID := chi.URLParam(r, "id")
	if _, err := s.opt.Store.GetEntry(r.Context(), entryID); err != nil {
		s.fail(w, r, err)
		return
	}
	items, err := s.opt.Store.ListAttachments(r.Context(), entryID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) addAttachment(w http.ResponseWriter, r *http.Request) {
	entryID := chi.URLParam(r, "id")
	name := strings.TrimSpace(path.Base(strings.ReplaceAll(r.URL.Query().Get("name"), "\\", "/")))
	if name == "." || name == ".." || name == "/" || name == "" || len(name) > 255 ||
		!utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		s.fail(w, r, store.ErrInvalid{Msg: "nom de fichier invalide"})
		return
	}
	if r.ContentLength > maxAttachmentSize {
		writeErr(w, http.StatusRequestEntityTooLarge, "le fichier dépasse 10 Mio")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAttachmentSize))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeErr(w, http.StatusRequestEntityTooLarge, "le fichier dépasse 10 Mio")
		return
	}
	if err != nil || len(data) == 0 {
		s.fail(w, r, store.ErrInvalid{Msg: "fichier vide ou illisible"})
		return
	}
	item, err := s.opt.Store.AddAttachment(r.Context(), entryID, name, data)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "attachment.add", entryID, item.Name)
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) downloadAttachment(w http.ResponseWriter, r *http.Request) {
	name, data, err := s.opt.Store.ReadAttachment(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "attachmentID"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func (s *Server) deleteAttachment(w http.ResponseWriter, r *http.Request) {
	entryID, id := chi.URLParam(r, "id"), chi.URLParam(r, "attachmentID")
	if err := s.opt.Store.DeleteAttachment(r.Context(), entryID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "attachment.delete", entryID, id)
	w.WriteHeader(http.StatusNoContent)
}
