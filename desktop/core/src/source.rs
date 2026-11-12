use crate::model::*;

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("serveur injoignable : {0}")]
    Network(String),
    #[error("{0}")]
    Server(String),
    #[error("accès refusé : le jeton d'appareil a peut-être été révoqué")]
    Unauthorized,
    #[error("élément introuvable")]
    NotFound,
    #[error("mot de passe maître incorrect")]
    BadPassword,
    #[error("{0}")]
    Invalid(String),
    #[error("base locale : {0}")]
    Database(String),
}

pub type Result<T> = std::result::Result<T, Error>;

/// A place where folders, entries and credentials live. Calls block and are
/// made from worker threads, never from the UI thread.
pub trait DataSource: Send + Sync {
    fn tree(&self) -> Result<Tree>;
    /// Creates the folder when `id` is empty, updates it otherwise.
    fn save_folder(&self, folder: &Folder) -> Result<Folder>;
    fn delete_folder(&self, id: &str) -> Result<()>;
    /// Creates the entry when `id` is empty, updates it otherwise.
    fn save_entry(&self, entry: &Entry) -> Result<Entry>;
    fn delete_entry(&self, id: &str) -> Result<()>;
    fn credentials(&self) -> Result<Vec<Credential>>;
    fn save_credential(&self, id: Option<&str>, input: &CredentialInput) -> Result<Credential>;
    fn delete_credential(&self, id: &str) -> Result<()>;
    fn reveal(&self, credential_id: &str, field: SecretField) -> Result<String>;
}

/// Credential used to open `entry`: its own, else the nearest folder default
/// for its protocol (same rule as the Relay server).
pub fn effective_credential(tree: &Tree, entry: &Entry) -> Option<String> {
    if let Some(id) = entry.credential_id.as_ref().filter(|s| !s.is_empty()) {
        return Some(id.clone());
    }
    let mut folder_id = entry.folder_id.clone();
    let mut guard = 0;
    while let Some(id) = folder_id {
        let folder = tree.folders.iter().find(|f| f.id == id)?;
        if let Some(Some(cred)) = folder.credentials.get(entry.kind.as_str()) {
            if !cred.is_empty() {
                return Some(cred.clone());
            }
        }
        folder_id = folder.parent_id.clone();
        guard += 1;
        if guard > 64 {
            return None;
        }
    }
    None
}
