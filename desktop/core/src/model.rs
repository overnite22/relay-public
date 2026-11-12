use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

/// Connection protocol, serialized like the Relay server (`"rdp"`, `"ssh"`…).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Kind {
    Rdp,
    Ssh,
    Vnc,
    Web,
    Pve,
}

impl Kind {
    pub const EDITABLE: [Kind; 4] = [Kind::Rdp, Kind::Ssh, Kind::Vnc, Kind::Web];

    pub fn as_str(self) -> &'static str {
        match self {
            Kind::Rdp => "rdp",
            Kind::Ssh => "ssh",
            Kind::Vnc => "vnc",
            Kind::Web => "web",
            Kind::Pve => "pve",
        }
    }
    pub fn label(self) -> &'static str {
        match self {
            Kind::Rdp => "RDP",
            Kind::Ssh => "SSH",
            Kind::Vnc => "VNC",
            Kind::Web => "Web",
            Kind::Pve => "Proxmox",
        }
    }
    pub fn default_port(self) -> u16 {
        match self {
            Kind::Rdp => 3389,
            Kind::Ssh => 22,
            Kind::Vnc => 5900,
            Kind::Web => 443,
            Kind::Pve => 8006,
        }
    }
    pub fn parse(s: &str) -> Option<Kind> {
        [Kind::Rdp, Kind::Ssh, Kind::Vnc, Kind::Web, Kind::Pve]
            .into_iter()
            .find(|k| k.as_str() == s)
    }
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Folder {
    pub id: String,
    pub parent_id: Option<String>,
    pub name: String,
    /// Default credential per protocol inherited by the folder content.
    #[serde(deserialize_with = "nullable")]
    pub credentials: BTreeMap<String, Option<String>>,
    pub created_at: String,
    pub updated_at: String,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Entry {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub folder_id: Option<String>,
    pub name: String,
    pub kind: Kind,
    #[serde(default, deserialize_with = "nullable")]
    pub host: String,
    #[serde(default)]
    pub port: u16,
    #[serde(default, deserialize_with = "nullable")]
    pub url: String,
    #[serde(default, deserialize_with = "nullable")]
    pub username: String,
    #[serde(default)]
    pub credential_id: Option<String>,
    #[serde(default, deserialize_with = "nullable")]
    pub settings: serde_json::Map<String, serde_json::Value>,
    #[serde(default, deserialize_with = "nullable")]
    pub notes: String,
    #[serde(default)]
    pub favorite: bool,
    /// Set when the entry is synchronized from NetBox/Proxmox (read-only).
    #[serde(default)]
    pub source_id: Option<String>,
    #[serde(default)]
    pub created_at: String,
    #[serde(default)]
    pub updated_at: String,
}

impl Entry {
    pub fn new(kind: Kind) -> Self {
        Entry {
            id: String::new(),
            folder_id: None,
            name: String::new(),
            kind,
            host: String::new(),
            port: kind.default_port(),
            url: String::new(),
            username: String::new(),
            credential_id: None,
            settings: Default::default(),
            notes: String::new(),
            favorite: false,
            source_id: None,
            created_at: String::new(),
            updated_at: String::new(),
        }
    }
}

/// Exact body accepted by `POST/PUT /api/entries` (unknown fields are refused).
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct EntryInput<'a> {
    pub folder_id: &'a Option<String>,
    pub name: &'a str,
    pub kind: Kind,
    pub host: &'a str,
    pub port: u16,
    pub url: &'a str,
    pub username: &'a str,
    pub credential_id: &'a Option<String>,
    pub settings: &'a serde_json::Map<String, serde_json::Value>,
    pub notes: &'a str,
    pub favorite: bool,
}

impl<'a> From<&'a Entry> for EntryInput<'a> {
    fn from(e: &'a Entry) -> Self {
        EntryInput {
            folder_id: &e.folder_id,
            name: &e.name,
            kind: e.kind,
            host: &e.host,
            port: e.port,
            url: &e.url,
            username: &e.username,
            credential_id: &e.credential_id,
            settings: &e.settings,
            notes: &e.notes,
            favorite: e.favorite,
        }
    }
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Credential {
    pub id: String,
    pub name: String,
    pub username: String,
    pub domain: String,
    pub notes: String,
    pub has_password: bool,
    pub has_private_key: bool,
    pub has_passphrase: bool,
    pub has_totp: bool,
    pub created_at: String,
    pub updated_at: String,
}

/// Secrets: `None` keeps the stored value, `Some("")` clears it.
#[derive(Debug, Clone, Default, Serialize, Deserialize, zeroize::Zeroize)]
#[serde(rename_all = "camelCase")]
pub struct CredentialInput {
    pub name: String,
    pub username: String,
    pub domain: String,
    pub notes: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub password: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub private_key: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub passphrase: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub totp: Option<String>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SecretField {
    Password,
    PrivateKey,
    Passphrase,
    Totp,
}

impl SecretField {
    pub fn as_str(self) -> &'static str {
        match self {
            SecretField::Password => "password",
            SecretField::PrivateKey => "privateKey",
            SecretField::Passphrase => "passphrase",
            SecretField::Totp => "totp",
        }
    }
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Tree {
    #[serde(deserialize_with = "nullable")]
    pub folders: Vec<Folder>,
    #[serde(deserialize_with = "nullable")]
    pub entries: Vec<Entry>,
}

/// Go encodes empty slices and maps as `null`.
fn nullable<'de, D: serde::Deserializer<'de>, T: Default + Deserialize<'de>>(d: D) -> Result<T, D::Error> {
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}
