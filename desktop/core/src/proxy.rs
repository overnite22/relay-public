use crate::config::{Config, ConfigFile};
use crate::pairing;
use crate::secrets::SecretStore;
use bytes::Bytes;
use http::header::{self, HeaderMap, HeaderValue};
use http::{Method, Request, Response, StatusCode, Uri};
use http_body_util::{combinators::BoxBody, BodyExt, Empty, Full, Limited};
use hyper::body::Incoming;
use hyper_util::client::legacy::{connect::HttpConnector, Client};
use hyper_util::rt::{TokioExecutor, TokioIo};
use rust_embed::RustEmbed;
use std::convert::Infallible;
use std::net::Ipv4Addr;
use std::sync::{Arc, Mutex};
use subtle::ConstantTimeEq;
use tokio::net::TcpListener;

type Body = BoxBody<Bytes, hyper::Error>;
type HttpsClient = Client<hyper_rustls::HttpsConnector<HttpConnector>, Body>;

const COOKIE: &str = "relay_desktop";
const CSP: &str = "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; \
connect-src 'self'; worker-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'";
const HOP_BY_HOP: [&str; 9] = [
    "connection",
    "keep-alive",
    "proxy-authenticate",
    "proxy-authorization",
    "proxy-connection",
    "te",
    "trailer",
    "transfer-encoding",
    "upgrade",
];
/// Request headers never forwarded upstream: local credentials and
/// browser context that the server would treat as a foreign origin.
const STRIP_REQUEST: [&str; 5] = ["host", "cookie", "authorization", "origin", "referer"];

#[derive(RustEmbed)]
#[folder = "$CARGO_MANIFEST_DIR/../../web/dist"]
#[allow_missing = true]
struct WebUi;

#[derive(RustEmbed)]
#[folder = "$CARGO_MANIFEST_DIR/ui"]
struct LocalUi;

struct State {
    port: u16,
    secret: String,
    config_file: ConfigFile,
    config: Mutex<Config>,
    secrets: Arc<dyn SecretStore>,
    client: HttpsClient,
    user_agent: String,
}

/// A running loopback proxy.
pub struct Running {
    pub port: u16,
    /// URL the webview must open first; it sets the session cookie.
    pub boot_url: String,
    pub task: tokio::task::JoinHandle<()>,
}

/// Binds the loopback proxy, reusing the saved port when it is free.
pub async fn start(config_file: ConfigFile, secrets: Arc<dyn SecretStore>, user_agent: &str) -> std::io::Result<Running> {
    let _ = rustls::crypto::ring::default_provider().install_default();
    let mut config = config_file.load();
    let listener = match TcpListener::bind((Ipv4Addr::LOCALHOST, config.port)).await {
        Ok(l) if config.port != 0 => l,
        _ => TcpListener::bind((Ipv4Addr::LOCALHOST, 0)).await?,
    };
    let port = listener.local_addr()?.port();
    if port != config.port {
        config.port = port;
        if let Err(e) = config_file.save(&config) {
            tracing::warn!("cannot persist loopback port: {e}");
        }
    }
    let https = hyper_rustls::HttpsConnectorBuilder::new()
        .with_platform_verifier()
        .https_or_http()
        .enable_http1()
        .build();
    let secret = random_hex(32);
    let state = Arc::new(State {
        port,
        secret: secret.clone(),
        config_file,
        config: Mutex::new(config),
        secrets,
        client: Client::builder(TokioExecutor::new()).build(https),
        user_agent: user_agent.to_owned(),
    });
    let task = tokio::spawn(serve(listener, state));
    Ok(Running {
        port,
        boot_url: format!("http://127.0.0.1:{port}/__desktop/boot?k={secret}"),
        task,
    })
}

async fn serve(listener: TcpListener, state: Arc<State>) {
    loop {
        let Ok((stream, peer)) = listener.accept().await else { continue };
        if !peer.ip().is_loopback() {
            continue;
        }
        let state = state.clone();
        tokio::spawn(async move {
            let svc = hyper::service::service_fn(move |req| handle(state.clone(), req));
            let _ = hyper::server::conn::http1::Builder::new()
                .serve_connection(TokioIo::new(stream), svc)
                .with_upgrades()
                .await;
        });
    }
}

