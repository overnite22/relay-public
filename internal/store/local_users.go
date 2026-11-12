package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const localUsersSchema = `
CREATE TABLE IF NOT EXISTS local_users (
 email TEXT PRIMARY KEY, password_hash TEXT NOT NULL,
 admin INTEGER NOT NULL DEFAULT 0, disabled INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS local_sessions (
 token_hash BLOB PRIMARY KEY, email TEXT NOT NULL REFERENCES local_users(email) ON DELETE CASCADE,
 expires_at INTEGER NOT NULL
);`

// Local users/sessions are instance access policy. Content restore preserves
// them, so an imported archive cannot replace the installation's login policy.
type LocalUser struct {
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	Admin        bool   `json:"admin"`
	Disabled     bool   `json:"disabled"`
}

func (s *Store) LocalUserCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM local_users").Scan(&n)
	return n, err
}
func (s *Store) LocalUser(ctx context.Context, email string) (LocalUser, error) {
	var u LocalUser
	err := s.db.QueryRowContext(ctx, "SELECT email,password_hash,admin,disabled FROM local_users WHERE email=?", email).Scan(&u.Email, &u.PasswordHash, &u.Admin, &u.Disabled)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return u, err
}
func (s *Store) LocalUsers(ctx context.Context) ([]LocalUser, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT email,admin,disabled FROM local_users ORDER BY email")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LocalUser{}
	for rows.Next() {
		var u LocalUser
		if err = rows.Scan(&u.Email, &u.Admin, &u.Disabled); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Store) CreateLocalUser(ctx context.Context, u LocalUser, first bool, actorHash []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !first {
		if _, err = authorizeLocalTx(ctx, tx, actorHash, true); err != nil {
			return err
		}
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM local_users").Scan(&count); err != nil {
		return err
	}
	if count >= 1000 {
		return ErrInvalid{"limite de 1000 comptes atteinte"}
	}
	query := "INSERT INTO local_users(email,password_hash,admin,disabled) VALUES(?,?,?,?)"
	if first {
		query = "INSERT INTO local_users(email,password_hash,admin,disabled) SELECT ?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM local_users)"
	}
	res, err := tx.ExecContext(ctx, query, u.Email, u.PasswordHash, u.Admin, u.Disabled)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrInvalid{"ce compte existe déjà"}
		}
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrInvalid{"instance déjà initialisée"}
	}
	return tx.Commit()
}

// UpdateLocalUser serializes last-admin protection and credential revocation.
func (s *Store) UpdateLocalUser(ctx context.Context, email, hash string, admin, disabled *bool, expected string, actorHash []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	actor, err := authorizeLocalTx(ctx, tx, actorHash, expected == "")
	if err != nil {
		return err
	}
	if expected != "" && actor != email {
		return ErrInvalid{"session invalide"}
	}
	var old LocalUser
	err = tx.QueryRowContext(ctx, "SELECT password_hash,admin,disabled FROM local_users WHERE email=?", email).Scan(&old.PasswordHash, &old.Admin, &old.Disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if expected != "" && old.PasswordHash != expected {
		return ErrInvalid{"le mot de passe a changé, reconnectez-vous"}
	}
	nextAdmin, nextDisabled := old.Admin, old.Disabled
	if admin != nil {
		nextAdmin = *admin
	}
	if disabled != nil {
		nextDisabled = *disabled
	}
	if old.Admin && !old.Disabled && (!nextAdmin || nextDisabled) {
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM local_users WHERE admin=1 AND disabled=0").Scan(&n); err != nil {
			return err
		}
		if n <= 1 {
			return ErrInvalid{"le dernier administrateur actif doit être conservé"}
		}
	}
	if hash == "" {
		hash = old.PasswordHash
	}
	if _, err = tx.ExecContext(ctx, "UPDATE local_users SET password_hash=?,admin=?,disabled=? WHERE email=?", hash, nextAdmin, nextDisabled, email); err != nil {
		return err
	}
	if hash != old.PasswordHash || nextDisabled || nextAdmin != old.Admin {
		if _, err = tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE email=?", email); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM device_tokens WHERE user=?", email); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) CreateLocalSession(ctx context.Context, email string, hash []byte, expires int64, passwordHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE expires_at<=?", now()); err != nil {
		return err
	}
	// Bound persistent sessions for an account.
	if _, err = tx.ExecContext(ctx, "DELETE FROM local_sessions WHERE token_hash IN (SELECT token_hash FROM local_sessions WHERE email=? ORDER BY expires_at DESC LIMIT -1 OFFSET 19)", email); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, "INSERT INTO local_sessions(token_hash,email,expires_at) SELECT ?,email,? FROM local_users WHERE email=? AND disabled=0 AND password_hash=?", hash, expires, email, passwordHash)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}
func (s *Store) ResolveLocalSession(ctx context.Context, hash []byte) (string, error) {
	var email string
	err := s.db.QueryRowContext(ctx, `SELECT u.email FROM local_sessions s JOIN local_users u ON u.email=s.email WHERE s.token_hash=? AND s.expires_at>? AND u.disabled=0`, hash, now()).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return email, err
}
func (s *Store) RevokeLocalSession(ctx context.Context, hash []byte) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM local_sessions WHERE token_hash=?", hash)
	return err
}

// Authentication policy mutations validate the current session inside their
// transaction, after network reads and expensive password verification.
func authorizeLocalTx(ctx context.Context, tx *sql.Tx, hash []byte, admin bool) (string, error) {
	var email string
	query := `SELECT u.email FROM local_sessions s JOIN local_users u ON u.email=s.email WHERE s.token_hash=? AND s.expires_at>? AND u.disabled=0`
	if admin {
		query += " AND u.admin=1"
	}
	err := tx.QueryRowContext(ctx, query, hash, now()).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvalid{"session expirée ou accès révoqué"}
	}
	return email, err
}
