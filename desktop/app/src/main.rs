// Hide the console window in release builds.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use relay_desktop_core::{start, ConfigFile, SecretStore};
use std::sync::Arc;
use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};

/// Device token stored in the Windows Credential Manager (DPAPI-protected,
/// per user). It never reaches the webview.
struct CredentialStore(keyring::Entry);

impl SecretStore for CredentialStore {
    fn load(&self) -> Result<Option<String>, String> {
        match self.0.get_password() {
            Ok(v) => Ok(Some(v)),
            Err(keyring::Error::NoEntry) => Ok(None),
            Err(e) => Err(e.to_string()),
        }
    }
    fn save(&self, token: &str) -> Result<(), String> {
        self.0.set_password(token).map_err(|e| e.to_string())
    }
    fn clear(&self) -> Result<(), String> {
        match self.0.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(e) => Err(e.to_string()),
        }
    }
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(w) = app.get_webview_window("main") {
                let _ = w.unminimize();
                let _ = w.set_focus();
            }
        }))
        .plugin(tauri_plugin_opener::init())
        .setup(|app| {
            let config = ConfigFile::new(app.path().app_config_dir()?.join("config.json"));
            let store = Arc::new(CredentialStore(keyring::Entry::new("Relay Desktop", "device-token")?));
            let agent = format!("Relay-Desktop/{}", env!("CARGO_PKG_VERSION"));
            let running = tauri::async_runtime::block_on(start(config, store, &agent))?;
            let port = running.port;
            let handle = app.handle().clone();
            WebviewWindowBuilder::new(app, "main", WebviewUrl::External(running.boot_url.parse()?))
                .title("Relay")
                .inner_size(1440.0, 900.0)
                .min_inner_size(960.0, 600.0)
                .center()
                // Only the loopback proxy may be displayed; other links open in
                // the default browser so remote pages never run in this window.
                .on_navigation(move |url| {
                    let local = url.scheme() == "http" && url.host_str() == Some("127.0.0.1") && url.port() == Some(port);
                    if !local && matches!(url.scheme(), "http" | "https") {
                        use tauri_plugin_opener::OpenerExt;
                        let _ = handle.opener().open_url(url.as_str(), None::<&str>);
                    }
                    local
                })
                .build()?;
            app.manage(running);
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("Relay Desktop failed to start");
}
