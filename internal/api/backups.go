package api

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/backup"
	"github.com/overnite22/relay-public/internal/store"
	"github.com/overnite22/relay-public/internal/vault"
)

type cappedBackupWriter struct {
	w         io.Writer
	remaining *int64
}

func (w cappedBackupWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > *w.remaining {
		return 0, errors.New("sauvegarde trop volumineuse (512 Mio maximum)")
	}
	n, err := w.w.Write(p)
	*w.remaining -= int64(n)
	return n, err
}

type backupDatabase struct {
	CreatedAt time.Time      `json:"createdAt"`
	Key       []byte         `json:"key"`
	Database  store.Snapshot `json:"database"`
}

// dataAccess prevents restoring while a session, command or sync is in progress.
func (s *Server) dataAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/backups/restore" {
			next.ServeHTTP(w, r)
			return
		}
		s.dataGuard.RLock()
		defer s.dataGuard.RUnlock()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) backupTemp() (string, error) {
	if s.opt.DataDir == "" {
		return "", store.ErrInvalid{Msg: "sauvegardes indisponibles"}
	}
	return os.MkdirTemp(s.opt.DataDir, ".backup-")
}

func (s *Server) exportBackup(w http.ResponseWriter, r *http.Request) {
	if !s.backupGuard(w, r) {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Password == "" || len(in.Password) > 1024 {
		s.fail(w, r, store.ErrInvalid{Msg: "mot de passe de sauvegarde obligatoire"})
		return
	}
	dir, err := s.backupTemp()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() {
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
	}()
	err = s.createBackup(r.Context(), dir, in.Password, s.backupLocalProof(r)...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "backup.export", "Relay", "")
	f, err := os.Open(filepath.Join(dir, "archive.relay"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="relay-`+time.Now().UTC().Format("20060102-150405")+`.relay"`)
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "relay.relay", info.ModTime(), f)
}

func (s *Server) createBackup(ctx context.Context, dir, password string, localAdminProof ...[]byte) error {
	snapshot, err := s.opt.Store.Snapshot(ctx, localAdminProof...)
	if err != nil {
		return err
	}
	raw, err := os.OpenFile(filepath.Join(dir, "payload.zip"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer raw.Close()
	archive := zip.NewWriter(raw)
	budget := int64(backup.MaxSize)
	entry, err := archive.Create("database.json")
	if err != nil {
		return err
	}
	bundle := backupDatabase{CreatedAt: time.Now().UTC(), Key: s.opt.Vault.BackupKey(), Database: snapshot}
	if err := json.NewEncoder(cappedBackupWriter{entry, &budget}).Encode(bundle); err != nil {
		return err
	}
	if s.opt.RecordingsDir != "" {
		err = filepath.WalkDir(s.opt.RecordingsDir, func(path string, d fs.DirEntry, walkErr error) error {
			if errors.Is(walkErr, os.ErrNotExist) && path == s.opt.RecordingsDir {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if strings.HasPrefix(d.Name(), ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(s.opt.RecordingsDir, path)
			if err != nil {
				return err
			}
			name := "recordings/" + filepath.ToSlash(rel)
			if !validRecordingPath(name) {
				return errors.New("chemin d’enregistrement inattendu")
			}
			if !d.Type().IsRegular() {
				return errors.New("fichier d’enregistrement non régulier")
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			dst, err := archive.Create(name)
			if err != nil {
				return err
			}
			_, err = io.Copy(cappedBackupWriter{dst, &budget}, file)
			return err
		})
		if err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if _, err := raw.Seek(0, io.SeekStart); err != nil {
		return err
	}
	dst, err := os.OpenFile(filepath.Join(dir, "archive.relay"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer dst.Close()
	return backup.Encrypt(dst, raw, password)
}

func validRecordingPath(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "recordings" || len(parts[1]) != 64 || !validRecordingID(parts[2]) {
		return false
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return false
	}
	return parts[3] == "session.guac" || parts[3] == "metadata.json"
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	if !s.backupGuard(w, r) {
		return
	}
	password, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Backup-Password"))
	if err != nil || len(password) == 0 || len(password) > 1024 {
		s.fail(w, r, store.ErrInvalid{Msg: "mot de passe de sauvegarde obligatoire"})
		return
	}
	defer clear(password)
	dir, err := s.backupTemp()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() {
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
	}()
	raw, err := os.OpenFile(filepath.Join(dir, "restore.zip"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer raw.Close()
	err = backup.Decrypt(raw, http.MaxBytesReader(w, r.Body, backup.MaxSize+(1<<20)), string(password))
	if err != nil {
		s.fail(w, r, store.ErrInvalid{Msg: err.Error()})
		return
	}
	info, err := raw.Stat()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	zr, err := zip.NewReader(raw, info.Size())
	if err != nil {
		s.fail(w, r, store.ErrInvalid{Msg: "archive invalide"})
		return
	}
	bundle, err := unpackBackup(zr, dir)
	if err != nil {
		s.fail(w, r, store.ErrInvalid{Msg: err.Error()})
		return
	}
	oldVault, err := vault.New(bundle.Key)
	clear(bundle.Key)
	if err == nil {
		err = rekeySnapshot(&bundle.Database, oldVault, s.opt.Vault)
	}
	if err == nil {
		err = finalizeRestoredRuns(&bundle.Database)
	}
	if err != nil {
		s.fail(w, r, store.ErrInvalid{Msg: "coffre de sauvegarde invalide"})
		return
	}

	if !s.dataGuard.TryLock() {
		writeErr(w, http.StatusConflict, "fermez les sessions et attendez la fin des commandes ou synchronisations, puis réessayez")
		return
	}
	defer s.dataGuard.Unlock()
	staged := filepath.Join(dir, "recordings")
	previous := filepath.Join(dir, "previous-recordings")
	hadPrevious := false
	installed := false
	prepare := func() error {
		if _, err := os.Stat(s.opt.RecordingsDir); err == nil {
			if err := os.Rename(s.opt.RecordingsDir, previous); err != nil {
				return err
			}
			hadPrevious = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(staged, s.opt.RecordingsDir); err != nil {
			return err
		}
		installed = true
		return nil
	}
	err = s.opt.Store.RestoreSnapshot(r.Context(), bundle.Database, prepare, s.backupLocalProof(r)...)
	if err != nil {
		if installed {
			_ = os.RemoveAll(s.opt.RecordingsDir)
		}
		if hadPrevious {
			if rollbackErr := os.Rename(previous, s.opt.RecordingsDir); rollbackErr != nil {
				s.log.Error("récupération des enregistrements", "path", previous, "err", rollbackErr)
				// Preserve the recovery directory if the filesystem itself failed.
				dir = ""
			}
		}
		s.fail(w, r, err)
		return
	}
	s.runs.mu.Lock()
	clear(s.runs.runs)
	s.runs.mu.Unlock()
	s.tokens.mu.Lock()
	clear(s.tokens.m)
	s.tokens.mu.Unlock()
	s.diagnostics.mu.Lock()
	clear(s.diagnostics.byAddress)
	s.diagnostics.mu.Unlock()
	s.audit(r, "backup.restore", "Relay", bundle.CreatedAt.Format(time.RFC3339))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func unpackBackup(zr *zip.Reader, dir string) (backupDatabase, error) {
	var bundle backupDatabase
	seen := map[string]bool{}
	var total uint64
	for _, file := range zr.File {
		if seen[file.Name] || (file.Name != "database.json" && !validRecordingPath(file.Name)) {
			return bundle, errors.New("fichier inattendu dans la sauvegarde")
		}
		seen[file.Name] = true
		if file.UncompressedSize64 > backup.MaxSize-total {
			return bundle, errors.New("sauvegarde décompressée trop volumineuse (512 Mio maximum)")
		}
		total += file.UncompressedSize64
		reader, err := file.Open()
		if err != nil {
			return bundle, err
		}
		if file.Name == "database.json" {
			err = json.NewDecoder(io.LimitReader(reader, backup.MaxSize+1)).Decode(&bundle)
		} else {
			path := filepath.Join(dir, filepath.FromSlash(file.Name))
			err = os.MkdirAll(filepath.Dir(path), 0o700)
			if err == nil {
				var dst *os.File
				dst, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
				if err == nil {
					_, err = io.Copy(dst, io.LimitReader(reader, int64(file.UncompressedSize64)+1))
					closeErr := dst.Close()
					if err == nil {
						err = closeErr
					}
				}
			}
		}
		closeErr := reader.Close()
		if err != nil {
			return bundle, err
		}
		if closeErr != nil {
			return bundle, closeErr
		}
	}
	if !seen["database.json"] {
		return bundle, errors.New("base absente de la sauvegarde")
	}
	return bundle, os.MkdirAll(filepath.Join(dir, "recordings"), 0o700)
}

func rekeySnapshot(snapshot *store.Snapshot, old, current *vault.Vault) error {
	for ti := range snapshot.Tables {
		table := &snapshot.Tables[ti]
		fields := map[string]string{}
		switch table.Name {
		case "credentials":
			fields = map[string]string{"password_enc": "password", "private_key_enc": "privateKey", "passphrase_enc": "passphrase", "totp_enc": "totp"}
		case "sources":
			fields = map[string]string{"secret_enc": "secret"}
		default:
			continue
		}
		index := map[string]int{}
		for i, name := range table.Columns {
			index[name] = i
		}
		idIndex, ok := index["id"]
		if !ok {
			return errors.New("identifiant absent")
		}
		for ri := range table.Rows {
			row := table.Rows[ri]
			if len(row) != len(table.Columns) || row[idIndex].Text == nil {
				return errors.New("ligne invalide")
			}
			id := *row[idIndex].Text
			for column, field := range fields {
				i, ok := index[column]
				if !ok || row[i].Text == nil {
					return errors.New("champ du coffre absent")
				}
				context := aad(id, field)
				if table.Name == "sources" {
					context = sourceAAD(id)
				}
				plain, err := old.Open(*row[i].Text, context)
				if err != nil {
					return err
				}
				sealed, err := current.Seal(plain, context)
				if err != nil {
					return err
				}
				row[i].Text = &sealed
			}
		}
	}
	return nil
}

func finalizeRestoredRuns(snapshot *store.Snapshot) error {
	for ti := range snapshot.Tables {
		table := &snapshot.Tables[ti]
		if table.Name != "command_runs" {
			continue
		}
		columns := map[string]int{}
		for i, name := range table.Columns {
			columns[name] = i
		}
		required := []string{"finished_at", "results", "ok", "failed"}
		for _, name := range required {
			if _, ok := columns[name]; !ok {
				return errors.New("historique de commandes invalide")
			}
		}
		for _, row := range table.Rows {
			if len(row) != len(table.Columns) {
				return errors.New("ligne de commande invalide")
			}
			if row[columns["finished_at"]].Number != nil {
				continue
			}
			cell := row[columns["results"]]
			if cell.Text == nil {
				return errors.New("résultats de commande invalides")
			}
			var results []map[string]any
			if err := json.Unmarshal([]byte(*cell.Text), &results); err != nil {
				return err
			}
			var succeeded, failed int64
			for _, result := range results {
				state, _ := result["status"].(string)
				if state == "pending" || state == "running" {
					result["status"] = "canceled"
					result["error"] = "Exécution interrompue avant la restauration"
				}
				if state == "ok" {
					succeeded++
				} else {
					failed++
				}
			}
			data, err := json.Marshal(results)
			if err != nil {
				return err
			}
			text := string(data)
			finished := time.Now().UnixMilli()
			row[columns["results"]] = store.SnapshotCell{Text: &text}
			row[columns["finished_at"]] = store.SnapshotCell{Number: &finished}
			row[columns["ok"]] = store.SnapshotCell{Number: &succeeded}
			row[columns["failed"]] = store.SnapshotCell{Number: &failed}
		}
	}
	return nil
}

// Instance backups contain every user's private data and authentication state.
func (s *Server) canManageBackups(r *http.Request) bool {
	if auth.Method(r.Context()) == auth.MethodToken {
		return false
	}
	if s.localMode() {
		return s.localAdministrator(r)
	}
	for _, user := range s.opt.AdminUsers {
		if strings.EqualFold(strings.TrimSpace(user), auth.User(r.Context())) {
			return true
		}
	}
	return false
}
func (s *Server) backupGuard(w http.ResponseWriter, r *http.Request) bool {
	if !s.canManageBackups(r) {
		writeErr(w, http.StatusForbidden, "sauvegardes réservées aux administrateurs configurés, via une session web")
		return false
	}
	return true
}

func (s *Server) backupLocalProof(r *http.Request) [][]byte {
	if s.localMode() {
		return [][]byte{localProof(r)}
	}
	return nil
}
