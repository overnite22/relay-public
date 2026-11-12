package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Types de sources de synchronisation.
const (
	SourceProxmox = "proxmox"
	SourceNetBox  = "netbox"
)

// Source est une source de synchronisation (cluster Proxmox, instance NetBox).
type Source struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	URL         string          `json:"url"`
	TokenID     string          `json:"tokenId"` // Proxmox : utilisateur@royaume!jeton
	HasSecret   bool            `json:"hasSecret"`
	VerifyTLS   bool            `json:"verifyTls"`
	FolderID    *string         `json:"folderId"`
	Options     json.RawMessage `json:"options"`
	IntervalMin int             `json:"intervalMin"` // 0 = synchronisation manuelle
	LastSyncAt  *time.Time      `json:"lastSyncAt"`
	LastStatus  string          `json:"lastStatus"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`

	SecretEnc string `json:"-"`
}

// migrate applique les évolutions de schéma idempotentes.
func migrate(db *sql.DB) error {
	cols := map[string]bool{}
	rows, err := db.Query(`PRAGMA table_info(entries)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	rows.Close()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sources (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			token_id TEXT NOT NULL DEFAULT '',
			secret_enc TEXT NOT NULL DEFAULT '',
			verify_tls INTEGER NOT NULL DEFAULT 1,
			folder_id TEXT REFERENCES folders(id) ON DELETE SET NULL,
			options TEXT NOT NULL DEFAULT '{}',
			interval_min INTEGER NOT NULL DEFAULT 60,
			last_sync_at INTEGER,
			last_status TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
	}
	if !cols["source_id"] {
		stmts = append(stmts, `ALTER TABLE entries ADD COLUMN source_id TEXT REFERENCES sources(id) ON DELETE SET NULL`)
	}
	if !cols["external_id"] {
		stmts = append(stmts, `ALTER TABLE entries ADD COLUMN external_id TEXT NOT NULL DEFAULT ''`)
	}
	stmts = append(stmts, `CREATE INDEX IF NOT EXISTS entries_source ON entries(source_id, external_id)`)
	stmts = append(stmts, `CREATE TABLE IF NOT EXISTS sync_exclusions (
		source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
		external_id TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (source_id, external_id)
	)`)
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

const sourceCols = `id, kind, name, url, token_id, secret_enc, verify_tls, folder_id, options, interval_min, last_sync_at, last_status, created_at, updated_at`

func scanSource(sc interface{ Scan(...any) error }) (Source, error) {
	var s Source
	var folder sql.NullString
	var opts string
	var verify int
	var last sql.NullInt64
	var c, u int64
	err := sc.Scan(&s.ID, &s.Kind, &s.Name, &s.URL, &s.TokenID, &s.SecretEnc, &verify, &folder, &opts, &s.IntervalMin, &last, &s.LastStatus, &c, &u)
	if err != nil {
		return s, err
	}
	s.FolderID, s.VerifyTLS, s.Options = strPtr(folder), verify != 0, json.RawMessage(opts)
	s.HasSecret = s.SecretEnc != ""
	if last.Valid {
		t := ts(last.Int64)
		s.LastSyncAt = &t
	}
	s.CreatedAt, s.UpdatedAt = ts(c), ts(u)
	return s, nil
}

// ListSources renvoie les sources de synchronisation.
func (s *Store) ListSources(ctx context.Context) ([]Source, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sourceCols+` FROM sources ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// GetSource renvoie une source.
func (s *Store) GetSource(ctx context.Context, id string) (Source, error) {
	src, err := scanSource(s.db.QueryRowContext(ctx, `SELECT `+sourceCols+` FROM sources WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return src, ErrNotFound
	}
	return src, err
}

// SaveSource crée ou met à jour une source (l'ID est fixé par l'appelant,
// car le secret chiffré y est lié).
func (s *Store) SaveSource(ctx context.Context, src Source) (Source, error) {
	src.Name, src.URL, src.TokenID = clean(src.Name), strings.TrimRight(clean(src.URL), "/"), clean(src.TokenID)
	if src.ID == "" {
		return src, errors.New("store: ID de source manquant")
	}
	if src.Kind != SourceProxmox && src.Kind != SourceNetBox {
		return src, ErrInvalid{"type de source inconnu"}
	}
	if src.Name == "" {
		return src, ErrInvalid{"le nom de la source est obligatoire"}
	}
	if !strings.HasPrefix(src.URL, "https://") && !strings.HasPrefix(src.URL, "http://") {
		return src, ErrInvalid{"l'URL doit commencer par https:// ou http://"}
	}
	if src.Kind == SourceProxmox && !strings.Contains(src.TokenID, "!") {
		return src, ErrInvalid{"identifiant de jeton Proxmox attendu au format utilisateur@royaume!nom-du-jeton"}
	}
	if src.IntervalMin < 0 || src.IntervalMin > 7*24*60 {
		return src, ErrInvalid{"intervalle de synchronisation invalide"}
	}
	if len(src.Options) == 0 || string(src.Options) == "null" {
		src.Options = json.RawMessage(`{}`)
	}
	var obj map[string]any
	if err := json.Unmarshal(src.Options, &obj); err != nil {
		return src, ErrInvalid{"options invalides"}
	}
	verify := 0
	if src.VerifyTLS {
		verify = 1
	}
	t := now()
	res, err := s.db.ExecContext(ctx, `UPDATE sources SET kind = ?, name = ?, url = ?, token_id = ?, secret_enc = ?, verify_tls = ?, folder_id = ?, options = ?, interval_min = ?, updated_at = ? WHERE id = ?`,
		src.Kind, src.Name, src.URL, src.TokenID, src.SecretEnc, verify, nullable(src.FolderID), string(src.Options), src.IntervalMin, t, src.ID)
	if err != nil {
		return src, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_, err = s.db.ExecContext(ctx, `INSERT INTO sources (id, kind, name, url, token_id, secret_enc, verify_tls, folder_id, options, interval_min, last_status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?)`,
			src.ID, src.Kind, src.Name, src.URL, src.TokenID, src.SecretEnc, verify, nullable(src.FolderID), string(src.Options), src.IntervalMin, t, t)
		if err != nil {
			return src, err
		}
	}
	return s.GetSource(ctx, src.ID)
}

// DeleteSource supprime une source ; ses entrées restent, détachées.
func (s *Store) DeleteSource(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sources WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSourceFolder mémorise le dossier racine d'une source.
func (s *Store) SetSourceFolder(ctx context.Context, id, folderID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sources SET folder_id = ? WHERE id = ?`, folderID, id)
	return err
}

// SetSourceStatus enregistre le résultat de la dernière synchronisation.
func (s *Store) SetSourceStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sources SET last_sync_at = ?, last_status = ? WHERE id = ?`, now(), status, id)
	return err
}

// ListSourceEntries renvoie les entrées créées par une source.
func (s *Store) ListSourceEntries(ctx context.Context, sourceID string) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryCols+` FROM entries WHERE source_id = ?`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EnsureFolder renvoie l'ID du sous-dossier name de parent, en le créant si besoin.
func (s *Store) EnsureFolder(ctx context.Context, parentID *string, name string) (string, error) {
	name = clean(name)
	if name == "" {
		name = "(sans nom)"
	}
	var id string
	var err error
	if parentID == nil {
		err = s.db.QueryRowContext(ctx, `SELECT id FROM folders WHERE parent_id IS NULL AND name = ? COLLATE NOCASE LIMIT 1`, name).Scan(&id)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT id FROM folders WHERE parent_id = ? AND name = ? COLLATE NOCASE LIMIT 1`, *parentID, name).Scan(&id)
	}
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	f, err := s.SaveFolder(ctx, Folder{Name: name, ParentID: parentID})
	return f.ID, err
}

