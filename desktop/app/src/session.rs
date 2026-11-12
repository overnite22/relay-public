//! Opens sessions with the tools shipped with Windows until the native
//! embedded sessions land: mstsc for RDP, OpenSSH for SSH, the default
//! browser for web consoles.

use relay_desktop_core::Kind;
use zeroize::Zeroizing;

pub struct Target {
    pub kind: Kind,
    pub host: String,
    pub port: u16,
    pub url: String,
    pub username: String,
    pub domain: String,
    pub password: Option<Zeroizing<String>>,
}

/// Rejects values that a launched tool could read as options or that hold
/// control characters.
fn safe(value: &str, what: &str) -> Result<(), String> {
    if value.starts_with('-') || value.chars().any(|c| c.is_control() || c.is_whitespace() || c == '"') {
        return Err(format!("{what} invalide : « {value} »"));
    }
    Ok(())
}

pub fn open(t: Target) -> Result<String, String> {
    match t.kind {
        Kind::Rdp => {
            safe(&t.host, "hôte")?;
            platform::rdp(&t)
        }
        Kind::Ssh => {
            safe(&t.host, "hôte")?;
            if !t.username.is_empty() {
                safe(&t.username, "utilisateur")?;
            }
            platform::ssh(&t)
        }
        Kind::Web | Kind::Pve => {
            let url = if !t.url.is_empty() {
                t.url.clone()
            } else {
                format!("https://{}:{}", t.host, t.port)
            };
            if !(url.starts_with("https://") || url.starts_with("http://")) || url.chars().any(|c| c.is_control() || c == '"') {
                return Err("seules les adresses http(s) peuvent être ouvertes".into());
            }
            platform::browser(&url)
        }
        Kind::Vnc => Err("Le VNC natif arrive dans une prochaine version.".into()),
    }
}

#[cfg(windows)]
mod platform {
    use super::Target;
    use std::os::windows::process::CommandExt;
    use std::path::PathBuf;
    use std::process::Command;
    use windows::core::{HSTRING, PWSTR};
    use windows::Win32::Security::Credentials::*;

    const CREATE_NEW_CONSOLE: u32 = 0x0000_0010;

    fn system32(exe: &str) -> PathBuf {
        let root = std::env::var_os("SystemRoot")
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from(r"C:\Windows"));
        root.join("System32").join(exe)
    }

    fn cred_exists(target: &HSTRING) -> bool {
        let mut out: *mut CREDENTIALW = std::ptr::null_mut();
        let found = unsafe { CredReadW(target, CRED_TYPE_DOMAIN_PASSWORD, None, &mut out) }.is_ok();
        if !out.is_null() {
            unsafe { CredFree(out as *const _) };
        }
        found
    }

    /// Session-only credential consumed by mstsc, removed shortly after.
    fn cred_write(target: &HSTRING, user: &str, password: &str) -> Result<(), String> {
        let user = HSTRING::from(user);
        let mut blob: Vec<u16> = password.encode_utf16().collect();
        let cred = CREDENTIALW {
            Type: CRED_TYPE_DOMAIN_PASSWORD,
            TargetName: PWSTR(target.as_ptr() as *mut _),
            CredentialBlobSize: (blob.len() * 2) as u32,
            CredentialBlob: blob.as_mut_ptr() as *mut u8,
            Persist: CRED_PERSIST_SESSION,
            UserName: PWSTR(user.as_ptr() as *mut _),
            ..Default::default()
        };
        let r = unsafe { CredWriteW(&cred, 0) };
        blob.iter_mut().for_each(|c| *c = 0);
        r.map_err(|e| format!("enregistrement de l'identifiant RDP impossible : {e}"))
    }

    pub fn rdp(t: &Target) -> Result<String, String> {
        let user = match (t.domain.is_empty(), t.username.is_empty()) {
            (_, true) => String::new(),
            (true, false) => t.username.clone(),
            (false, false) => format!("{}\\{}", t.domain, t.username),
        };
        let dir = std::env::temp_dir().join("Relay");
        std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
        let file = dir.join(format!("{}.rdp", uuid::Uuid::new_v4()));
        let mut lines = vec![
            format!("full address:s:{}:{}", t.host, t.port),
            "screen mode id:i:1".into(),
            "use multimon:i:0".into(),
            "authentication level:i:2".into(),
            "prompt for credentials:i:0".into(),
            "redirectclipboard:i:1".into(),
        ];
        if !user.is_empty() {
            lines.push(format!("username:s:{user}"));
        }
        let text = lines.join("\r\n") + "\r\n";
        let mut bytes = vec![0xFF, 0xFE];
        bytes.extend(text.encode_utf16().flat_map(|u| u.to_le_bytes()));
        std::fs::write(&file, bytes).map_err(|e| e.to_string())?;

        let target = HSTRING::from(format!("TERMSRV/{}", t.host));
        let mut wrote = false;
        if let (Some(pw), false) = (&t.password, user.is_empty()) {
            if !pw.is_empty() && !cred_exists(&target) {
                cred_write(&target, &user, pw)?;
                wrote = true;
            }
        }
        let spawned = Command::new(system32("mstsc.exe")).arg(&file).spawn();
        std::thread::spawn(move || {
            std::thread::sleep(std::time::Duration::from_secs(30));
            if wrote {
                let _ = unsafe { CredDeleteW(&target, CRED_TYPE_DOMAIN_PASSWORD, None) };
            }
            let _ = std::fs::remove_file(file);
        });
        spawned.map_err(|e| format!("mstsc introuvable : {e}"))?;
        Ok(format!("Connexion Bureau à distance lancée vers {}", t.host))
    }

    pub fn ssh(t: &Target) -> Result<String, String> {
        let dest = if t.username.is_empty() {
            t.host.clone()
        } else {
            format!("{}@{}", t.username, t.host)
        };
        Command::new(system32(r"OpenSSH\ssh.exe"))
            .args(["-p", &t.port.to_string(), "--", &dest])
            .creation_flags(CREATE_NEW_CONSOLE)
            .spawn()
            .map_err(|e| format!("client OpenSSH introuvable : {e}"))?;
        Ok(format!("Terminal SSH ouvert vers {dest}"))
    }

    pub fn browser(url: &str) -> Result<String, String> {
        Command::new(system32("rundll32.exe"))
            .args(["url.dll,FileProtocolHandler", url])
            .spawn()
            .map_err(|e| e.to_string())?;
        Ok(format!("Ouvert dans le navigateur : {url}"))
    }
}

#[cfg(not(windows))]
mod platform {
    use super::Target;
    const MSG: &str = "disponible uniquement sous Windows";
    pub fn rdp(_: &Target) -> Result<String, String> {
        Err(MSG.into())
    }
    pub fn ssh(_: &Target) -> Result<String, String> {
        Err(MSG.into())
    }
    pub fn browser(_: &str) -> Result<String, String> {
        Err(MSG.into())
    }
}