async fn handle(st: Arc<State>, req: Request<Incoming>) -> Result<Response<Body>, Infallible> {
    // Reject DNS rebinding: only our exact loopback authority is served.
    let expected = format!("127.0.0.1:{}", st.port);
    if req.headers().get(header::HOST).and_then(|h| h.to_str().ok()) != Some(expected.as_str()) {
        return Ok(text(StatusCode::MISDIRECTED_REQUEST, "hôte refusé"));
    }
    let path = req.uri().path().to_owned();
    if path == "/__desktop/boot" {
        return Ok(boot(&st, req.uri()));
    }
    if !has_session(&st, req.headers()) {
        return Ok(text(StatusCode::FORBIDDEN, "Ouvrez Relay depuis l'application Relay Desktop."));
    }
    let resp = match (req.method().clone(), path.as_str()) {
        (_, p) if p == "/api" || p.starts_with("/api/") => proxy(&st, req).await,
        (Method::GET, "/__desktop/status") => json(StatusCode::OK, &status(&st)),
        (Method::POST, "/__desktop/pair") if xrelay(&req) => pair(&st, req).await,
        (Method::POST, "/__desktop/unpair") if xrelay(&req) => unpair(&st),
        (Method::GET, p) if p.starts_with("/__desktop/") => local_asset(&p["/__desktop/".len()..]),
        (Method::GET | Method::HEAD, "/sw.js") => text(StatusCode::NOT_FOUND, "absent"),
        (Method::GET | Method::HEAD, p) => {
            if paired(&st).is_none() && !p.starts_with("/assets/") {
                redirect("/__desktop/pair.html")
            } else {
                web_asset(p)
            }
        }
        _ => text(StatusCode::FORBIDDEN, "requête refusée"),
    };
    Ok(resp)
}

fn boot(st: &State, uri: &Uri) -> Response<Body> {
    let given = uri
        .query()
        .and_then(|q| {
            url::form_urlencoded::parse(q.as_bytes())
                .find(|(k, _)| k == "k")
                .map(|(_, v)| v.into_owned())
        })
        .unwrap_or_default();
    if !bool::from(given.as_bytes().ct_eq(st.secret.as_bytes())) {
        return text(StatusCode::FORBIDDEN, "lien de démarrage invalide");
    }
    let mut r = redirect("/");
    let cookie = format!("{COOKIE}={}; Path=/; HttpOnly; SameSite=Strict", st.secret);
    r.headers_mut().insert(header::SET_COOKIE, HeaderValue::from_str(&cookie).unwrap());
    r
}

fn has_session(st: &State, headers: &HeaderMap) -> bool {
    headers
        .get_all(header::COOKIE)
        .iter()
        .filter_map(|v| v.to_str().ok())
        .flat_map(|v| v.split(';'))
        .filter_map(|kv| kv.trim().split_once('='))
        .any(|(k, v)| k == COOKIE && bool::from(v.as_bytes().ct_eq(st.secret.as_bytes())))
}

/// State-changing local endpoints require the same custom header as the
/// Relay API, which a cross-site form or simple request cannot send.
fn xrelay<B>(req: &Request<B>) -> bool {
    req.headers().get("x-relay").is_some_and(|v| v == "1")
}

fn paired(st: &State) -> Option<(String, String)> {
    let server = st.config.lock().unwrap().server.clone()?;
    let token = st.secrets.load().ok().flatten()?;
    Some((server, token))
}

fn status(st: &State) -> serde_json::Value {
    let server = st.config.lock().unwrap().server.clone();
    let token = st.secrets.load().ok().flatten().is_some();
    serde_json::json!({ "paired": server.is_some() && token, "server": server, "version": env!("CARGO_PKG_VERSION") })
}

async fn pair(st: &State, req: Request<Incoming>) -> Response<Body> {
    #[derive(serde::Deserialize)]
    struct In {
        server: String,
        #[serde(default)]
        token: String,
    }
    let Ok(body) = Limited::new(req.into_body(), 8 * 1024).collect().await else {
        return error(StatusCode::PAYLOAD_TOO_LARGE, "requête trop grande");
    };
    let Ok(input) = serde_json::from_slice::<In>(&body.to_bytes()) else {
        return error(StatusCode::BAD_REQUEST, "requête invalide");
    };
    let p = match pairing::parse(&input.server, &input.token) {
        Ok(p) => p,
        Err(e) => return error(StatusCode::BAD_REQUEST, &e.to_string()),
    };
    let user = match whoami(st, &p.server, &p.token).await {
        Ok(user) => user,
        Err(msg) => return error(StatusCode::BAD_GATEWAY, &msg),
    };
    if let Err(e) = st.secrets.save(&p.token) {
        return error(StatusCode::INTERNAL_SERVER_ERROR, &format!("stockage du jeton impossible : {e}"));
    }
    let mut cfg = st.config.lock().unwrap();
    let previous = cfg.server.replace(p.server.clone());
    if let Err(e) = st.config_file.save(&cfg) {
        cfg.server = previous;
        let _ = st.secrets.clear();
        return error(StatusCode::INTERNAL_SERVER_ERROR, &format!("enregistrement impossible : {e}"));
    }
    json(StatusCode::OK, &serde_json::json!({ "server": p.server, "user": user }))
}

