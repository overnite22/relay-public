package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Attachment struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) ListAttachments(ctx context.Context, entryID string) ([]Attachment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, size, created_at FROM entry_attachments
		WHERE entry_id = ? ORDER BY created_at DESC, id`, entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Attachment{}
	for rows.Next() {
		var item Attachment
		var createdAt int64
		if err := rows.Scan(&item.ID, &item.Name, &item.Size, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt = ts(createdAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) AddAttachment(ctx context.Context, entryID, name string, data []byte) (Attachment, error) {
	item := Attachment{ID: NewID(), Name: name, Size: int64(len(data)), CreatedAt: ts(now())}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO entry_attachments (id, entry_id, name, size, created_at, data)
		SELECT ?, id, ?, ?, ?, ? FROM entries WHERE id = ?`,
		item.ID, item.Name, item.Size, item.CreatedAt.UnixMilli(), data, entryID)
	if err != nil {
		return Attachment{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return Attachment{}, ErrNotFound
	}
	return item, nil
}

func (s *Store) ReadAttachment(ctx context.Context, entryID, id string) (string, []byte, error) {
	var name string
	var data []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT name, data FROM entry_attachments WHERE entry_id = ? AND id = ?`,
		entryID, id).Scan(&name, &data)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return name, data, err
}

func (s *Store) DeleteAttachment(ctx context.Context, entryID, id string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM entry_attachments WHERE entry_id = ? AND id = ?`, entryID, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMergedEntry moves attachments and removes the old entry in one transaction.
func (s *Store) DeleteMergedEntry(ctx context.Context, oldID, keepID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE entry_attachments SET entry_id = ? WHERE entry_id = ?`, keepID, oldID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE id = ?`, oldID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