// FolderExists indique si un dossier existe.
func (s *Store) FolderExists(ctx context.Context, id string) bool {
	_, err := s.getFolder(ctx, id)
	return err == nil
}

// ExcludeFromSync empêche la synchronisation de recréer une entrée supprimée
// ou fusionnée par l'utilisateur.
func (s *Store) ExcludeFromSync(ctx context.Context, sourceID, externalID string) error {
	if sourceID == "" || externalID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO sync_exclusions (source_id, external_id, created_at) VALUES (?, ?, ?)`, sourceID, externalID, now())
	return err
}

// SyncExclusions renvoie les identifiants externes exclus d'une source.
func (s *Store) SyncExclusions(ctx context.Context, sourceID string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT external_id FROM sync_exclusions WHERE source_id = ?`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ClearSyncExclusions réautorise toutes les entrées d'une source.
func (s *Store) ClearSyncExclusions(ctx context.Context, sourceID string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sync_exclusions WHERE source_id = ?`, sourceID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// AdoptSync rattache une entrée existante à une source (elle sera mise à jour
// par la synchronisation à la place de l'entrée d'origine).
func (s *Store) AdoptSync(ctx context.Context, entryID, sourceID, externalID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE entries SET source_id = ?, external_id = ? WHERE id = ?`, sourceID, externalID, entryID)
	return err
}
