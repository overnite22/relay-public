package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

func (s *Store) folderCredentials(ctx context.Context, id string) (map[string]*string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT kind, credential_id FROM folder_credentials WHERE folder_id = ?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]*string{}
	for rows.Next() {
		var kind string
		var credential sql.NullString
		if err := rows.Scan(&kind, &credential); err != nil {
			return nil, err
		}
		result[kind] = strPtr(credential)
	}
	return result, rows.Err()
}

func (s *Store) SetFolderCredentials(ctx context.Context, id string, values map[string]*string) error {
	if _, err := s.getFolder(ctx, id); err != nil {
		return err
	}
	for kind, credential := range values {
		if kind != KindSSH && kind != KindRDP && kind != KindVNC && kind != KindWeb {
			return ErrInvalid{"type d’identifiant de dossier invalide"}
		}
		if credential != nil {
			if _, err := s.GetCredential(ctx, *credential); err != nil {
				return ErrInvalid{"identifiant du coffre introuvable"}
			}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM folder_credentials WHERE folder_id = ?", id); err != nil {
		return err
	}
	for kind, credential := range values {
		if _, err := tx.ExecContext(ctx, "INSERT INTO folder_credentials (folder_id, kind, credential_id) VALUES (?, ?, ?)", id, kind, nullable(credential)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// EffectiveCredentialID resolves the closest folder default unless explicitly disabled.
func (s *Store) EffectiveCredentialID(ctx context.Context, entry Entry) (*string, error) {
	if entry.CredentialID != nil {
		return entry.CredentialID, nil
	}
	var settings struct {
		CredentialMode string `json:"credentialMode"`
	}
	_ = json.Unmarshal(entry.Settings, &settings)
	if settings.CredentialMode == "none" || entry.FolderID == nil || entry.Kind == KindPVE {
		return nil, nil
	}
	seen := map[string]bool{}
	id := *entry.FolderID
	for !seen[id] {
		seen[id] = true
		values, err := s.folderCredentials(ctx, id)
		if err != nil {
			return nil, err
		}
		if credential, ok := values[entry.Kind]; ok {
			return credential, nil
		}
		folder, err := s.getFolder(ctx, id)
		if err != nil {
			return nil, err
		}
		if folder.ParentID == nil {
			return nil, nil
		}
		id = *folder.ParentID
	}
	return nil, ErrInvalid{"cycle dans l’arborescence"}
}
