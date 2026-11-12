package store

import (
	"context"
	"encoding/json"
)

type Workspace struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	EntryIDs []string `json:"entryIds"`
}

func (s *Store) ListWorkspaces(ctx context.Context, user string) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, entry_ids FROM workspaces WHERE user = ? ORDER BY name COLLATE NOCASE", user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Workspace{}
	for rows.Next() {
		var item Workspace
		var ids string
		if err := rows.Scan(&item.ID, &item.Name, &ids); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(ids), &item.EntryIDs); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) SaveWorkspace(ctx context.Context, user string, w Workspace) (Workspace, error) {
	w.Name = clean(w.Name)
	if w.Name == "" || len(w.Name) > 100 || len(w.EntryIDs) == 0 || len(w.EntryIDs) > 50 {
		return w, ErrInvalid{"nom obligatoire et 1 à 50 entrées par espace"}
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, id := range w.EntryIDs {
		if seen[id] {
			continue
		}
		entry, err := s.GetEntry(ctx, id)
		if err != nil {
			return w, ErrInvalid{"une entrée de l’espace est introuvable"}
		}
		if entry.Kind == KindWeb {
			return w, ErrInvalid{"choisissez des sessions SSH, RDP, VNC ou Proxmox"}
		}
		seen[id] = true
		ids = append(ids, id)
	}
	w.EntryIDs = ids
	data, _ := json.Marshal(ids)
	if w.ID == "" {
		w.ID = NewID()
		_, err := s.db.ExecContext(ctx, "INSERT INTO workspaces (id, user, name, entry_ids) VALUES (?, ?, ?, ?)", w.ID, user, w.Name, string(data))
		return w, err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE workspaces SET name = ?, entry_ids = ? WHERE id = ? AND user = ?", w.Name, string(data), w.ID, user)
	if err != nil {
		return w, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return w, ErrNotFound
	}
	return w, nil
}

func (s *Store) DeleteWorkspace(ctx context.Context, user, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM workspaces WHERE id = ? AND user = ?", id, user)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
