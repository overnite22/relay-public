use std::net::IpAddr;
use url::Url;

pub const TOKEN_PREFIX: &str = "rlt_";

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Pairing {
    /// Server origin without trailing slash.
    pub server: String,
    pub token: String,
}

#[derive(Debug, thiserror::Error, PartialEq, Eq)]
pub enum PairingError {
    #[error("lien d'appairage invalide")]
    InvalidLink,
    #[error("adresse du serveur invalide")]
    InvalidServer,
    #[error("le serveur doit utiliser HTTPS")]
    InsecureServer,
    #[error("jeton d'appareil invalide")]
    InvalidToken,
}

/// Accepts either a `relay://pair?server=…&token=…` link or a server
/// address plus a token copied from Administration → Appareils.
pub fn parse(server_or_link: &str, token: &str) -> Result<Pairing, PairingError> {
    let input = server_or_link.trim();
    if input.starts_with("relay:") {
        let link = Url::parse(input).map_err(|_| PairingError::InvalidLink)?;
        if link.host_str() != Some("pair") {
            return Err(PairingError::InvalidLink);
        }
        let get = |k: &str| link.query_pairs().find(|(n, _)| n == k).map(|(_, v)| v.into_owned());
        let (Some(server), Some(token)) = (get("server"), get("token")) else {
            return Err(PairingError::InvalidLink);
        };
        return Ok(Pairing {
            server: normalize_server(&server)?,
            token: check_token(&token)?,
        });
    }
    Ok(Pairing {
        server: normalize_server(input)?,
        token: check_token(token)?,
    })
}

/// Reduces a server address to its origin and enforces HTTPS, except for
/// loopback addresses used by local test instances.
pub fn normalize_server(raw: &str) -> Result<String, PairingError> {
    let raw = raw.trim();
    let with_scheme = if raw.contains("://") {
        raw.to_owned()
    } else {
        format!("https://{raw}")
    };
    let url = Url::parse(&with_scheme).map_err(|_| PairingError::InvalidServer)?;
    if !url.username().is_empty() || url.password().is_some() || url.host_str().is_none() {
        return Err(PairingError::InvalidServer);
    }
    match url.scheme() {
        "https" => {}
        "http" if is_loopback(&url) => {}
        "http" => return Err(PairingError::InsecureServer),
        _ => return Err(PairingError::InvalidServer),
    }
    Ok(url.origin().ascii_serialization())
}

fn is_loopback(url: &Url) -> bool {
    match url.host() {
        Some(url::Host::Domain(d)) => d.eq_ignore_ascii_case("localhost"),
        Some(url::Host::Ipv4(ip)) => IpAddr::V4(ip).is_loopback(),
        Some(url::Host::Ipv6(ip)) => IpAddr::V6(ip).is_loopback(),
        None => false,
    }
}

fn check_token(raw: &str) -> Result<String, PairingError> {
    let token = raw.trim();
    let secret = token.strip_prefix(TOKEN_PREFIX).ok_or(PairingError::InvalidToken)?;
    let valid = (32..=128).contains(&secret.len()) && secret.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_');
    if valid {
        Ok(token.to_owned())
    } else {
        Err(PairingError::InvalidToken)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    const TOK: &str = "rlt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";

    #[test]
    fn parses_link_and_fields() {
        let link = format!("relay://pair?server=https%3A%2F%2Frelay.example.com%2Fx&token={TOK}");
        let p = parse(&link, "").unwrap();
        assert_eq!(p.server, "https://relay.example.com");
        assert_eq!(p.token, TOK);
        assert_eq!(
            parse("relay.example.com:8443", TOK).unwrap().server,
            "https://relay.example.com:8443"
        );
        assert_eq!(parse("http://127.0.0.1:9000", TOK).unwrap().server, "http://127.0.0.1:9000");
    }

    #[test]
    fn rejects_unsafe_input() {
        assert_eq!(parse("http://relay.lan", TOK), Err(PairingError::InsecureServer));
        assert_eq!(parse("https://u:p@relay.lan", TOK), Err(PairingError::InvalidServer));
        assert_eq!(parse("file:///etc", TOK), Err(PairingError::InvalidServer));
        assert_eq!(parse("https://relay.lan", "rlt_short"), Err(PairingError::InvalidToken));
        assert_eq!(
            parse("https://relay.lan", &format!("{TOK}\r\nX: y")),
            Err(PairingError::InvalidToken)
        );
        assert_eq!(parse("relay://evil?server=https://a&token=x", ""), Err(PairingError::InvalidLink));
    }
}
