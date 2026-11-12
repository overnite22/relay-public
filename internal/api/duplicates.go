package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/overnite22/relay-public/internal/store"
)

// DuplicateGroup regroupe des entrées qui visent la même cible.
type DuplicateGroup struct {
	Key     string        `json:"key"`
	Label   string        `json:"label"`
	Entries []store.Entry `json:"entries"`
}

// resolver résout les noms d'hôte en IPv4 (avec cache et délai court), afin
// que « srv.lab » et « 192.0.2.10 » soient reconnus comme une même cible.
type resolver struct {
	mu    sync.Mutex
	cache map[string]string
}

func (r *resolver) resolve(ctx context.Context, host string) string {
	h := strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[] ")), ".")
	if h == "" || net.ParseIP(h) != nil {
		return h
	}
	r.mu.Lock()
	if v, ok := r.cache[h]; ok {
		r.mu.Unlock()
		return v
	}
	r.mu.Unlock()
	out := h
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if addrs, err := net.DefaultResolver.LookupIPAddr(cctx, h); err == nil {
		for _, a := range addrs {
			if v4 := a.IP.To4(); v4 != nil {
				out = v4.String()
				break
			}
		}
	}
	r.mu.Lock()
	r.cache[h] = out
	r.mu.Unlock()
	return out
}

func webTarget(raw string) (scheme, host, port, path string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", "", "", "", false
	}
	scheme = strings.ToLower(u.Scheme)
	port = u.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[scheme]
	}
	return scheme, u.Hostname(), port, strings.TrimRight(u.EscapedPath(), "/"), true
}

// findDuplicates calcule les groupes de doublons parmi les entrées.
func findDuplicates(ctx context.Context, entries []store.Entry) []DuplicateGroup {
	res := &resolver{cache: map[string]string{}}
	// Résolution DNS en parallèle (8 au plus).
	hosts := map[string]bool{}
	for _, e := range entries {
		switch e.Kind {
		case store.KindRDP, store.KindSSH, store.KindVNC:
			hosts[e.Host] = true
		case store.KindWeb:
			if _, h, _, _, ok := webTarget(e.URL); ok {
				hosts[h] = true
			}
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for h := range hosts {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res.resolve(ctx, h)
		}(h)
	}
	wg.Wait()

	groups := map[string]*DuplicateGroup{}
	for _, e := range entries {
		var key, label string
		switch e.Kind {
		case store.KindRDP, store.KindSSH, store.KindVNC:
			ip := res.resolve(ctx, e.Host)
			key = fmt.Sprintf("%s|%s|%d", e.Kind, ip, e.Port)
			label = fmt.Sprintf("%s %s:%d", strings.ToUpper(e.Kind), ip, e.Port)
		case store.KindWeb:
			scheme, h, port, path, ok := webTarget(e.URL)
			if !ok {
				continue
			}
			ip := res.resolve(ctx, h)
			key = fmt.Sprintf("web|%s|%s|%s|%s", scheme, ip, port, path)
			label = fmt.Sprintf("Web %s://%s:%s%s", scheme, ip, port, path)
		default:
			continue // consoles Proxmox : identifiées par VMID, gérées par la synchro
		}
		g := groups[key]
		if g == nil {
			g = &DuplicateGroup{Key: key, Label: label}
			groups[key] = g
		}
		g.Entries = append(g.Entries, e)
	}
	out := []DuplicateGroup{}
	for _, g := range groups {
		if len(g.Entries) < 2 {
			continue
		}
		// Entrées synchronisées d'abord : ce sont les meilleures candidates à garder.
		sort.SliceStable(g.Entries, func(i, j int) bool {
			si, sj := g.Entries[i].SourceID != nil, g.Entries[j].SourceID != nil
			if si != sj {
				return si
			}
			return g.Entries[i].Name < g.Entries[j].Name
		})
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func (s *Server) listDuplicates(w http.ResponseWriter, r *http.Request) {
	entries, err := s.opt.Store.ListEntries(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, findDuplicates(ctx, entries))
}

// mergeEntries garde l'entrée {id} et supprime les entrées ids, après avoir
// repris ce qui manque à l'entrée gardée (identifiant, utilisateur, notes,
// favori). Si l'entrée gardée n'est pas synchronisée, elle reprend
// l'identité de synchronisation de la première entrée synchronisée supprimée ;
// les autres sont exclues pour que la synchronisation ne les recrée pas.
func (s *Server) mergeEntries(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	keep, err := s.opt.Store.GetEntry(ctx, chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var others []store.Entry
	seen := map[string]bool{keep.ID: true}
	for _, id := range in.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		e, err := s.opt.Store.GetEntry(ctx, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		others = append(others, e)
	}
	if len(others) == 0 {
		s.fail(w, r, store.ErrInvalid{Msg: "aucune entrée à fusionner"})
		return
	}
	adopt := keep.SourceID == nil
	var adoptFrom *store.Entry
	for i, o := range others {
		if keep.CredentialID == nil && o.CredentialID != nil {
			keep.CredentialID = o.CredentialID
		}
		if keep.Username == "" && o.Username != "" {
			keep.Username = o.Username
		}
		if n := strings.TrimSpace(o.Notes); n != "" && !strings.Contains(keep.Notes, n) {
			if keep.Notes != "" {
				keep.Notes += "\n\n"
			}
			keep.Notes += "— Notes reprises de « " + o.Name + " » —\n" + n
		}
		keep.Favorite = keep.Favorite || o.Favorite
		if adopt && adoptFrom == nil && o.SourceID != nil && o.Kind == keep.Kind {
			adoptFrom = &others[i]
		}
	}
	if _, err := s.opt.Store.SaveEntry(ctx, keep); err != nil {
		s.fail(w, r, err)
		return
	}
	names := []string{}
	for _, o := range others {
		if err := s.opt.Store.DeleteMergedEntry(ctx, o.ID, keep.ID); err != nil {
			s.fail(w, r, err)
			return
		}
		names = append(names, o.Name)
		if o.SourceID == nil {
			continue
		}
		if adoptFrom != nil && o.ID == adoptFrom.ID {
			if err := s.opt.Store.AdoptSync(ctx, keep.ID, *o.SourceID, o.ExternalID); err != nil {
				s.fail(w, r, err)
				return
			}
			continue
		}
		if err := s.opt.Store.ExcludeFromSync(ctx, *o.SourceID, o.ExternalID); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.audit(r, "entry.merge", keep.Name, "fusion de "+strings.Join(names, ", "))
	e, _ := s.opt.Store.GetEntry(ctx, keep.ID)
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) clearExclusions(w http.ResponseWriter, r *http.Request) {
	n, err := s.opt.Store.ClearSyncExclusions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": strconv.FormatInt(n, 10) + " entrée(s) réautorisée(s) à la prochaine synchronisation"})
}
