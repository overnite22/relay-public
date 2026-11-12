package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"

	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/sshexec"
	"github.com/overnite22/relay-public/internal/store"
)

func (s *Server) commandRoutes(r chi.Router) {
	r.Post("/commands/run", s.startRun)
	r.Get("/commands/runs", s.listRuns)
	r.Get("/commands/runs/{id}", s.getRun)
	r.Post("/commands/runs/{id}/cancel", s.cancelRun)
	r.Get("/commands/saved", s.listSaved)
	r.Post("/commands/saved", s.saveSaved)
	r.Put("/commands/saved/{id}", s.saveSaved)
	r.Delete("/commands/saved/{id}", s.deleteSaved)
	r.Post("/known-hosts/forget", s.forgetHost)
}

// activeRun est une exécution en cours, consultable pendant son déroulement.
type activeRun struct {
	mu      sync.Mutex
	run     store.CommandRun
	results []sshexec.Result
	cancel  context.CancelFunc
	done    bool
}

func (a *activeRun) snapshot() store.CommandRun {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.run
	r.Results, _ = json.Marshal(a.results)
	return r
}

type runRegistry struct {
	mu   sync.Mutex
	runs map[string]*activeRun
}

// storeKeys adapte le store à l'interface sshexec.HostKeys.
type storeKeys struct{ st *store.Store }

func (k storeKeys) Get(ctx context.Context, hp string) ([]byte, bool, error) {
	return k.st.KnownHostKey(ctx, hp)
}

func (k storeKeys) Put(ctx context.Context, hp string, key ssh.PublicKey) error {
	return k.st.RememberHostKey(context.WithoutCancel(ctx), hp, key.Marshal(), ssh.FingerprintSHA256(key))
}

func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EntryIDs    []string `json:"entryIds"`
		Command     string   `json:"command"`
		Sudo        bool     `json:"sudo"`
		TimeoutSec  int      `json:"timeoutSec"`
		Concurrency int      `json:"concurrency"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.Command = strings.TrimSpace(in.Command)
	if in.Command == "" {
		s.fail(w, r, store.ErrInvalid{Msg: "la commande est vide"})
		return
	}
	if len(in.EntryIDs) == 0 || len(in.EntryIDs) > 500 {
		s.fail(w, r, store.ErrInvalid{Msg: "choisissez entre 1 et 500 serveurs"})
		return
	}
	ctx := r.Context()
	var targets []sshexec.Target
	seen := map[string]bool{}
	for _, id := range in.EntryIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		e, err := s.opt.Store.GetEntry(ctx, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if e.Kind != store.KindSSH {
			s.fail(w, r, store.ErrInvalid{Msg: "« " + e.Name + " » n'est pas une entrée SSH"})
			return
		}
		sec, err := s.sessionSecrets(ctx, e)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		t := sshexec.Target{EntryID: e.ID, Name: e.Name, Host: e.Host, Port: e.Port, User: sec.username, Password: sec.password, PrivateKey: sec.privateKey, Passphrase: sec.passphrase}
		targets = append(targets, t)
	}
	timeout := time.Duration(in.TimeoutSec) * time.Second
	if timeout <= 0 || timeout > time.Hour {
		timeout = 60 * time.Second
	}
	conc := in.Concurrency
	if conc <= 0 || conc > 50 {
		conc = 10
	}

	user := auth.User(ctx)
	a := &activeRun{run: store.CommandRun{ID: store.NewID(), At: time.Now().UTC(), User: user, Command: in.Command, Sudo: in.Sudo, Targets: len(targets)}}
	a.results = make([]sshexec.Result, len(targets))
	for i, t := range targets {
		a.results[i] = sshexec.Result{EntryID: t.EntryID, Name: t.Name, Address: t.Host, Status: sshexec.StatusPending}
	}
	runCtx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	s.runs.mu.Lock()
	s.runs.runs[a.run.ID] = a
	s.runs.mu.Unlock()
	if err := s.opt.Store.SaveRun(ctx, a.snapshot()); err != nil {
		cancel()
		s.runs.mu.Lock()
		delete(s.runs.runs, a.run.ID)
		s.runs.mu.Unlock()
		s.fail(w, r, err)
		return
	}
	s.audit(r, "command.run", in.Command, fmt.Sprintf("%d serveur(s)%s", len(targets), map[bool]string{true: ", sudo"}[in.Sudo]))

	s.dataGuard.RLock()
	go func() {
		defer s.dataGuard.RUnlock()
		defer cancel()
		sshexec.Run(runCtx, targets, sshexec.Options{Command: in.Command, Sudo: in.Sudo, Timeout: timeout, Concurrency: conc},
			storeKeys{s.opt.Store}, func(i int, res sshexec.Result) {
				a.mu.Lock()
				a.results[i] = res
				a.mu.Unlock()
			})
		a.mu.Lock()
		fin := time.Now().UTC()
		a.run.FinishedAt = &fin
		for _, res := range a.results {
			switch res.Status {
			case sshexec.StatusOK:
				a.run.OK++
			default:
				a.run.Failed++
			}
		}
		a.done = true
		a.mu.Unlock()
		if err := s.opt.Store.SaveRun(context.Background(), a.snapshot()); err != nil {
			s.log.Error("historique des commandes", "err", err)
		}
		// L'exécution reste consultable en mémoire quelques minutes, puis via l'historique.
		time.AfterFunc(10*time.Minute, func() {
			s.runs.mu.Lock()
			delete(s.runs.runs, a.run.ID)
			s.runs.mu.Unlock()
		})
	}()
	writeJSON(w, http.StatusOK, a.snapshot())
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s.runs.mu.Lock()
	a := s.runs.runs[id]
	s.runs.mu.Unlock()
	if a != nil {
		writeJSON(w, http.StatusOK, a.snapshot())
		return
	}
	run, err := s.opt.Store.GetRun(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	s.runs.mu.Lock()
	a := s.runs.runs[chi.URLParam(r, "id")]
	s.runs.mu.Unlock()
	if a == nil {
		writeErr(w, http.StatusNotFound, "exécution terminée ou introuvable")
		return
	}
	a.cancel()
	s.audit(r, "command.cancel", a.run.Command, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.opt.Store.ListRuns(r.Context(), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) listSaved(w http.ResponseWriter, r *http.Request) {
	list, err := s.opt.Store.ListSavedCommands(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) saveSaved(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		Command string `json:"command"`
		Sudo    bool   `json:"sudo"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	c, err := s.opt.Store.SaveCommand(r.Context(), store.SavedCommand{ID: chi.URLParam(r, "id"), Name: in.Name, Command: in.Command, Sudo: in.Sudo})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "command.save", c.Name, "")
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deleteSaved(w http.ResponseWriter, r *http.Request) {
	if err := s.opt.Store.DeleteSavedCommand(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) forgetHost(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Address string `json:"address"`
	}
	if err := decode(r, &in); err != nil || in.Address == "" {
		s.fail(w, r, store.ErrInvalid{Msg: "adresse manquante"})
		return
	}
	if err := s.opt.Store.ForgetHostKey(r.Context(), in.Address); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "hostkey.forget", in.Address, "")
	w.WriteHeader(http.StatusNoContent)
}
