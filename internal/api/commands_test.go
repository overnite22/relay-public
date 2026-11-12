package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/overnite22/relay-public/internal/store"
)

func TestCommandWorkflow(t *testing.T) {
	e := newEnv(t, "")
	var saved store.SavedCommand
	if code := e.do("POST", "/api/commands/saved", map[string]any{"name": "Inventaire", "command": "uname -a"}, &saved); code != http.StatusOK {
		t.Fatalf("save: %d", code)
	}
	var library []store.SavedCommand
	e.do("GET", "/api/commands/saved", nil, &library)
	if len(library) != 1 || library[0].Command != "uname -a" {
		t.Fatalf("library: %+v", library)
	}
	if code := e.do("POST", "/api/commands/run", map[string]any{"command": "", "entryIds": []string{"missing"}}, nil); code != http.StatusBadRequest {
		t.Fatalf("empty: %d", code)
	}
	entry, err := e.st.SaveEntry(context.Background(), store.Entry{Name: "Sans identifiant", Kind: store.KindSSH, Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	var run store.CommandRun
	if code := e.do("POST", "/api/commands/run", map[string]any{"command": "uname -a", "entryIds": []string{entry.ID, entry.ID}}, &run); code != http.StatusOK {
		t.Fatalf("run: %d", code)
	}
	if run.Targets != 1 {
		t.Fatalf("targets: %d", run.Targets)
	}
	deadline := time.Now().Add(3 * time.Second)
	for run.FinishedAt == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		e.do("GET", "/api/commands/runs/"+run.ID, nil, &run)
	}
	if run.FinishedAt == nil || run.Failed != 1 {
		t.Fatalf("finished: %+v", run)
	}
	var history []store.CommandRun
	e.do("GET", "/api/commands/runs", nil, &history)
	if len(history) != 1 || history[0].Failed != 1 {
		t.Fatalf("history: %+v", history)
	}
	if code := e.do("DELETE", "/api/commands/saved/"+saved.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
}
