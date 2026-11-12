// Package store gère la base SQLite de Relay : dossiers, entrées,
// identifiants (secrets chiffrés par le vault) et journal d'activité.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound est renvoyée quand l'objet demandé n'existe pas.
var ErrNotFound = errors.New("introuvable")

// ErrInvalid est renvoyée pour une donnée refusée (validation).
type ErrInvalid struct{ Msg string }

func (e ErrInvalid) Error() string { return e.Msg }

// Kinds d'entrées gérés.
const (
	KindRDP = "rdp"
	KindSSH = "ssh"
	KindVNC = "vnc"
	KindWeb = "web"
	KindPVE = "pve" // console Proxmox (créée par la synchronisation)
)

var defaultPorts = map[string]int{KindRDP: 3389, KindSSH: 22, KindVNC: 5900}

// DefaultPort renvoie le port standard d'un type d'entrée (0 si sans objet).
func DefaultPort(kind string) int { return defaultPorts[kind] }

// Store encapsule la connexion SQLite.
type Store struct {
	db *sql.DB
}

// Folder est un dossier de l'arborescence.
type Folder struct {
	Credentials map[string]*string `json:"credentials"`
	ID          string             `json:"id"`
	ParentID    *string            `json:"parentId"`
	Name        string             `json:"name"`
	CreatedAt   time.Time          `json:"createdAt"`
	UpdatedAt   time.Time          `json:"updatedAt"`
}

// Entry est une connexion (RDP, SSH, VNC) ou un lien web.
type Entry struct {
	ID           string          `json:"id"`
	FolderID     *string         `json:"folderId"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	Host         string          `json:"host"`
	Port         int             `json:"port"`
	URL          string          `json:"url"`
	Username     string          `json:"username"`
	CredentialID *string         `json:"credentialId"`
	Settings     json.RawMessage `json:"settings"`
	Notes        string          `json:"notes"`
	Favorite     bool            `json:"favorite"`
	SourceID     *string         `json:"sourceId"`
	ExternalID   string          `json:"externalId"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

// Credential est un identifiant du coffre. Les champs *Enc ne sortent jamais
// de l'API : seuls les indicateurs Has* sont exposés.
type Credential struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Username      string    `json:"username"`
	Domain        string    `json:"domain"`
	Notes         string    `json:"notes"`
	HasPassword   bool      `json:"hasPassword"`
	HasPrivateKey bool      `json:"hasPrivateKey"`
	HasPassphrase bool      `json:"hasPassphrase"`
	HasTOTP       bool      `json:"hasTotp"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`

	PasswordEnc   string `json:"-"`
	PrivateKeyEnc string `json:"-"`
	PassphraseEnc string `json:"-"`
	TOTPEnc       string `json:"-"`
}

// AuditEvent est une ligne du journal d'activité.
type AuditEvent struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	User   string    `json:"user"`
	Action string    `json:"action"`
	Target string    `json:"target"`
	Detail string    `json:"detail"`
}

const schema = `
CREATE TABLE IF NOT EXISTS folders (
	id TEXT PRIMARY KEY,
	parent_id TEXT REFERENCES folders(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS credentials (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	username TEXT NOT NULL DEFAULT '',
	domain TEXT NOT NULL DEFAULT '',
	password_enc TEXT NOT NULL DEFAULT '',
	private_key_enc TEXT NOT NULL DEFAULT '',
	passphrase_enc TEXT NOT NULL DEFAULT '',
	totp_enc TEXT NOT NULL DEFAULT '',
	notes TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS folder_credentials (
 folder_id TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
 kind TEXT NOT NULL,
 credential_id TEXT REFERENCES credentials(id) ON DELETE SET NULL,
 PRIMARY KEY(folder_id,kind)
);
CREATE TABLE IF NOT EXISTS workspaces (
 id TEXT PRIMARY KEY, user TEXT NOT NULL, name TEXT NOT NULL, entry_ids TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS entries (
	id TEXT PRIMARY KEY,
	folder_id TEXT REFERENCES folders(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	kind TEXT NOT NULL,
	host TEXT NOT NULL DEFAULT '',
	port INTEGER NOT NULL DEFAULT 0,
	url TEXT NOT NULL DEFAULT '',
	username TEXT NOT NULL DEFAULT '',
	credential_id TEXT REFERENCES credentials(id) ON DELETE SET NULL,
	settings TEXT NOT NULL DEFAULT '{}',
	notes TEXT NOT NULL DEFAULT '',
	favorite INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS entries_folder ON entries(folder_id);
CREATE TABLE IF NOT EXISTS entry_attachments (
	id TEXT PRIMARY KEY,
	entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	size INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	data BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS entry_attachments_entry ON entry_attachments(entry_id, created_at);
CREATE TABLE IF NOT EXISTS audit (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	at INTEGER NOT NULL,
	user TEXT NOT NULL,
	action TEXT NOT NULL,
	target TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS audit_at ON audit(at);
`

