package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const devicesSchema = `
CREATE TABLE IF NOT EXISTS device_tokens (
	id TEXT PRIMARY KEY,
	user TEXT NOT NULL,
	name TEXT NOT NULL,
	token_hash BLOB NOT NULL UNIQUE,
	created_at INTEGER NOT NULL,
	last_used_at INTEGER,
	last_ip TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS device_tokens_user ON device_tokens(user, created_at);
`

// Device est un appareil appairé (application mobile) : un jeton d'accès
// porteur, stocké haché, lié à un utilisateur.
type Device struct {
	ID         string     `json:"id"`
	User       string     `json:"user"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	LastIP     string     `json:"lastIp"`
}

// ListDevices renvoie les appareils d'un utilisateur, du plus récent au plus ancien.
func (s *Store) ListDevices(ctx context.Context, user string) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user, name, created_at, last_used_at, last_ip FROM device_tokens WHERE user = ? ORDER BY created_at DESC`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanDevice(r rowScanner) (Device, error) {
	var d Device
	var created int64
	var used sql.NullInt64
	if err := r.Scan(&d.ID, &d.User, &d.Name, &created, &used, &d.LastIP); err != nil {
		return Device{}, err
	}
	d.CreatedAt = ts(created)
	if used.Valid {
		t := ts(used.Int64)
		d.LastUsedAt = &t
	}
	return d, nil
}

// CreateDevice enregistre un appareil ; hash est le SHA-256 du jeton.
func (s *Store) CreateDevice(ctx context.Context, user, name string, hash []byte) (Device, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Device{}, ErrInvalid{Msg: "nom d'appareil requis"}
	}
	if len(name) > 80 {
		return Device{}, ErrInvalid{Msg: "nom d'appareil trop long"}
	}
	if len(hash) != 32 {
		return Device{}, errors.New("empreinte de jeton invalide")
	}
	d := Device{ID: NewID(), User: user, Name: name, CreatedAt: ts(now())}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO device_tokens (id, user, name, token_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		d.ID, d.User, d.Name, hash, d.CreatedAt.UnixMilli())
	return d, err
}

// DeleteDevice révoque un appareil de l'utilisateur.
func (s *Store) DeleteDevice(ctx context.Context, user, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM device_tokens WHERE id = ? AND user = ?`, id, user)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeviceByTokenHash retrouve l'appareil d'un jeton haché.
func (s *Store) DeviceByTokenHash(ctx context.Context, hash []byte) (Device, error) {
	d, err := scanDevice(s.db.QueryRowContext(ctx,
		`SELECT id, user, name, created_at, last_used_at, last_ip FROM device_tokens WHERE token_hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	return d, err
}

// TouchDevice note la dernière utilisation (au plus une fois par minute).
func (s *Store) TouchDevice(ctx context.Context, id, ip string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE device_tokens SET last_used_at = ?, last_ip = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`,
		now(), ip, id, now()-60_000)
	return err
}
