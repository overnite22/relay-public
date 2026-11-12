//! Data source list (`%APPDATA%\Relay\settings.json`) and device tokens
//! (Windows Credential Manager, never written to the settings file).

use serde::{Deserialize, Serialize};
use std::path::PathBuf;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SourceCfg {
    pub id: String,
    pub name: String,
    #[serde(flatten)]
    pub kind: SourceKind,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "lowercase")]
pub enum SourceKind {
    Server { url: String },
    Local { path: String },
}

#[derive(Debug, Default, Serialize, Deserialize)]
pub struct Settings {
    #[serde(default)]
    pub sources: Vec<SourceCfg>,
    #[serde(default)]
    pub last: Option<String>,
}

pub fn dir() -> PathBuf {
    std::env::var_os("APPDATA")
        .map(PathBuf::from)
        .unwrap_or_else(std::env::temp_dir)
        .join("Relay")
}

impl Settings {
    pub fn load() -> Settings {
        std::fs::read(dir().join("settings.json"))
            .ok()
            .and_then(|b| serde_json::from_slice(&b).ok())
            .unwrap_or_default()
    }

    pub fn save(&self) -> Result<(), String> {
        let d = dir();
        std::fs::create_dir_all(&d).map_err(|e| e.to_string())?;
        let tmp = d.join("settings.json.tmp");
        std::fs::write(&tmp, serde_json::to_vec_pretty(self).map_err(|e| e.to_string())?).map_err(|e| e.to_string())?;
        std::fs::rename(&tmp, d.join("settings.json")).map_err(|e| e.to_string())
    }
}

fn token_entry(id: &str) -> keyring::Result<keyring::Entry> {
    keyring::Entry::new("Relay Desktop", &format!("server:{id}"))
}

pub fn save_token(id: &str, token: &str) -> Result<(), String> {
    token_entry(id)
        .and_then(|e| e.set_password(token))
        .map_err(|e| format!("Gestionnaire d'identification : {e}"))
}

pub fn load_token(id: &str) -> Option<String> {
    token_entry(id).and_then(|e| e.get_password()).ok()
}

pub fn delete_token(id: &str) {
    if let Ok(e) = token_entry(id) {
        let _ = e.delete_credential();
    }
}
