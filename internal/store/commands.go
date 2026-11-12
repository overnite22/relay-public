package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const commandsSchema = `
CREATE TABLE IF NOT EXISTS command_runs (
	id TEXT PRIMARY KEY,
	at INTEGER NOT NULL,
	finished_at INTEGER,
	user TEXT NOT NULL,
	command TEXT NOT NULL,
	sudo INTEGER NOT NULL DEFAULT 0,
	targets INTEGER NOT NULL,
	ok INTEGER NOT NULL DEFAULT 0,
	failed INTEGER NOT NULL DEFAULT 0,
	results TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS command_runs_at ON command_runs(at);
CREATE TABLE IF NOT EXISTS saved_commands (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	command TEXT NOT NULL,
	sudo INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS ssh_known_hosts (
	host_port TEXT PRIMARY KEY,
	key BLOB NOT NULL,
	fingerprint TEXT NOT NULL,
	first_seen INTEGER NOT NULL
);
`

// CommandRun est une exécution de commande (historique).
type CommandRun struct {
	ID         string          `json:"id"`
	At         time.Time       `json:"at"`
	FinishedAt *time.Time      `json:"finishedAt"`
	User       string          `json:"user"`
	Command    string          `json:"command"`
	Sudo       bool            `json:"sudo"`
	Targets    int             `json:"targets"`
	OK         int             `json:"ok"`
	Failed     int             `json:"failed"`
	Results    json.RawMessage `json:"results,omitempty"`
}

// SavedCommand est une commande enregistrée (bibliothèque).
type SavedCommand struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Command   string    `json:"command"`
	Sudo      bool      `json:"sudo"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SaveRun enregistre ou met à jour une exécution.
func (s *Store) SaveRun(ctx context.Context, r CommandRun) error {
	var fin any
	if r.FinishedAt != nil {
		fin = r.FinishedAt.UnixMilli()
	}
	if len(r.Results) == 0 {
		r.Results = json.RawMessage(`[]`)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO command_runs (id, at, finished_at, user, command, sudo, targets, ok, failed, results)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET finished_at = excluded.finished_at, ok = excluded.ok, failed = excluded.failed, results = excluded.results`,
		r.ID, r.At.UnixMilli(), fin, r.User, r.Command, b2i(r.Sudo), r.Targets, r.OK, r.Failed, string(r.Results))
	if err == nil {
		// Historique borné aux 200 dernières exécutions.
		_, err = s.db.ExecContext(ctx, `DELETE FROM command_runs WHERE id NOT IN (SELECT id FROM command_runs ORDER BY at DESC LIMIT 200)`)
	}
	return err
}

func scanRun(sc interface{ Scan(...any) error }, withResults bool) (CommandRun, error) {
	var r CommandRun
	var at int64
	var fin sql.NullInt64
	var sudo int
	var results string
	err := sc.Scan(&r.ID, &at, &fin, &r.User, &r.Command, &sudo, &r.Targets, &r.OK, &r.Failed, &results)
	if err != nil {
		return r, err
	}
	r.At, r.Sudo = ts(at), sudo != 0
	if fin.Valid {
		t := ts(fin.Int64)
		r.FinishedAt = &t
	}
	if withResults {
		r.Results = json.RawMessage(results)
	}
	return r, nil
}

// ListRuns renvoie l'historique (sans le détail des résultats).
func (s *Store) ListRuns(ctx context.Context, limit int) ([]CommandRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, finished_at, user, command, sudo, targets, ok, failed, results FROM command_runs ORDER BY at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CommandRun{}
	for rows.Next() {
		r, err := scanRun(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRun renvoie une exécution avec ses résultats.
func (s *Store) GetRun(ctx context.Context, id string) (CommandRun, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, `SELECT id, at, finished_at, user, command, sudo, targets, ok, failed, results FROM command_runs WHERE id = ?`, id), true)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// ListSavedCommands renvoie la bibliothèque de commandes.
func (s *Store) ListSavedCommands(ctx context.Context) ([]SavedCommand, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, command, sudo, created_at, updated_at FROM saved_commands ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedCommand{}
	for rows.Next() {
		var c SavedCommand
		var sudo int
		var cr, up int64
		if err := rows.Scan(&c.ID, &c.Name, &c.Command, &sudo, &cr, &up); err != nil {
			return nil, err
		}
		c.Sudo, c.CreatedAt, c.UpdatedAt = sudo != 0, ts(cr), ts(up)
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveCommand crée (ID vide) ou met à jour une commande enregistrée.
func (s *Store) SaveCommand(ctx context.Context, c SavedCommand) (SavedCommand, error) {
	c.Name = clean(c.Name)
	if c.Name == "" || clean(c.Command) == "" {
		return c, ErrInvalid{"le nom et la commande sont obligatoires"}
	}
	t := now()
	if c.ID == "" {
		c.ID = NewID()
		if _, err := s.db.ExecContext(ctx, `INSERT INTO saved_commands (id, name, command, sudo, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			c.ID, c.Name, c.Command, b2i(c.Sudo), t, t); err != nil {
			return c, err
		}
	} else {
		res, err := s.db.ExecContext(ctx, `UPDATE saved_commands SET name = ?, command = ?, sudo = ?, updated_at = ? WHERE id = ?`, c.Name, c.Command, b2i(c.Sudo), t, c.ID)
		if err != nil {
			return c, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return c, ErrNotFound
		}
	}
	c.CreatedAt, c.UpdatedAt = ts(t), ts(t)
	return c, nil
}

// DeleteSavedCommand supprime une commande enregistrée.
func (s *Store) DeleteSavedCommand(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM saved_commands WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// KnownHostKey renvoie la clé d'hôte mémorisée pour "hôte:port".
func (s *Store) KnownHostKey(ctx context.Context, hostPort string) ([]byte, bool, error) {
	var key []byte
	err := s.db.QueryRowContext(ctx, `SELECT key FROM ssh_known_hosts WHERE host_port = ?`, hostPort).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return key, err == nil, err
}

// RememberHostKey mémorise une clé d'hôte (première connexion).
func (s *Store) RememberHostKey(ctx context.Context, hostPort string, key []byte, fingerprint string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO ssh_known_hosts (host_port, key, fingerprint, first_seen) VALUES (?, ?, ?, ?)`,
		hostPort, key, fingerprint, now())
	if err != nil {
		return err
	}
	stored, _, err := s.KnownHostKey(ctx, hostPort)
	if err != nil {
		return err
	}
	if !bytes.Equal(stored, key) {
		return errors.New("clé d'hôte modifiée pendant la première connexion")
	}
	return nil
}

// ForgetHostKey oublie la clé d'hôte (après un changement légitime).
func (s *Store) ForgetHostKey(ctx context.Context, hostPort string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ssh_known_hosts WHERE host_port = ?`, hostPort)
	return err
}
