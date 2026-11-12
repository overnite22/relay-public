package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/store"
)

func (s *Server) setFolderCredentials(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Credentials map[string]*string `json:"credentials"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.opt.Store.SetFolderCredentials(r.Context(), id, in.Credentials); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "folder.credentials", id, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	items, err := s.opt.Store.ListWorkspaces(r.Context(), auth.User(r.Context()))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (s *Server) saveWorkspace(w http.ResponseWriter, r *http.Request) {
	var item store.Workspace
	if err := decode(r, &item); err != nil {
		s.fail(w, r, err)
		return
	}
	item.ID = chi.URLParam(r, "id")
	item, err := s.opt.Store.SaveWorkspace(r.Context(), auth.User(r.Context()), item)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "workspace.save", item.Name, "")
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	if err := s.opt.Store.DeleteWorkspace(r.Context(), auth.User(r.Context()), chi.URLParam(r, "id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