async fn whoami(st: &State, server: &str, token: &str) -> Result<String, String> {
    let req = Request::get(format!("{server}/api/me"))
        .header(header::AUTHORIZATION, format!("Bearer {token}"))
        .header(header::USER_AGENT, &st.user_agent)
        .header(header::ACCEPT, "application/json")
        .body(Empty::new().map_err(|n| match n {}).boxed())
        .map_err(|_| "adresse du serveur invalide".to_owned())?;
    let resp = tokio::time::timeout(std::time::Duration::from_secs(15), st.client.request(req))
        .await
        .map_err(|_| "le serveur ne répond pas".to_owned())?
        .map_err(|e| format!("connexion au serveur impossible : {e}"))?;
    let status = resp.status();
    let body = Limited::new(resp.into_body(), 64 * 1024)
        .collect()
        .await
        .map(|b| b.to_bytes())
        .unwrap_or_default();
    let user = serde_json::from_slice::<serde_json::Value>(&body)
        .ok()
        .and_then(|v| v.get("user").and_then(|u| u.as_str()).map(str::to_owned));
    match (status, user) {
        (StatusCode::OK, Some(user)) => Ok(user),
        (StatusCode::UNAUTHORIZED, _) => Err("jeton refusé : il a peut-être été révoqué".into()),
        _ => Err(format!(
            "réponse inattendue du serveur ({status}). Si un SSO protège Relay, il doit laisser passer /api/ avec un jeton d'appareil."
        )),
    }
}

fn unpair(st: &State) -> Response<Body> {
    let _ = st.secrets.clear();
    let mut cfg = st.config.lock().unwrap();
    cfg.server = None;
    let _ = st.config_file.save(&cfg);
    json(StatusCode::OK, &serde_json::json!({ "paired": false }))
}

async fn proxy(st: &State, mut req: Request<Incoming>) -> Response<Body> {
    let Some((server, token)) = paired(st) else {
        return error(StatusCode::UNAUTHORIZED, "Relay Desktop n'est pas appairé");
    };
    let pq = req.uri().path_and_query().map(|p| p.as_str()).unwrap_or("/");
    let Ok(uri) = format!("{server}{pq}").parse::<Uri>() else {
        return error(StatusCode::BAD_REQUEST, "chemin invalide");
    };
    let upgrade = req.headers().get(header::UPGRADE).cloned();
    let client_upgrade = upgrade.as_ref().map(|_| hyper::upgrade::on(&mut req));
    let (parts, body) = req.into_parts();
    let mut out = Request::builder().method(parts.method).uri(uri);
    let headers = out.headers_mut().unwrap();
    for (name, value) in &parts.headers {
        let n = name.as_str();
        if !HOP_BY_HOP.contains(&n) && !STRIP_REQUEST.contains(&n) {
            headers.append(name, value.clone());
        }
    }
    if let Some(u) = &upgrade {
        headers.insert(header::CONNECTION, HeaderValue::from_static("upgrade"));
        headers.insert(header::UPGRADE, u.clone());
    }
    let Ok(bearer) = HeaderValue::from_str(&format!("Bearer {token}")) else {
        return error(StatusCode::UNAUTHORIZED, "jeton invalide");
    };
    headers.insert(header::AUTHORIZATION, bearer);
    if let Ok(ua) = HeaderValue::from_str(&st.user_agent) {
        headers.insert(header::USER_AGENT, ua);
    }
    let Ok(out) = out.body(body.boxed()) else {
        return error(StatusCode::BAD_REQUEST, "requête invalide");
    };
    let mut resp = match st.client.request(out).await {
        Ok(r) => r,
        Err(e) => return error(StatusCode::BAD_GATEWAY, &format!("serveur Relay injoignable : {e}")),
    };
    if resp.status() == StatusCode::SWITCHING_PROTOCOLS {
        let Some(client_upgrade) = client_upgrade else {
            return error(StatusCode::BAD_GATEWAY, "changement de protocole inattendu");
        };
        let server_upgrade = hyper::upgrade::on(&mut resp);
        tokio::spawn(async move {
            if let (Ok(a), Ok(b)) = tokio::join!(client_upgrade, server_upgrade) {
                let _ = tokio::io::copy_bidirectional(&mut TokioIo::new(a), &mut TokioIo::new(b)).await;
            }
        });
        let mut r = Response::new(Empty::new().map_err(|n| match n {}).boxed());
        *r.status_mut() = StatusCode::SWITCHING_PROTOCOLS;
        *r.headers_mut() = resp.headers().clone();
        r.headers_mut().remove(header::SET_COOKIE);
        return r;
    }
    let (parts, body) = resp.into_parts();
    let mut r = Response::new(body.boxed());
    *r.status_mut() = parts.status;
    for (name, value) in &parts.headers {
        if !HOP_BY_HOP.contains(&name.as_str()) && name != header::SET_COOKIE {
            r.headers_mut().append(name, value.clone());
        }
    }
    r
}

