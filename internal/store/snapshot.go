package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

var snapshotTables = []string{
	"credentials", "folders", "sources", "entries", "folder_credentials",
	"entry_attachments", "sync_exclusions", "saved_commands", "command_runs", "ssh_known_hosts", "workspaces", "audit", "device_tokens",
}

type SnapshotCell struct {
	Text   *string `json:"text,omitempty"`
	Number *int64  `json:"number,omitempty"`
	Blob   []byte  `json:"blob,omitempty"`
}
type SnapshotTable struct {
	Name    string           `json:"name"`
	Columns []string         `json:"columns"`
	Rows    [][]SnapshotCell `json:"rows"`
}
type Snapshot struct {
	Version int             `json:"version"`
	Tables  []SnapshotTable `json:"tables"`
}

func quoted(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback()
	snapshot := Snapshot{Version: 1}
	for _, name := range snapshotTables {
		rows, err := tx.QueryContext(ctx, "SELECT * FROM "+quoted(name))
		if err != nil {
			return Snapshot{}, err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return Snapshot{}, err
		}
		table := SnapshotTable{Name: name, Columns: cols, Rows: [][]SnapshotCell{}}
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return Snapshot{}, err
			}
			row := make([]SnapshotCell, len(cols))
			for i, value := range values {
				switch v := value.(type) {
				case nil:
				case string:
					row[i].Text = &v
				case int64:
					row[i].Number = &v
				case []byte:
					row[i].Blob = append([]byte{}, v...)
				default:
					rows.Close()
					return Snapshot{}, fmt.Errorf("type de sauvegarde inattendu dans %s", name)
				}
			}
			table.Rows = append(table.Rows, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Tables = append(snapshot.Tables, table)
	}
	return snapshot, tx.Commit()
}

// RestoreSnapshot replaces all backed-up data in a transaction. prepare installs
// staged recording files just before commit, after validating the foreign keys.
func (s *Store) RestoreSnapshot(ctx context.Context, snapshot Snapshot, prepare func() error) error {
	if snapshot.Version != 1 || len(snapshot.Tables) != len(snapshotTables) {
		return ErrInvalid{"version de sauvegarde incompatible"}
	}
	tables := map[string]SnapshotTable{}
	for _, table := range snapshot.Tables {
		if _, ok := tables[table.Name]; ok {
			return ErrInvalid{"table répétée dans la sauvegarde"}
		}
		tables[table.Name] = table
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "PRAGMA defer_foreign_keys = ON"); err != nil {
		return err
	}
	for _, name := range snapshotTables {
		table, ok := tables[name]
		if !ok {
			return ErrInvalid{"table manquante dans la sauvegarde"}
		}
		rows, err := tx.QueryContext(ctx, "SELECT * FROM "+quoted(name)+" LIMIT 0")
		if err != nil {
			return err
		}
		cols, err := rows.Columns()
		rows.Close()
		if err != nil {
			return err
		}
		a, _ := json.Marshal(cols)
		b, _ := json.Marshal(table.Columns)
		if string(a) != string(b) {
			return ErrInvalid{"colonnes de sauvegarde incompatibles"}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+quoted(name)); err != nil {
			return err
		}
	}
	for _, name := range snapshotTables {
		table := tables[name]
		columns := make([]string, len(table.Columns))
		for i, c := range table.Columns {
			columns[i] = quoted(c)
		}
		query := "INSERT INTO " + quoted(name) + " (" + strings.Join(columns, ",") + ") VALUES (" + strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",") + ")"
		for _, row := range table.Rows {
			if len(row) != len(columns) {
				return ErrInvalid{"ligne de sauvegarde invalide"}
			}
			values := make([]any, len(row))
			for i, cell := range row {
				switch {
				case cell.Text != nil:
					values[i] = *cell.Text
				case cell.Number != nil:
					values[i] = *cell.Number
				case cell.Blob != nil:
					values[i] = cell.Blob
				}
			}
			if _, err := tx.ExecContext(ctx, query, values...); err != nil {
				return ErrInvalid{"contenu de sauvegarde invalide : " + err.Error()}
			}
		}
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	invalid := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if invalid {
		return ErrInvalid{"références de sauvegarde invalides"}
	}
	if prepare != nil {
		if err := prepare(); err != nil {
			return err
		}
	}
	return tx.Commit()
}
