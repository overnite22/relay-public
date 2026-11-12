package api

import (
	"context"
	"net"
	"net/http"
	"testing"

	"github.com/overnite22/relay-public/internal/store"
)

func TestConnectionDiagnosticChecksTheStoredPort(t *testing.T) {
	e := newEnv(t, "")
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	entry, err := e.st.SaveEntry(context.Background(), store.Entry{
		Name: "Test SSH", Kind: store.KindSSH, Host: "127.0.0.1", Port: port,
	})
	if err != nil {
		t.Fatal(err)
	}
	monitor := New(Options{Store: e.st})
	monitor.refreshConnectionStatus(context.Background())
	if result := monitor.diagnostics.byAddress[ln.Addr().String()]; result.Status != "reachable" || result.CheckedAt.IsZero() {
		t.Fatalf("automatic check: %+v", result)
	}
	path := "/api/entries/" + entry.ID + "/test-connection"
	var result connectionTestResult
	if code := e.do(http.MethodPost, path, nil, &result); code != http.StatusOK ||
		result.Status != "reachable" || result.Address != ln.Addr().String() || result.IP != "127.0.0.1" {
		t.Fatalf("open port: %d %+v", code, result)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	result = connectionTestResult{}
	if code := e.do(http.MethodPost, path, nil, &result); code != http.StatusOK || result.Status != "refused" {
		t.Fatalf("closed port: %d %+v", code, result)
	}

	var cached map[string]connectionTestResult
	if code := e.do(http.MethodGet, "/api/connection-status", nil, &cached); code != http.StatusOK || cached[result.Address].Status != "refused" {
		t.Fatalf("manual result in status cache: %d %+v", code, cached)
	}
	if err := e.st.DeleteEntry(context.Background(), entry.ID); err != nil {
		t.Fatal(err)
	}
	monitor.refreshConnectionStatus(context.Background())
	if len(monitor.diagnostics.byAddress) != 0 {
		t.Fatal("removed endpoint is still monitored")
	}

	web, err := e.st.SaveEntry(context.Background(), store.Entry{
		Name: "Test web", Kind: store.KindWeb, URL: "https://localhost",
	})
	if err != nil {
		t.Fatal(err)
	}
	if code := e.do(http.MethodPost, "/api/entries/"+web.ID+"/test-connection", nil, nil); code != http.StatusBadRequest {
		t.Fatalf("unsupported entry: %d", code)
	}
}