fn web_asset(path: &str) -> Response<Body> {
    let rel = path.trim_start_matches('/');
    let (file, name) = match WebUi::get(rel) {
        Some(f) if !rel.is_empty() => (f, rel),
        // SPA fallback: unknown routes render the application shell.
        _ if !rel.starts_with("assets/") => match WebUi::get("index.html") {
            Some(f) => (f, "index.html"),
            None => return text(StatusCode::SERVICE_UNAVAILABLE, "interface absente de cette version"),
        },
        _ => return text(StatusCode::NOT_FOUND, "fichier introuvable"),
    };
    let cache = if name.starts_with("assets/") {
        "public, max-age=31536000, immutable"
    } else {
        "no-store"
    };
    file_response(name, file.data.into_owned(), cache)
}

fn local_asset(name: &str) -> Response<Body> {
    match LocalUi::get(name) {
        Some(f) => file_response(name, f.data.into_owned(), "no-store"),
        None => text(StatusCode::NOT_FOUND, "fichier introuvable"),
    }
}

fn file_response(name: &str, data: Vec<u8>, cache: &'static str) -> Response<Body> {
    let mime = mime_guess::from_path(name).first_or_octet_stream();
    let mut r = Response::new(Full::new(Bytes::from(data)).map_err(|n| match n {}).boxed());
    let h = r.headers_mut();
    h.insert(header::CONTENT_TYPE, HeaderValue::from_str(mime.as_ref()).unwrap());
    h.insert(header::CACHE_CONTROL, HeaderValue::from_static(cache));
    secure(h);
    r
}

fn secure(h: &mut HeaderMap) {
    h.insert(header::X_CONTENT_TYPE_OPTIONS, HeaderValue::from_static("nosniff"));
    h.insert(header::X_FRAME_OPTIONS, HeaderValue::from_static("DENY"));
    h.insert(header::REFERRER_POLICY, HeaderValue::from_static("no-referrer"));
    h.insert(header::CONTENT_SECURITY_POLICY, HeaderValue::from_static(CSP));
}

fn text(status: StatusCode, msg: &str) -> Response<Body> {
    let mut r = Response::new(Full::new(Bytes::from(msg.to_owned())).map_err(|n| match n {}).boxed());
    *r.status_mut() = status;
    r.headers_mut()
        .insert(header::CONTENT_TYPE, HeaderValue::from_static("text/plain; charset=utf-8"));
    r.headers_mut().insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    secure(r.headers_mut());
    r
}

fn json(status: StatusCode, v: &serde_json::Value) -> Response<Body> {
    let mut r = text(status, &v.to_string());
    r.headers_mut()
        .insert(header::CONTENT_TYPE, HeaderValue::from_static("application/json"));
    r
}

fn error(status: StatusCode, msg: &str) -> Response<Body> {
    json(status, &serde_json::json!({ "error": msg }))
}

fn redirect(to: &'static str) -> Response<Body> {
    let mut r = text(StatusCode::FOUND, "");
    r.headers_mut().insert(header::LOCATION, HeaderValue::from_static(to));
    r
}

fn random_hex(n: usize) -> String {
    use rand::RngCore;
    let mut b = vec![0u8; n];
    rand::rng().fill_bytes(&mut b);
    b.iter().map(|x| format!("{x:02x}")).collect()
}