// Open ouvre (ou crée) la base SQLite à path.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schéma : %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration : %w", err)
	}
	if _, err := db.Exec(commandsSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schéma des commandes : %w", err)
	}
	if _, err := db.Exec(devicesSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schéma des appareils : %w", err)
	}
	return &Store{db: db}, nil
}

// Close ferme la base.
func (s *Store) Close() error { return s.db.Close() }

// NewID génère un identifiant aléatoire de 128 bits.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func now() int64 { return time.Now().UnixMilli() }

func ts(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func nullable(p *string) any {
	if p == nil || *p == "" {
		return nil
	}
	return *p
}

func strPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

func clean(s string) string { return strings.TrimSpace(s) }

// ---------- Dossiers ----------

// ListFolders renvoie tous les dossiers triés par nom.
func (s *Store) ListFolders(ctx context.Context) ([]Folder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, parent_id, name, created_at, updated_at FROM folders ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Folder{}
	for rows.Next() {
		var f Folder
		var parent sql.NullString
		var c, u int64
		if err := rows.Scan(&f.ID, &parent, &f.Name, &c, &u); err != nil {
			return nil, err
		}
		f.ParentID, f.CreatedAt, f.UpdatedAt = strPtr(parent), ts(c), ts(u)
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		out[i].Credentials, err = s.folderCredentials(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) getFolder(ctx context.Context, id string) (Folder, error) {
	var f Folder
	var parent sql.NullString
	var c, u int64
	err := s.db.QueryRowContext(ctx, `SELECT id, parent_id, name, created_at, updated_at FROM folders WHERE id = ?`, id).
		Scan(&f.ID, &parent, &f.Name, &c, &u)
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	f.ParentID, f.CreatedAt, f.UpdatedAt = strPtr(parent), ts(c), ts(u)
	return f, err
}

// checkParent vérifie que parentID existe et n'est pas id ni l'un de ses descendants.
func (s *Store) checkParent(ctx context.Context, id string, parentID *string) error {
	if parentID == nil || *parentID == "" {
		return nil
	}
	cur := *parentID
	for depth := 0; depth < 1000; depth++ {
		if cur == id {
			return ErrInvalid{"un dossier ne peut pas être déplacé dans lui-même ou dans un de ses sous-dossiers"}
		}
		f, err := s.getFolder(ctx, cur)
		if errors.Is(err, ErrNotFound) {
			return ErrInvalid{"dossier parent introuvable"}
		}
		if err != nil {
			return err
		}
		if f.ParentID == nil {
			return nil
		}
		cur = *f.ParentID
	}
	return ErrInvalid{"arborescence trop profonde"}
}

// SaveFolder crée (ID vide) ou met à jour un dossier.
func (s *Store) SaveFolder(ctx context.Context, f Folder) (Folder, error) {
	f.Name = clean(f.Name)
	if f.Name == "" {
		return f, ErrInvalid{"le nom du dossier est obligatoire"}
	}
	if f.ParentID != nil && *f.ParentID == "" {
		f.ParentID = nil
	}
	t := now()
	if f.ID == "" {
		f.ID = NewID()
		if err := s.checkParent(ctx, f.ID, f.ParentID); err != nil {
			return f, err
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO folders (id, parent_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			f.ID, nullable(f.ParentID), f.Name, t, t)
		if err != nil {
			return f, err
		}
		return s.getFolder(ctx, f.ID)
	}
	if err := s.checkParent(ctx, f.ID, f.ParentID); err != nil {
		return f, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE folders SET parent_id = ?, name = ?, updated_at = ? WHERE id = ?`,
		nullable(f.ParentID), f.Name, t, f.ID)
	if err != nil {
		return f, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return f, ErrNotFound
	}
	return s.getFolder(ctx, f.ID)
}

// DeleteFolder supprime un dossier, ses sous-dossiers et leurs entrées.
func (s *Store) DeleteFolder(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM folders WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- Entrées ----------

const entryCols = `id, folder_id, name, kind, host, port, url, username, credential_id, settings, notes, favorite, source_id, external_id, created_at, updated_at`

func scanEntry(sc interface{ Scan(...any) error }) (Entry, error) {
	var e Entry
	var folder, cred, source sql.NullString
	var settings string
	var fav int
	var c, u int64
	err := sc.Scan(&e.ID, &folder, &e.Name, &e.Kind, &e.Host, &e.Port, &e.URL, &e.Username, &cred, &settings, &e.Notes, &fav, &source, &e.ExternalID, &c, &u)
	if err != nil {
		return e, err
	}
	e.FolderID, e.CredentialID, e.SourceID = strPtr(folder), strPtr(cred), strPtr(source)
	e.Settings = json.RawMessage(settings)
	e.Favorite = fav != 0
	e.CreatedAt, e.UpdatedAt = ts(c), ts(u)
	return e, nil
}

// ListEntries renvoie toutes les entrées triées par nom.
func (s *Store) ListEntries(ctx context.Context) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryCols+` FROM entries ORDER BY name COLLATE NOCASE`)
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

// GetEntry renvoie une entrée.
func (s *Store) GetEntry(ctx context.Context, id string) (Entry, error) {
	e, err := scanEntry(s.db.QueryRowContext(ctx, `SELECT `+entryCols+` FROM entries WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

func (s *Store) validateEntry(ctx context.Context, e *Entry) error {
	e.Name, e.Host, e.URL, e.Username = clean(e.Name), clean(e.Host), clean(e.URL), clean(e.Username)
	if e.Name == "" {
		return ErrInvalid{"le nom de l'entrée est obligatoire"}
	}
	switch e.Kind {
	case KindRDP, KindSSH, KindVNC:
		if e.Host == "" {
			return ErrInvalid{"l'hôte est obligatoire"}
		}
		if e.Port == 0 {
			e.Port = DefaultPort(e.Kind)
		}
		if e.Port < 1 || e.Port > 65535 {
			return ErrInvalid{"le port doit être compris entre 1 et 65535"}
		}
	case KindWeb:
		if !strings.HasPrefix(e.URL, "http://") && !strings.HasPrefix(e.URL, "https://") {
			return ErrInvalid{"l'URL doit commencer par http:// ou https://"}
		}
		e.Port = 0
	case KindPVE:
		var pve struct {
			SourceID string `json:"sourceId"`
			Node     string `json:"node"`
			VMID     int    `json:"vmid"`
			Type     string `json:"type"`
		}
		if err := json.Unmarshal(e.Settings, &pve); err != nil || pve.SourceID == "" || pve.Node == "" || pve.VMID <= 0 || (pve.Type != "qemu" && pve.Type != "lxc") {
			return ErrInvalid{"console Proxmox : source, nœud, VMID et type (qemu/lxc) obligatoires"}
		}
		e.Port = 0
	default:
		return ErrInvalid{"type d'entrée inconnu : " + e.Kind}
	}
	if len(e.Settings) == 0 || string(e.Settings) == "null" {
		e.Settings = json.RawMessage(`{}`)
	}
	var obj map[string]any
	if err := json.Unmarshal(e.Settings, &obj); err != nil {
		return ErrInvalid{"paramètres avancés invalides (objet JSON attendu)"}
	}
	if e.FolderID != nil && *e.FolderID == "" {
		e.FolderID = nil
	}
	if e.FolderID != nil {
		if _, err := s.getFolder(ctx, *e.FolderID); errors.Is(err, ErrNotFound) {
			return ErrInvalid{"dossier introuvable"}
		} else if err != nil {
			return err
		}
	}
	if e.CredentialID != nil && *e.CredentialID == "" {
		e.CredentialID = nil
	}
	if e.CredentialID != nil {
		if _, err := s.GetCredential(ctx, *e.CredentialID); errors.Is(err, ErrNotFound) {
			return ErrInvalid{"identifiant introuvable"}
		} else if err != nil {
			return err
		}
	}
	return nil
}

// SaveEntry crée (ID vide) ou met à jour une entrée.
func (s *Store) SaveEntry(ctx context.Context, e Entry) (Entry, error) {
	if err := s.validateEntry(ctx, &e); err != nil {
		return e, err
	}
	t := now()
	fav := 0
	if e.Favorite {
		fav = 1
	}
	if e.ID == "" {
		e.ID = NewID()
		_, err := s.db.ExecContext(ctx, `INSERT INTO entries (`+entryCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ID, nullable(e.FolderID), e.Name, e.Kind, e.Host, e.Port, e.URL, e.Username, nullable(e.CredentialID), string(e.Settings), e.Notes, fav, nullable(e.SourceID), e.ExternalID, t, t)
		if err != nil {
			return e, err
		}
		return s.GetEntry(ctx, e.ID)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE entries SET folder_id = ?, name = ?, kind = ?, host = ?, port = ?, url = ?, username = ?, credential_id = ?, settings = ?, notes = ?, favorite = ?, updated_at = ? WHERE id = ?`,
		nullable(e.FolderID), e.Name, e.Kind, e.Host, e.Port, e.URL, e.Username, nullable(e.CredentialID), string(e.Settings), e.Notes, fav, t, e.ID)
	if err != nil {
		return e, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return e, ErrNotFound
	}
	return s.GetEntry(ctx, e.ID)
}

// DeleteEntry supprime une entrée.
func (s *Store) DeleteEntry(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM entries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- Identifiants ----------

const credCols = `id, name, username, domain, password_enc, private_key_enc, passphrase_enc, totp_enc, notes, created_at, updated_at`

func scanCred(sc interface{ Scan(...any) error }) (Credential, error) {
	var c Credential
	var cr, up int64
	err := sc.Scan(&c.ID, &c.Name, &c.Username, &c.Domain, &c.PasswordEnc, &c.PrivateKeyEnc, &c.PassphraseEnc, &c.TOTPEnc, &c.Notes, &cr, &up)
	if err != nil {
		return c, err
	}
	c.HasPassword, c.HasPrivateKey = c.PasswordEnc != "", c.PrivateKeyEnc != ""
	c.HasPassphrase, c.HasTOTP = c.PassphraseEnc != "", c.TOTPEnc != ""
	c.CreatedAt, c.UpdatedAt = ts(cr), ts(up)
	return c, nil
}

// ListCredentials renvoie les identifiants (secrets chiffrés inclus, non sérialisés).
func (s *Store) ListCredentials(ctx context.Context) ([]Credential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+credCols+` FROM credentials ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Credential{}
	for rows.Next() {
		c, err := scanCred(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCredential renvoie un identifiant.
func (s *Store) GetCredential(ctx context.Context, id string) (Credential, error) {
	c, err := scanCred(s.db.QueryRowContext(ctx, `SELECT `+credCols+` FROM credentials WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// SaveCredential crée (ID vide) ou met à jour un identifiant. Les champs *Enc
// doivent déjà être chiffrés par l'appelant (voir la couche API).
func (s *Store) SaveCredential(ctx context.Context, c Credential) (Credential, error) {
	c.Name, c.Username, c.Domain = clean(c.Name), clean(c.Username), clean(c.Domain)
	if c.Name == "" {
		return c, ErrInvalid{"le nom de l'identifiant est obligatoire"}
	}
	t := now()
	if c.ID == "" {
		return c, errors.New("store: l'ID de l'identifiant doit être fixé par l'appelant (lié au chiffrement)")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE credentials SET name = ?, username = ?, domain = ?, password_enc = ?, private_key_enc = ?, passphrase_enc = ?, totp_enc = ?, notes = ?, updated_at = ? WHERE id = ?`,
		c.Name, c.Username, c.Domain, c.PasswordEnc, c.PrivateKeyEnc, c.PassphraseEnc, c.TOTPEnc, c.Notes, t, c.ID)
	if err != nil {
		return c, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_, err = s.db.ExecContext(ctx, `INSERT INTO credentials (`+credCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.ID, c.Name, c.Username, c.Domain, c.PasswordEnc, c.PrivateKeyEnc, c.PassphraseEnc, c.TOTPEnc, c.Notes, t, t)
		if err != nil {
			return c, err
		}
	}
	return s.GetCredential(ctx, c.ID)
}

// DeleteCredential supprime un identifiant (les entrées qui l'utilisent sont détachées).
func (s *Store) DeleteCredential(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM credentials WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountEntriesUsingCredential compte les entrées liées à un identifiant.
func (s *Store) CountEntriesUsingCredential(ctx context.Context, id string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE credential_id = ?`, id).Scan(&n)
	return n, err
}

// ---------- Journal ----------

// Audit ajoute un événement au journal.
func (s *Store) Audit(ctx context.Context, user, action, target, detail string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit (at, user, action, target, detail) VALUES (?, ?, ?, ?, ?)`,
		now(), user, action, target, detail)
	return err
}

// ListAudit renvoie les derniers événements, du plus récent au plus ancien.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, user, action, target, detail FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var a AuditEvent
		var at int64
		if err := rows.Scan(&a.ID, &at, &a.User, &a.Action, &a.Target, &a.Detail); err != nil {
			return nil, err
		}
		a.At = ts(at)
		out = append(out, a)
	}
	return out, rows.Err()
}
