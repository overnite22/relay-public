//! Relay server data source, authenticated with a device token
//! (`Authorization: Bearer rlt_…`) created in Administration → Appareils.

use crate::model::*;
use crate::source::{DataSource, Error, Result};
use serde::de::DeserializeOwned;
use serde::Serialize;
use std::time::Duration;
use zeroize::Zeroizing;

pub struct ServerSource {
    base: String,
    token: Zeroizing<String>,
    agent: ureq::Agent,
    user: String,
}

fn seg(id: &str) -> String {
    url::form_urlencoded::byte_serialize(id.as_bytes()).collect()
}

impl ServerSource {
    /// Validates the token against `/api/me` and returns a ready source.
    pub fn connect(server: &str, token: &str, user_agent: &str) -> Result<Self> {
        let tls = ureq::tls::TlsConfig::builder()
            .root_certs(ureq::tls::RootCerts::PlatformVerifier)
            .build();
        let agent: ureq::Agent = ureq::Agent::config_builder()
            .timeout_global(Some(Duration::from_secs(30)))
            .http_status_as_error(false)
            .max_redirects(0)
            .user_agent(user_agent)
            .tls_config(tls)
            .build()
            .into();
        let mut s = ServerSource {
            base: server.trim_end_matches('/').to_owned(),
            token: Zeroizing::new(token.to_owned()),
            agent,
            user: String::new(),
        };
        #[derive(serde::Deserialize)]
        struct Me {
            user: String,
        }
        s.user = s.call::<Me>("GET", "/api/me", None::<&()>)?.user;
        Ok(s)
    }

    pub fn user(&self) -> &str {
        &self.user
    }

    pub fn server(&self) -> &str {
        &self.base
    }

    fn call<T: DeserializeOwned>(&self, method: &str, path: &str, body: Option<&impl Serialize>) -> Result<T> {
        let text = self.send(method, path, body)?;
        let text = if text.trim().is_empty() { "null" } else { text.as_str() };
        serde_json::from_str(text).map_err(|e| Error::Server(format!("réponse illisible du serveur : {e}")))
    }

    fn send(&self, method: &str, path: &str, body: Option<&impl Serialize>) -> Result<String> {
        let req = ureq::http::Request::builder()
            .method(method)
            .uri(format!("{}{}", self.base, path))
            .header("Authorization", format!("Bearer {}", self.token.as_str()))
            .header("X-Relay", "1")
            .header("Accept", "application/json");
        let resp = match body {
            Some(b) => {
                let bytes = serde_json::to_vec(b).map_err(|e| Error::Invalid(e.to_string()))?;
                let req = req
                    .header("Content-Type", "application/json")
                    .body(bytes)
                    .map_err(|e| Error::Invalid(e.to_string()))?;
                self.agent.run(req)
            }
            None => self.agent.run(req.body(()).map_err(|e| Error::Invalid(e.to_string()))?),
        };
        let mut resp = resp.map_err(|e| Error::Network(e.to_string()))?;
        let status = resp.status().as_u16();
        let text = resp.body_mut().with_config().limit(64 << 20).read_to_string().unwrap_or_default();
        match status {
            200..=299 => Ok(text),
            401 => Err(Error::Unauthorized),
            404 => Err(Error::NotFound),
            300..=399 | 403 if !text.trim_start().starts_with('{') => Err(Error::Server(
                "accès bloqué avant Relay : si un SSO protège le serveur, il doit laisser passer /api/ avec un jeton d'appareil".into(),
            )),
            _ => {
                let msg = serde_json::from_str::<serde_json::Value>(&text)
                    .ok()
                    .and_then(|v| v.get("error").and_then(|e| e.as_str()).map(str::to_owned))
                    .unwrap_or_else(|| format!("erreur HTTP {status}"));
                Err(Error::Server(msg))
            }
        }
    }
}

impl DataSource for ServerSource {
    fn tree(&self) -> Result<Tree> {
        self.call("GET", "/api/tree", None::<&()>)
    }

    fn save_folder(&self, f: &Folder) -> Result<Folder> {
        let body = serde_json::json!({ "name": f.name.trim(), "parentId": f.parent_id });
        if f.id.is_empty() {
            self.call("POST", "/api/folders", Some(&body))
        } else {
            self.call("PUT", &format!("/api/folders/{}", seg(&f.id)), Some(&body))
        }
    }

    fn delete_folder(&self, id: &str) -> Result<()> {
        self.send("DELETE", &format!("/api/folders/{}", seg(id)), None::<&()>).map(|_| ())
    }

    fn save_entry(&self, e: &Entry) -> Result<Entry> {
        let body = EntryInput::from(e);
        if e.id.is_empty() {
            self.call("POST", "/api/entries", Some(&body))
        } else {
            self.call("PUT", &format!("/api/entries/{}", seg(&e.id)), Some(&body))
        }
    }

    fn delete_entry(&self, id: &str) -> Result<()> {
        self.send("DELETE", &format!("/api/entries/{}", seg(id)), None::<&()>).map(|_| ())
    }

    fn credentials(&self) -> Result<Vec<Credential>> {
        self.call("GET", "/api/credentials", None::<&()>)
    }

    fn save_credential(&self, id: Option<&str>, input: &CredentialInput) -> Result<Credential> {
        match id {
            None => self.call("POST", "/api/credentials", Some(input)),
            Some(id) => self.call("PUT", &format!("/api/credentials/{}", seg(id)), Some(input)),
        }
    }

    fn delete_credential(&self, id: &str) -> Result<()> {
        self.send("DELETE", &format!("/api/credentials/{}", seg(id)), None::<&()>)
            .map(|_| ())
    }

    fn reveal(&self, credential_id: &str, field: SecretField) -> Result<String> {
        #[derive(serde::Deserialize)]
        struct V {
            value: String,
        }
        let body = serde_json::json!({ "field": field.as_str() });
        Ok(self
            .call::<V>("POST", &format!("/api/credentials/{}/reveal", seg(credential_id)), Some(&body))?
            .value)
    }
}
