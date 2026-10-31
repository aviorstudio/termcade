use super::*;
use std::{
    io::{Read, Write},
    net::TcpListener,
    sync::{Arc, Mutex},
    thread,
};

type Reply = (u16, Vec<(&'static str, String)>, Vec<u8>);

pub(crate) struct Mock {
    pub base: String,
    pub requests: Arc<Mutex<Vec<String>>>,
    worker: Option<thread::JoinHandle<()>>,
}
impl Mock {
    pub fn new(replies: Vec<Reply>) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        listener.set_nonblocking(true).unwrap();
        let requests = Arc::new(Mutex::new(Vec::new()));
        let seen = requests.clone();
        let worker = thread::spawn(move || {
            for (status, headers, body) in replies {
                let deadline = std::time::Instant::now() + Duration::from_secs(5);
                let mut socket = loop {
                    match listener.accept() {
                        Ok((socket, _)) => break socket,
                        Err(e)
                            if e.kind() == std::io::ErrorKind::WouldBlock
                                && std::time::Instant::now() < deadline =>
                        {
                            thread::sleep(Duration::from_millis(5))
                        }
                        Err(_) => return,
                    }
                };
                socket
                    .set_read_timeout(Some(Duration::from_secs(2)))
                    .unwrap();
                let mut raw = Vec::new();
                loop {
                    let mut chunk = [0; 8192];
                    let n = socket.read(&mut chunk).unwrap();
                    if n == 0 {
                        break;
                    }
                    raw.extend_from_slice(&chunk[..n]);
                    if let Some(at) = raw.windows(4).position(|s| s == b"\r\n\r\n") {
                        let text = String::from_utf8_lossy(&raw[..at]);
                        let length = text
                            .lines()
                            .find_map(|line| {
                                line.to_ascii_lowercase()
                                    .strip_prefix("content-length:")
                                    .and_then(|v| v.trim().parse::<usize>().ok())
                            })
                            .unwrap_or(0);
                        if raw.len() >= at + 4 + length {
                            break;
                        }
                    }
                }
                seen.lock().unwrap().push(String::from_utf8(raw).unwrap());
                let mut response = format!(
                    "HTTP/1.1 {status} response\r\nContent-Length: {}\r\nConnection: close\r\n",
                    body.len()
                );
                for (key, value) in headers {
                    response.push_str(&format!("{key}: {value}\r\n"));
                }
                response.push_str("\r\n");
                socket.write_all(response.as_bytes()).unwrap();
                let _ = socket.write_all(&body);
            }
        });
        Self {
            base,
            requests,
            worker: Some(worker),
        }
    }
    pub fn json(values: Vec<Value>) -> Self {
        Self::new(
            values
                .into_iter()
                .map(|v| (200, vec![], serde_json::to_vec(&v).unwrap()))
                .collect(),
        )
    }
}
impl Drop for Mock {
    fn drop(&mut self) {
        if let Some(worker) = self.worker.take() {
            worker.join().unwrap();
        }
    }
}

#[tokio::test]
async fn follows_short_catalog_pages_deduplicates_and_detects_cursor_loops() {
    let mock = Mock::json(vec![
        json!({"games":[],"next":"a b"}),
        json!({"games":[{"id":"a/b","name":"B"}],"next":"next"}),
        json!({"games":[{"id":"a/b","name":"duplicate"},{"id":"a/c"}]}),
    ]);
    let games = Client::new(&mock.base, "")
        .unwrap()
        .search_catalog(" fire & ice ")
        .await
        .unwrap();
    assert_eq!(
        games.iter().map(|g| g.id.as_str()).collect::<Vec<_>>(),
        ["a/b", "a/c"]
    );
    {
        let requests = mock.requests.lock().unwrap();
        assert!(requests[0].contains("q=fire+%26+ice"));
        assert!(requests[1].contains("cursor=a+b"));
    }
    let looped = Mock::json(vec![json!({"next":"same"}), json!({"next":"same"})]);
    assert!(
        Client::new(&looped.base, "")
            .unwrap()
            .search_catalog("")
            .await
            .unwrap_err()
            .to_string()
            .contains("repeated")
    );
}
#[tokio::test]
async fn credentials_stay_on_origin_and_paths_escape_single_segments() {
    let mock = Mock::new(vec![(
        302,
        vec![("Location", "http://127.0.0.1:9/stolen".into())],
        vec![],
    )]);
    let client = Client::new(&mock.base, &format!("tcc_{}", "a".repeat(43))).unwrap();
    assert!(matches!(client.me().await, Err(Error::Unreachable(_))));
    let requests = mock.requests.lock().unwrap();
    assert!(
        requests[0]
            .to_ascii_lowercase()
            .contains("authorization: bearer tcc_")
    );
    assert!(
        client
            .endpoint(&["v1", "orgs", "a/b", "members", "x+y@example.org"])
            .contains("a%2Fb")
    );
}
#[tokio::test]
async fn errors_and_metadata_are_bounded_and_terminal_safe() {
    let mock = Mock::new(vec![
        (401, vec![], vec![]),
        (404, vec![], br#"{"message":"gone\u001b[31m\n"}"#.to_vec()),
        (200, vec![], b"{} trailing".to_vec()),
        (200, vec![], vec![b'a'; MAX_JSON + 1]),
    ]);
    let client = Client::new(&mock.base, "").unwrap();
    assert!(matches!(client.me().await, Err(Error::LoginRequired)));
    let error = client.me().await.unwrap_err();
    assert!(error.not_found());
    assert!(!error.to_string().contains('\x1b'));
    assert!(client.me().await.is_err());
    assert!(client.me().await.is_err());
}
#[tokio::test]
async fn package_digest_length_identity_and_requirements_are_verified() {
    let raw = include_bytes!("../../starter/packs/asteroid.tcade").to_vec();
    let package = termcade_manifest::Package::read(&raw).unwrap();
    let m = package.manifest;
    let resolution = json!({"id":m.game.id,"version":m.game.version,"size":raw.len(),"sha256":format!("{:x}",Sha256::digest(&raw)),"abi":m.requirements.abi,"width":m.requirements.width,"height":m.requirements.height});
    let mock = Mock::new(vec![
        (200, vec![], serde_json::to_vec(&resolution).unwrap()),
        (200, vec![], raw.clone()),
    ]);
    assert_eq!(
        std::fs::read(
            Client::new(&mock.base, "")
                .unwrap()
                .download(m.author(), m.slug())
                .await
                .unwrap()
                .path()
        )
        .unwrap(),
        raw
    );
    for field in ["sha256", "size", "version", "width"] {
        let mut bad = resolution.clone();
        bad[field] = match field {
            "sha256" => json!("0".repeat(64)),
            "size" => json!(raw.len() + 1),
            "version" => json!("wrong"),
            _ => json!(1),
        };
        let mock = Mock::new(vec![
            (200, vec![], serde_json::to_vec(&bad).unwrap()),
            (200, vec![], raw.clone()),
        ]);
        assert!(
            Client::new(&mock.base, "")
                .unwrap()
                .download(m.author(), m.slug())
                .await
                .is_err(),
            "accepted invalid {field}"
        );
    }
}
#[tokio::test]
async fn account_studio_and_mutation_routes_keep_payloads_distinct() {
    let mock = Mock::json(vec![
        json!({"username":"studio"}),
        json!({"username":"studio"}),
        json!({}),
        json!({"id":"g/s","name":"Game"}),
        json!({"name":"other"}),
    ]);
    let client = Client::new(&mock.base, "").unwrap();
    client
        .create_org("studio", "bio", "https://example.org")
        .await
        .unwrap();
    client.update_org("studio", Some(""), None).await.unwrap();
    client
        .empty(
            Method::POST,
            &["v1", "orgs", "studio", "members"],
            Some(json!({"email":"m@example.org","admin":false})),
        )
        .await
        .unwrap();
    assert_eq!(client.game("g", "s").await.unwrap().name, "Game");
    assert!(client.owner("expected").await.is_err());
    let requests = mock.requests.lock().unwrap();
    let body = requests[1].split("\r\n\r\n").nth(1).unwrap();
    assert_eq!(
        serde_json::from_str::<Value>(body).unwrap(),
        json!({"bio":""})
    );
    assert!(requests[2].starts_with("POST /v1/orgs/studio/members "));
}
#[test]
fn credentials_and_trusted_pairing_addresses_are_closed() {
    assert!(is_cli_token(&format!("tcc_{}", "A_-".repeat(14) + "a")));
    for token in [
        "",
        "tcc_short",
        "ak_abcdefghijklmnopqrstuvwxyz",
        "Bearer tcc_any",
    ] {
        assert!(!is_cli_token(token));
    }
    assert!(is_publish_key("ak_abcdefghijklmnopqrst"));
    assert!(!is_publish_key("tcc_abcdefghijklmnopqrst"));
    assert_eq!(
        pairing_url_with_code(PAIRING_URI, "abcd-efgh"),
        format!("{PAIRING_URI}/ABCD-EFGH")
    );
    for base in [
        "https://example.org/pair?secret=1",
        "https://name@example.org/pair",
        "file:///pair",
        "https://app.termca.de/other",
    ] {
        assert_eq!(pairing_url_with_code(base, "ABCD-EFGH"), base);
    }
    for url in [
        "ftp://example.org",
        "https://user@example.org",
        "https://example.org?x=1",
        "https://example.org/#f",
    ] {
        assert!(Client::new(url, "").is_err());
    }
}
