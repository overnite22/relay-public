//! Local data source: a single SQLite file. Inventory is stored in clear like
//! an RDM local data source; every credential secret is sealed with a key
//! derived from the master password (see [`crate::vault`]).

use crate::model::*;
use crate::source::{DataSource, Error, Result};
use crate::vault::{random_salt, Key};
use rusqlite::{params, Connection, OptionalExtension};
use serde::{Deserialize, Serialize};
use std::path::Path;
use std::sync::Mutex;
use zeroize::{Zeroize, ZeroizeOnDrop};

const VERIFIER: &[u8] = b"relay-local-vault-v1";
pub const MIN_PASSWORD: usize = 8;

const SCHEMA: &str = "
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS folders (
  id TEXT PRIMARY KEY, parent_id TEXT REFERENCES folders(id), name TEXT NOT NULL,
  credentials TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS credentials (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, username TEXT NOT NULL, domain TEXT NOT NULL,
  notes TEXT NOT NULL, flags INTEGER NOT NULL, secrets BLOB NOT NULL,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS entries (
  id TEXT PRIMARY KEY, folder_id TEXT REFERENCES folders(id), name TEXT NOT NULL, kind TEXT NOT NULL,
  host TEXT NOT NULL, port INTEGER NOT NULL, url TEXT NOT NULL, username TEXT NOT NULL,
  credential_id TEXT REFERENCES credentials(id), settings TEXT NOT NULL, notes TEXT NOT NULL,
  favorite INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
";

#[derive(Default, Serialize, Deserialize, Zeroize, ZeroizeOnDrop)]
#[serde(rename_all = "camelCase")]
struct Secrets {
    #[serde(default)]
    password: String,
    #[serde(default)]
    private_key: String,
    #[serde(default)]
    passphrase: String,
    #[serde(default)]
    totp: String,
}

impl Secrets {
    fn flags(&self) -> i64 {
        [&self.password, &self.private_key, &self.passphrase, &self.totp]
            .iter()
            .enumerate()
            .fold(0, |acc, (i, v)| if v.is_empty() { acc } else { acc | 1 << i })
    }
}

pub struct LocalSource {
    conn: Mutex<Connection>,
    key: Key,
}

fn db(e: rusqlite::Error) -> Error {
    Error::Database(e.to_string())
}

fn now() -> String {
    time::OffsetDateTime::now_utc()
        .format(&time::format_description::well_known::Rfc3339)
        .unwrap_or_default()
}

fn new_id() -> String {
    uuid::Uuid::new_v4().to_string()
}

impl LocalSource {
    /// Creates a new database protected by `password`.
    pub fn create(path: &Path, password: &str) -> Result<Self> {
        if password.chars().count() < MIN_PASSWORD {
            return Err(Error::Invalid(format!(
                "le mot de passe maître doit faire au moins {MIN_PASSWORD} caractères"
            )));
        }
        if path.exists() {
            return Err(Error::Invalid("ce fichier existe déjà".into()));
        }
        let conn = Connection::open(path).map_err(db)?;
        conn.execute_batch(SCHEMA).map_err(db)?;
        let salt = random_salt();
        let key = Key::derive(password, &salt);
        conn.execute(
            "INSERT INTO meta(key, value) VALUES ('salt', ?1), ('verifier', ?2)",
            params![&salt[..], key.seal(b"verifier", VERIFIER)],
        )
        .map_err(db)?;
        Self::finish(conn, key)
    }

    /// Opens an existing database; fails with [`Error::BadPassword`].
    pub fn open(path: &Path, password: &str) -> Result<Self> {
        if !path.is_file() {
            return Err(Error::Invalid("fichier de base locale introuvable".into()));
        }
        let conn = Connection::open(path).map_err(db)?;
        let get = |k: &str| -> Result<Vec<u8>> {
            conn.query_row("SELECT value FROM meta WHERE key = ?1", [k], |r| r.get(0))
                .optional()
                .map_err(db)?
                .ok_or_else(|| Error::Invalid("ce fichier n'est pas une base Relay".into()))
        };
        let (salt, verifier) = (get("salt")?, get("verifier")?);
        let key = Key::derive(password, &salt);
        if key.open(b"verifier", &verifier).as_deref().map(|v| v.as_slice()) != Some(VERIFIER) {
            return Err(Error::BadPassword);
        }
        conn.execute_batch(SCHEMA).map_err(db)?;
        Self::finish(conn, key)
    }

    fn finish(conn: Connection, key: Key) -> Result<Self> {
        conn.execute_batch("PRAGMA foreign_keys = ON; PRAGMA secure_delete = ON;")
            .map_err(db)?;
        Ok(LocalSource {
            conn: Mutex::new(conn),
            key,
        })
    }

    fn folder_exists(conn: &Connection, id: &str) -> Result<bool> {
        conn.query_row("SELECT 1 FROM folders WHERE id = ?1", [id], |_| Ok(()))
            .optional()
            .map(|o| o.is_some())
            .map_err(db)
    }

    fn credential(conn: &Connection, id: &str) -> Result<(Credential, Vec<u8>)> {
        conn.query_row(
            "SELECT id, name, username, domain, notes, flags, created_at, updated_at, secrets FROM credentials WHERE id = ?1",
            [id],
            |r| {
                let flags: i64 = r.get(5)?;
                Ok((
                    Credential {
                        id: r.get(0)?,
                        name: r.get(1)?,
                        username: r.get(2)?,
                        domain: r.get(3)?,
                        notes: r.get(4)?,
                        has_password: flags & 1 != 0,
                        has_private_key: flags & 2 != 0,
                        has_passphrase: flags & 4 != 0,
                        has_totp: flags & 8 != 0,
                        created_at: r.get(6)?,
                        updated_at: r.get(7)?,
                    },
                    r.get(8)?,
                ))
            },
        )
        .optional()
        .map_err(db)?
        .ok_or(Error::NotFound)
    }

    fn secrets(&self, id: &str, sealed: &[u8]) -> Result<Secrets> {
        let plain = self
            .key
            .open(id.as_bytes(), sealed)
            .ok_or_else(|| Error::Database("secret illisible".into()))?;
        serde_json::from_slice(&plain).map_err(|e| Error::Database(e.to_string()))
    }
}

impl DataSource for LocalSource {
    fn tree(&self) -> Result<Tree> {
        let conn = self.conn.lock().unwrap();
        let mut st = conn
            .prepare("SELECT id, parent_id, name, credentials, created_at, updated_at FROM folders")
            .map_err(db)?;
        let folders = st
            .query_map([], |r| {
                Ok(Folder {
                    id: r.get(0)?,
                    parent_id: r.get(1)?,
                    name: r.get(2)?,
                    credentials: serde_json::from_str(&r.get::<_, String>(3)?).unwrap_or_default(),
                    created_at: r.get(4)?,
                    updated_at: r.get(5)?,
                })
            })
            .map_err(db)?
            .collect::<rusqlite::Result<Vec<_>>>()
            .map_err(db)?;
        let mut st = conn
            .prepare(
                "SELECT id, folder_id, name, kind, host, port, url, username, credential_id, settings, notes, favorite, created_at, updated_at FROM entries",
            )
            .map_err(db)?;
        let entries = st
            .query_map([], |r| {
                let kind: String = r.get(3)?;
                Ok(Entry {
                    id: r.get(0)?,
                    folder_id: r.get(1)?,
                    name: r.get(2)?,
                    kind: Kind::parse(&kind).unwrap_or(Kind::Ssh),
                    host: r.get(4)?,
                    port: r.get(5)?,
                    url: r.get(6)?,
                    username: r.get(7)?,
                    credential_id: r.get(8)?,
                    settings: serde_json::from_str(&r.get::<_, String>(9)?).unwrap_or_default(),
                    notes: r.get(10)?,
                    favorite: r.get(11)?,
                    source_id: None,
                    created_at: r.get(12)?,
                    updated_at: r.get(13)?,
                })
            })
            .map_err(db)?
            .collect::<rusqlite::Result<Vec<_>>>()
            .map_err(db)?;
        Ok(Tree { folders, entries })
    }

    fn save_folder(&self, folder: &Folder) -> Result<Folder> {
        let name = folder.name.trim();
        if name.is_empty() {
            return Err(Error::Invalid("le nom du dossier est obligatoire".into()));
        }
        let conn = self.conn.lock().unwrap();
        let id = if folder.id.is_empty() { new_id() } else { folder.id.clone() };
        // Refuse a parent that does not exist or lies inside the folder itself.
        let mut cursor = folder.parent_id.clone();
        while let Some(p) = cursor {
            if p == id {
                return Err(Error::Invalid("un dossier ne peut pas être placé dans lui-même".into()));
            }
            cursor = conn
                .query_row("SELECT parent_id FROM folders WHERE id = ?1", [&p], |r| {
                    r.get::<_, Option<String>>(0)
                })
                .optional()
                .map_err(db)?
                .ok_or_else(|| Error::Invalid("dossier parent introuvable".into()))?;
        }
        let creds = serde_json::to_string(&folder.credentials).unwrap_or_else(|_| "{}".into());
        let ts = now();
        if folder.id.is_empty() {
            conn.execute(
                "INSERT INTO folders(id, parent_id, name, credentials, created_at, updated_at) VALUES (?1, ?2, ?3, ?4, ?5, ?5)",
                params![id, folder.parent_id, name, creds, ts],
            )
            .map_err(db)?;
        } else if conn
            .execute(
                "UPDATE folders SET parent_id = ?2, name = ?3, credentials = ?4, updated_at = ?5 WHERE id = ?1",
                params![id, folder.parent_id, name, creds, ts],
            )
            .map_err(db)?
            == 0
        {
            return Err(Error::NotFound);
        }
        drop(conn);
        self.tree()?.folders.into_iter().find(|f| f.id == id).ok_or(Error::NotFound)
    }

    fn delete_folder(&self, id: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        let used: i64 = conn
            .query_row(
                "SELECT (SELECT count(*) FROM folders WHERE parent_id = ?1) + (SELECT count(*) FROM entries WHERE folder_id = ?1)",
                [id],
                |r| r.get(0),
            )
            .map_err(db)?;
        if used > 0 {
            return Err(Error::Invalid("le dossier n'est pas vide".into()));
        }
        match conn.execute("DELETE FROM folders WHERE id = ?1", [id]).map_err(db)? {
            0 => Err(Error::NotFound),
            _ => Ok(()),
        }
    }

    fn save_entry(&self, e: &Entry) -> Result<Entry> {
        if e.name.trim().is_empty() {
            return Err(Error::Invalid("le nom est obligatoire".into()));
        }
        if e.kind != Kind::Web && e.host.trim().is_empty() {
            return Err(Error::Invalid("l'hôte est obligatoire".into()));
        }
        let conn = self.conn.lock().unwrap();
        if let Some(f) = &e.folder_id {
            if !Self::folder_exists(&conn, f)? {
                return Err(Error::Invalid("dossier introuvable".into()));
            }
        }
        if let Some(c) = e.credential_id.as_deref() {
            Self::credential(&conn, c).map_err(|_| Error::Invalid("identifiant introuvable".into()))?;
        }
        let id = if e.id.is_empty() { new_id() } else { e.id.clone() };
        let settings = serde_json::to_string(&e.settings).unwrap_or_else(|_| "{}".into());
        let ts = now();
        let p = params![
            id,
            e.folder_id,
            e.name.trim(),
            e.kind.as_str(),
            e.host.trim(),
            e.port,
            e.url.trim(),
            e.username.trim(),
            e.credential_id,
            settings,
            e.notes,
            e.favorite,
            ts
        ];
        let n = if e.id.is_empty() {
            conn.execute("INSERT INTO entries VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?13)", p)
        } else {
            conn.execute(
                "UPDATE entries SET folder_id=?2, name=?3, kind=?4, host=?5, port=?6, url=?7, username=?8, credential_id=?9, settings=?10, notes=?11, favorite=?12, updated_at=?13 WHERE id=?1",
                p,
            )
        }
        .map_err(db)?;
        if n == 0 {
            return Err(Error::NotFound);
        }
        drop(conn);
        self.tree()?.entries.into_iter().find(|x| x.id == id).ok_or(Error::NotFound)
    }

    fn delete_entry(&self, id: &str) -> Result<()> {
        match self
            .conn
            .lock()
            .unwrap()
            .execute("DELETE FROM entries WHERE id = ?1", [id])
            .map_err(db)?
        {
            0 => Err(Error::NotFound),
            _ => Ok(()),
        }
    }

    fn credentials(&self) -> Result<Vec<Credential>> {
        let conn = self.conn.lock().unwrap();
        let ids: Vec<String> = conn
            .prepare("SELECT id FROM credentials ORDER BY name COLLATE NOCASE")
            .map_err(db)?
            .query_map([], |r| r.get(0))
            .map_err(db)?
            .collect::<rusqlite::Result<_>>()
            .map_err(db)?;
        ids.iter().map(|id| Self::credential(&conn, id).map(|(c, _)| c)).collect()
    }

    fn save_credential(&self, id: Option<&str>, input: &CredentialInput) -> Result<Credential> {
        if input.name.trim().is_empty() {
            return Err(Error::Invalid("le nom de l'identifiant est obligatoire".into()));
        }
        let conn = self.conn.lock().unwrap();
        let (id, mut secrets) = match id {
            Some(id) => {
                let (_, sealed) = Self::credential(&conn, id)?;
                (id.to_owned(), self.secrets(id, &sealed)?)
            }
            None => (new_id(), Secrets::default()),
        };
        for (slot, value) in [
            (&mut secrets.password, &input.password),
            (&mut secrets.private_key, &input.private_key),
            (&mut secrets.passphrase, &input.passphrase),
            (&mut secrets.totp, &input.totp),
        ] {
            if let Some(v) = value {
                slot.zeroize();
                *slot = v.clone();
            }
        }
        let mut plain = serde_json::to_vec(&secrets).map_err(|e| Error::Database(e.to_string()))?;
        let sealed = self.key.seal(id.as_bytes(), &plain);
        plain.zeroize();
        let ts = now();
        conn.execute(
            "INSERT INTO credentials(id, name, username, domain, notes, flags, secrets, created_at, updated_at)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?8)
             ON CONFLICT(id) DO UPDATE SET name=?2, username=?3, domain=?4, notes=?5, flags=?6, secrets=?7, updated_at=?8",
            params![
                id,
                input.name.trim(),
                input.username.trim(),
                input.domain.trim(),
                input.notes,
                secrets.flags(),
                sealed,
                ts
            ],
        )
        .map_err(db)?;
        Self::credential(&conn, &id).map(|(c, _)| c)
    }

    fn delete_credential(&self, id: &str) -> Result<()> {
        let conn = self.conn.lock().unwrap();
        let used: i64 = conn
            .query_row("SELECT count(*) FROM entries WHERE credential_id = ?1", [id], |r| r.get(0))
            .map_err(db)?;
        if used > 0 {
            return Err(Error::Invalid(format!("identifiant utilisé par {used} entrée(s)")));
        }
        match conn.execute("DELETE FROM credentials WHERE id = ?1", [id]).map_err(db)? {
            0 => Err(Error::NotFound),
            _ => Ok(()),
        }
    }

    fn reveal(&self, credential_id: &str, field: SecretField) -> Result<String> {
        let conn = self.conn.lock().unwrap();
        let (_, sealed) = Self::credential(&conn, credential_id)?;
        let s = self.secrets(credential_id, &sealed)?;
        Ok(match field {
            SecretField::Password => s.password.clone(),
            SecretField::PrivateKey => s.private_key.clone(),
            SecretField::Passphrase => s.passphrase.clone(),
            SecretField::Totp => s.totp.clone(),
        })
    }
}
