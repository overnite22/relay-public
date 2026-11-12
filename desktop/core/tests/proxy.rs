use bytes::Bytes;
use http::{Request, Response, StatusCode};
use http_body_util::{BodyExt, Full};
use hyper::body::Incoming;
use hyper_util::rt::TokioIo;
use relay_desktop_core::{start, ConfigFile, MemoryStore, SecretStore};
use std::sync::Arc;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};

const TOK: &str = "rlt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";

/// Fake Relay server: /api/me, /api/echo (reflects headers), /api/ws (byte echo after 101).
async fn upstream() -> u16 {
    let l = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = l.local_addr().unwrap().port();
    tokio::spawn(async move {
        loop {
            let (s, _) = l.accept().await.unwrap();
            tokio::spawn(async move {
                let svc = hyper::service::service_fn(|mut req: Request<Incoming>| async move {
                    let auth = req.headers().get("authorization").map(|v| v.to_str().unwrap().to_owned());
                    let ok = auth.as_deref() == Some(&format!("Bearer {TOK}"));
                    let resp = match req.uri().path() {
                        _ if !ok => Response::builder().status(401).body(Full::new(Bytes::from("{}"))),
                        "/api/me" => Response::builder().body(Full::new(Bytes::from(r#"{"user":"alice@example.com"}"#))),
                        "/api/echo" => {
                            let h = |n: &str| req.headers().get(n).map(|v| v.to_str().unwrap().to_owned());
                            let v = serde_json::json!({ "cookie": h("cookie"), "origin": h("origin"),
                                "xrelay": h("x-relay"), "query": req.uri().query() });
                            Response::builder()
                                .header("set-cookie", "leak=1")
                                .body(Full::new(Bytes::from(v.to_string())))
                        }
                        "/api/ws" => {
                            let up = hyper::upgrade::on(&mut req);
                            tokio::spawn(async move {
                                let mut io = TokioIo::new(up.await.unwrap());
                                let mut buf = [0u8; 64];
                                let n = io.read(&mut buf).await.unwrap();
                                io.write_all(&buf[..n]).await.unwrap();
                            });
                            Response::builder()
                                .status(101)
                                .header("connection", "upgrade")
                                .header("upgrade", "websocket")
                                .body(Full::new(Bytes::new()))
                        }
                        _ => Response::builder().status(404).body(Full::new(Bytes::new())),
                    };
                    Ok::<_, std::convert::Infallible>(resp.unwrap())
                });
                let _ = hyper::server::conn::http1::Builder::new()
                    .serve_connection(TokioIo::new(s), svc)
                    .with_upgrades()
                    .await;
            });
        }
    });
    port
}

async fn send(port: u16, req: Request<Full<Bytes>>) -> (StatusCode, http::HeaderMap, String) {
    let s = TcpStream::connect(("127.0.0.1", port)).await.unwrap();
    let (mut tx, conn) = hyper::client::conn::http1::handshake(TokioIo::new(s)).await.unwrap();
    tokio::spawn(conn);
    let r = tx.send_request(req).await.unwrap();
    let (p, b) = r.into_parts();
    (
        p.status,
        p.headers,
        String::from_utf8(b.collect().await.unwrap().to_bytes().to_vec()).unwrap(),
    )
}

fn req(port: u16, method: &str, path: &str, cookie: Option<&str>, body: &str) -> Request<Full<Bytes>> {
    let mut b = Request::builder()
        .method(method)
        .uri(path)
        .header("host", format!("127.0.0.1:{port}"));
    if let Some(c) = cookie {
        b = b.header("cookie", c);
    }
    if method == "POST" {
        b = b
            .header("x-relay", "1")
            .header("content-type", "application/json")
            .header("origin", format!("http://127.0.0.1:{port}"));
    }
    b.body(Full::new(Bytes::from(body.to_owned()))).unwrap()
}

#[tokio::test]
async fn pairing_proxy_and_websocket() {
    let up = upstream().await;
    let dir = tempfile::tempdir().unwrap();
    let cfg = ConfigFile::new(dir.path().join("config.json"));
    let secrets = Arc::new(MemoryStore::default());
    let run = start(cfg.clone(), secrets.clone(), "Relay-Desktop-Test").await.unwrap();
    let port = run.port;

    // Without the boot cookie nothing is served, and a foreign Host is refused.
    assert_eq!(send(port, req(port, "GET", "/api/me", None, "")).await.0, StatusCode::FORBIDDEN);
    let mut evil = req(port, "GET", "/", None, "");
    evil.headers_mut().insert("host", "attacker.example:80".parse().unwrap());
    assert_eq!(send(port, evil).await.0, StatusCode::MISDIRECTED_REQUEST);
    assert_eq!(
        send(port, req(port, "GET", "/__desktop/boot?k=wrong", None, "")).await.0,
        StatusCode::FORBIDDEN
    );

    let path = run.boot_url.split_once(&format!("{port}")).unwrap().1.to_owned();
    let (st, h, _) = send(port, req(port, "GET", &path, None, "")).await;
    assert_eq!(st, StatusCode::FOUND);
    let set = h.get("set-cookie").unwrap().to_str().unwrap();
    assert!(set.contains("HttpOnly") && set.contains("SameSite=Strict"));
    let cookie = format!("other=x; {}", set.split(';').next().unwrap());
    let c = Some(cookie.as_str());

    // Unpaired: the UI redirects to the pairing page and /api is refused.
    assert_eq!(
        send(port, req(port, "GET", "/", c, "")).await.1.get("location").unwrap(),
        "/__desktop/pair.html"
    );
    assert_eq!(send(port, req(port, "GET", "/api/me", c, "")).await.0, StatusCode::UNAUTHORIZED);
    assert_eq!(send(port, req(port, "GET", "/__desktop/pair.html", c, "")).await.0, StatusCode::OK);

    // Pairing needs X-Relay, validates the token upstream and persists the server only.
    let mut no_header = req(port, "POST", "/__desktop/pair", c, "{}");
    no_header.headers_mut().remove("x-relay");
    assert_eq!(send(port, no_header).await.0, StatusCode::FORBIDDEN);
    let bad = format!(r#"{{"server":"http://127.0.0.1:{up}","token":"rlt_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"}}"#);
    assert_eq!(
        send(port, req(port, "POST", "/__desktop/pair", c, &bad)).await.0,
        StatusCode::BAD_GATEWAY
    );
    assert!(secrets.load().unwrap().is_none());
    let link = format!(r#"{{"server":"relay://pair?server=http%3A%2F%2F127.0.0.1%3A{up}&token={TOK}"}}"#);
    let (st, _, body) = send(port, req(port, "POST", "/__desktop/pair", c, &link)).await;
    assert_eq!(st, StatusCode::OK, "{body}");
    assert!(body.contains("alice@example.com"));
    assert_eq!(secrets.load().unwrap().as_deref(), Some(TOK));
    let saved = std::fs::read_to_string(dir.path().join("config.json")).unwrap();
    assert!(saved.contains(&format!("127.0.0.1:{up}")) && !saved.contains("rlt_"));

    // Proxied API: bearer injected, local cookie/origin stripped, upstream cookies dropped.
    let mut post = req(port, "POST", "/api/echo?x=1", c, "{}");
    post.headers_mut().insert("authorization", "Bearer forged".parse().unwrap());
    let (st, h, body) = send(port, post).await;
    assert_eq!(st, StatusCode::OK, "{body}");
    let v: serde_json::Value = serde_json::from_str(&body).unwrap();
    assert_eq!(
        v,
        serde_json::json!({ "cookie": null, "origin": null, "xrelay": "1", "query": "x=1" })
    );
    assert!(h.get("set-cookie").is_none());

    // WebSocket-style upgrade is spliced end to end.
    let mut s = TcpStream::connect(("127.0.0.1", port)).await.unwrap();
    let head =
        format!("GET /api/ws HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\nCookie: {cookie}\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n");
    s.write_all(head.as_bytes()).await.unwrap();
    let mut buf = vec![0u8; 1024];
    let mut got = String::new();
    while !got.contains("\r\n\r\n") {
        let n = s.read(&mut buf).await.unwrap();
        got.push_str(&String::from_utf8_lossy(&buf[..n]));
    }
    assert!(got.starts_with("HTTP/1.1 101"), "{got}");
    s.write_all(b"ping-guac").await.unwrap();
    let n = s.read(&mut buf).await.unwrap();
    assert_eq!(&buf[..n], b"ping-guac");

    // Unpairing clears the token; the next launch reuses the same port.
    assert_eq!(send(port, req(port, "POST", "/__desktop/unpair", c, "")).await.0, StatusCode::OK);
    assert!(secrets.load().unwrap().is_none());
    run.task.abort();
    let _ = run.task.await;
    let again = start(cfg, secrets, "t").await.unwrap();
    assert_eq!(again.port, port);
}
