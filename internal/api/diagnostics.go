package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/overnite22/relay-public/internal/store"
)

type connectionTestResult struct {
	Address    string    `json:"address"`
	IP         string    `json:"ip,omitempty"`
	Status     string    `json:"status"`
	DurationMs int64     `json:"durationMs"`
	CheckedAt  time.Time `json:"checkedAt"`
}

func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	entry, err := s.opt.Store.GetEntry(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !testableEntry(entry) {
		s.fail(w, r, store.ErrInvalid{Msg: "ce type d’entrée ne permet pas de tester un port de session"})
		return
	}
	result := checkTCP(r.Context(), connectionAddress(entry), 5*time.Second)
	if r.Context().Err() == nil {
		s.diagnostics.set(result)
	}
	writeJSON(w, http.StatusOK, result)
}

func testableEntry(entry store.Entry) bool {
	return entry.Kind == store.KindSSH || entry.Kind == store.KindRDP || entry.Kind == store.KindVNC
}

func connectionAddress(entry store.Entry) string {
	host := entry.Host
	if len(host) >= 2 && host[0] == '[' && host[len(host)-1] == ']' {
		host = host[1 : len(host)-1]
	}
	return net.JoinHostPort(host, strconv.Itoa(entry.Port))
}

func checkTCP(ctx context.Context, address string, timeout time.Duration) connectionTestResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	result := connectionTestResult{Address: address, DurationMs: time.Since(start).Milliseconds(), CheckedAt: time.Now().UTC()}
	if err == nil {
		result.Status = "reachable"
		result.IP = conn.RemoteAddr().(*net.TCPAddr).IP.String()
		_ = conn.Close()
	} else {
		result.Status = connectionStatus(err)
	}
	return result
}

func connectionStatus(err error) string {
	var networkErr net.Error
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &networkErr) && networkErr.Timeout():
		return "timeout"
	case errors.As(err, &dnsErr):
		return "dns_error"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "unreachable"
	default:
		return "error"
	}
}

const (
	diagnosticInterval = time.Minute
	diagnosticMaxAge   = 3 * time.Minute
	diagnosticWorkers  = 8
)

type diagnosticCache struct {
	mu        sync.RWMutex
	byAddress map[string]connectionTestResult
}

func (c *diagnosticCache) set(result connectionTestResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byAddress == nil {
		c.byAddress = make(map[string]connectionTestResult)
	}
	c.byAddress[result.Address] = result
}

func (s *Server) listConnectionStatus(w http.ResponseWriter, _ *http.Request) {
	recent := make(map[string]connectionTestResult)
	s.diagnostics.mu.RLock()
	for address, result := range s.diagnostics.byAddress {
		if time.Since(result.CheckedAt) < diagnosticMaxAge {
			recent[address] = result
		}
	}
	s.diagnostics.mu.RUnlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, recent)
}

// MonitorConnections refreshes the TCP indicators without opening sessions.
func (s *Server) MonitorConnections(ctx context.Context) {
	ticker := time.NewTicker(diagnosticInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		s.refreshConnectionStatus(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) refreshConnectionStatus(ctx context.Context) {
	entries, err := s.opt.Store.ListEntries(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("lecture de l’inventaire pour le test TCP", "err", err)
		}
		return
	}
	addresses := make(map[string]bool)
	for _, entry := range entries {
		if testableEntry(entry) {
			addresses[connectionAddress(entry)] = true
		}
	}

	s.diagnostics.mu.Lock()
	for address := range s.diagnostics.byAddress {
		if !addresses[address] {
			delete(s.diagnostics.byAddress, address)
		}
	}
	s.diagnostics.mu.Unlock()

	jobs := make(chan string)
	var workers sync.WaitGroup
	for range min(diagnosticWorkers, len(addresses)) {
		workers.Go(func() {
			for address := range jobs {
				result := checkTCP(ctx, address, 3*time.Second)
				if ctx.Err() == nil {
					s.diagnostics.set(result)
				}
			}
		})
	}
	defer workers.Wait()
	defer close(jobs)
	for address := range addresses {
		select {
		case jobs <- address:
		case <-ctx.Done():
			return
		}
	}
}
