//! Marketplace transport and credential boundaries, shared by the CLI and TUI.
mod device;
mod session;
pub use device::*;
use reqwest::{Method, StatusCode};
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use serde_json::{Value, json};
pub use session::*;
use sha2::{Digest, Sha256};
use std::{collections::HashSet, io::Write, time::Duration};
use tempfile::NamedTempFile;
use url::Url;

pub const DEFAULT_URL: &str = "https://api.termca.de";
pub const MAX_JSON: usize = 1 << 20;
pub const MAX_PACKAGE: usize = 64 << 20;
#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("login required")]
    LoginRequired,
    #[error("{message}")]
    Response { status: u16, message: String },
    #[error("{0}")]
    Unreachable(String),
    #[error("{0}")]
    Invalid(String),
}
impl Error {
    pub fn not_found(&self) -> bool {
        matches!(self, Self::Response { status: 404, .. })
    }
    pub fn transient(&self) -> bool {
        matches!(self, Self::Unreachable(_))
            || matches!(self, Self::Response { status, .. } if *status >= 500 || *status == 408 || *status == 429)
    }
}
pub type Result<T> = std::result::Result<T, Error>;
pub(crate) fn invalid(message: impl Into<String>) -> Error {
    Error::Invalid(message.into())
}
fn unreachable(error: reqwest::Error) -> Error {
    let mut message =
        "the marketplace is not answering — check your connection and try again".to_string();
    // Strip URL/request headers even from opt-in diagnostics: bearer and device
    // credentials must never be exposed by a transport error.
    if std::env::var_os("TERMCADE_DEBUG").is_some() {
        message.push_str(&format!(": {}", error.without_url()));
    }
    Error::Unreachable(message)
}
pub fn safe_text(value: &str, max: usize) -> String {
    let mut result = String::new();
    for c in value.chars().filter(|c| !c.is_control()) {
        result.push(c);
        if result.len() >= max {
            break;
        }
    }
    result.trim().to_string()
}
fn null_default<'de, D: serde::Deserializer<'de>, T: Deserialize<'de> + Default>(
    decoder: D,
) -> std::result::Result<T, D::Error> {
    Ok(Option::<T>::deserialize(decoder)?.unwrap_or_default())
}
macro_rules! model {
    ($name:ident { $($field:ident : $kind:ty),* $(,)? }) => {
        #[derive(Clone, Debug, Default, Deserialize, Serialize)]
        #[serde(default)]
        pub struct $name { $(#[serde(deserialize_with="null_default")] pub $field: $kind),* }
    }
}
model!(Game {
    id: String,
    name: String,
    description: String,
    repo: String,
    version: String,
    abi: i32,
    width: i32,
    height: i32,
    has_package: bool,
    sha256: String,
    created_at: String,
    released_at: String,
    likes: i64,
    liked: bool
});
model!(Resolved {
    id: String,
    name: String,
    version: String,
    repo: String,
    tag: String,
    asset: String,
    url: String,
    sha256: String,
    size: i64,
    abi: i32,
    width: i32,
    height: i32
});
model!(Published {
    id: String,
    version: String,
    repo: String,
    tag: String,
    asset: String,
    sha256: String,
    size: i64
});
model!(Session {
    registry: String,
    email: String,
    token: String,
    username: String,
    notice: String,
    credential_id: String,
    expires_at: String
});
model!(Org { username:String, link:String, bio:String, admin:bool, games:Vec<String> });
model!(Me { email:String, username:String, orgs:Vec<Org>, handles:Vec<String> });
model!(Member {
    email: String,
    admin: bool
});
model!(HandleOwner { name:String, is_org:bool, bio:String, link:String, games:Vec<Game> });
model!(Activity {
    id: String,
    personal_best: i64,
    plays: i64,
    last_played: String,
    last_version: String,
    last_completed: String
});
model!(Run {
    run: String,
    score: i64,
    played_at: String,
    completed: bool,
    version: String
});
model!(PublishKey {
    id: String,
    name: String,
    username: String,
    last_used_at: String,
    created_at: String,
    token: String
});
model!(LikeState {
    likes: i64,
    liked: bool
});
model!(PlayDay {
    day: String,
    count: i64
});
model!(CliCredential {
    id: String,
    client_id: String,
    device_name: String,
    expires_at: String,
    revoked: bool,
    last_used_at: String,
    created_at: String
});
model!(CatalogPage { games:Vec<Game>, next:String });

#[derive(Clone)]
pub struct Client {
    pub base: String,
    token: String,
    http: reqwest::Client,
}
impl Client {
    pub fn new(base: &str, token: &str) -> Result<Self> {
        let _ = rustls::crypto::ring::default_provider().install_default();
        let base = base.trim_end_matches('/').to_string();
        validate_registry(&base)?;
        let origin = Url::parse(&base)
            .map_err(|_| invalid("invalid registry URL"))?
            .origin();
        let http = reqwest::Client::builder()
            .timeout(Duration::from_secs(60))
            .redirect(reqwest::redirect::Policy::custom(move |attempt| {
                if attempt.url().origin() != origin {
                    attempt.error("refusing a registry redirect to another origin")
                } else if attempt.previous().len() >= 10 {
                    attempt.error("too many registry redirects")
                } else {
                    attempt.follow()
                }
            }))
            .build()
            .map_err(unreachable)?;
        Ok(Self {
            base,
            token: token.to_string(),
            http,
        })
    }
    pub fn anonymous(&self) -> Result<Self> {
        Self::new(&self.base, "")
    }
    pub fn endpoint(&self, parts: &[&str]) -> String {
        let mut url = Url::parse(&format!("{}/", self.base)).expect("validated registry");
        {
            let mut path = url.path_segments_mut().expect("HTTP registry");
            path.pop_if_empty();
            for part in parts {
                path.push(part);
            }
        }
        url.to_string()
    }
    fn request(&self, method: Method, url: &str, body: Option<&Value>) -> reqwest::RequestBuilder {
        let mut request = self.http.request(method, url);
        if !self.token.is_empty() {
            if self.token.starts_with("tcc_") || self.token.starts_with("ak_") {
                request = request.bearer_auth(&self.token);
            } else {
                request = request.header("Authorization", &self.token);
            }
        }
        if let Some(body) = body {
            request = request.json(body);
        }
        request
    }
    async fn response(
        &self,
        method: Method,
        url: &str,
        body: Option<&Value>,
    ) -> Result<reqwest::Response> {
        let response = self
            .request(method, url, body)
            .send()
            .await
            .map_err(unreachable)?;
        let status = response.status();
        if status == StatusCode::UNAUTHORIZED {
            return Err(Error::LoginRequired);
        }
        if !status.is_success() {
            let raw = bounded(response, 8192).await.unwrap_or_default();
            let prose = serde_json::from_slice::<Value>(&raw)
                .ok()
                .and_then(|v| {
                    v.get("message")
                        .and_then(Value::as_str)
                        .map(|s| safe_text(s, 300))
                })
                .filter(|s| !s.is_empty());
            let message = prose.unwrap_or_else(|| {
                if status.is_server_error() || matches!(status.as_u16(), 408 | 429) {
                    format!(
                        "the marketplace is having trouble (HTTP {status}) — try again shortly",
                        status = status.as_u16()
                    )
                } else {
                    format!(
                        "the marketplace refused that request (HTTP {})",
                        status.as_u16()
                    )
                }
            });
            return Err(Error::Response {
                status: status.as_u16(),
                message,
            });
        }
        Ok(response)
    }
    pub async fn json<T: DeserializeOwned>(
        &self,
        method: Method,
        path: &str,
        body: Option<Value>,
    ) -> Result<T> {
        self.json_url(method, &format!("{}{path}", self.base), body)
            .await
    }
    pub async fn json_url<T: DeserializeOwned>(
        &self,
        method: Method,
        url: &str,
        body: Option<Value>,
    ) -> Result<T> {
        let response = self.response(method, url, body.as_ref()).await?;
        let raw = bounded(response, MAX_JSON).await?;
        serde_json::from_slice(&raw).map_err(|_| invalid("the marketplace returned malformed data"))
    }
    pub async fn empty(&self, method: Method, parts: &[&str], body: Option<Value>) -> Result<()> {
        self.response(method, &self.endpoint(parts), body.as_ref())
            .await?;
        Ok(())
    }
    pub async fn get<T: DeserializeOwned>(&self, parts: &[&str]) -> Result<T> {
        self.json_url(Method::GET, &self.endpoint(parts), None)
            .await
    }
    pub async fn catalog_page(
        &self,
        cursor: &str,
        search: &str,
        abi: i32,
        sort: &str,
    ) -> Result<CatalogPage> {
        let mut url = Url::parse(&self.endpoint(&["v1", "games"])).expect("registry URL");
        {
            let mut q = url.query_pairs_mut();
            if !cursor.is_empty() {
                q.append_pair("cursor", cursor);
            }
            if !search.is_empty() {
                q.append_pair("q", search);
            }
            if abi > 0 {
                q.append_pair("abi", &abi.to_string());
            }
            if !sort.is_empty() {
                q.append_pair("sort", sort);
            }
        }
        self.json_url(Method::GET, url.as_str(), None).await
    }
    pub async fn search_catalog(&self, search: &str) -> Result<Vec<Game>> {
        let mut games = Vec::new();
        let mut cursor = String::new();
        let mut cursors = HashSet::new();
        let mut ids = HashSet::new();
        loop {
            let page = self.catalog_page(&cursor, search.trim(), 0, "").await?;
            for game in page.games {
                if ids.insert(game.id.clone()) {
                    games.push(game);
                }
            }
            if page.next.is_empty() {
                return Ok(games);
            }
            if !cursors.insert(page.next.clone()) {
                return Err(invalid("marketplace returned a repeated page cursor"));
            }
            cursor = page.next;
        }
    }
    pub async fn game(&self, author: &str, slug: &str) -> Result<Game> {
        let game: Game = self.get(&["v1", "games", author, slug]).await?;
        if game.id != format!("{author}/{slug}") || game.name.is_empty() {
            return Err(invalid(
                "the marketplace returned a mismatched game identity",
            ));
        }
        Ok(game)
    }
    pub async fn owner(&self, name: &str) -> Result<HandleOwner> {
        let owner: HandleOwner = self.get(&["v1", "usernames", name]).await?;
        if owner.name != name {
            return Err(invalid(
                "the marketplace returned a mismatched owner identity",
            ));
        }
        Ok(owner)
    }
    pub async fn resolve(&self, author: &str, slug: &str) -> Result<Resolved> {
        let url = format!(
            "{}?abi=1",
            self.endpoint(&["v1", "games", author, slug, "resolve"])
        );
        self.json_url(Method::GET, &url, None).await
    }
    pub async fn download(&self, author: &str, slug: &str) -> Result<NamedTempFile> {
        let resolved = self.resolve(author, slug).await?;
        if resolved.id != format!("{author}/{slug}")
            || resolved.abi != 1
            || resolved.size < 1
            || resolved.size > MAX_PACKAGE as i64
        {
            return Err(invalid(
                "the marketplace returned mismatched package metadata",
            ));
        }
        if resolved.sha256.len() != 64 || !resolved.sha256.bytes().all(|b| b.is_ascii_hexdigit()) {
            return Err(invalid(format!(
                "registry published no checksum for {} {}",
                resolved.id, resolved.version
            )));
        }
        let url = format!(
            "{}?abi=1",
            self.endpoint(&["v1", "games", author, slug, "download"])
        );
        let mut response = self.response(Method::GET, &url, None).await?;
        if response
            .content_length()
            .is_some_and(|n| n > MAX_PACKAGE as u64)
        {
            return Err(invalid("package exceeds 64 MiB"));
        }
        let mut output = NamedTempFile::new().map_err(|e| invalid(e.to_string()))?;
        let mut digest = Sha256::new();
        let mut size = 0;
        while let Some(chunk) = response.chunk().await.map_err(unreachable)? {
            size += chunk.len();
            if size > MAX_PACKAGE {
                return Err(invalid("package exceeds 64 MiB"));
            }
            digest.update(&chunk);
            output
                .write_all(&chunk)
                .map_err(|e| invalid(e.to_string()))?;
        }
        if size as i64 != resolved.size {
            return Err(invalid(
                "downloaded package length does not match the registry",
            ));
        }
        let got = format!("{:x}", digest.finalize());
        if got != resolved.sha256.to_ascii_lowercase() {
            return Err(invalid(format!(
                "checksum mismatch for {} {}: the registry recorded {}, the download is {got}",
                resolved.id, resolved.version, resolved.sha256
            )));
        }
        let raw = std::fs::read(output.path()).map_err(|e| invalid(e.to_string()))?;
        let package = termcade_manifest::Package::read(&raw).map_err(|e| invalid(e.to_string()))?;
        let manifest = &package.manifest;
        if manifest.game.id != resolved.id
            || manifest.game.version != resolved.version
            || manifest.requirements.abi != resolved.abi
            || manifest.requirements.width != resolved.width
            || manifest.requirements.height != resolved.height
        {
            return Err(invalid(
                "downloaded package identity does not match the registry",
            ));
        }
        Ok(output)
    }
    pub async fn me(&self) -> Result<Me> {
        self.get(&["v1", "me"]).await
    }
    pub async fn library(&self) -> Result<Vec<Game>> {
        self.get(&["v1", "library"]).await
    }
    pub async fn activity(&self) -> Result<Vec<Activity>> {
        self.get(&["v1", "activity"]).await
    }
    pub async fn library_set(&self, author: &str, slug: &str, added: bool) -> Result<()> {
        self.empty(
            if added { Method::PUT } else { Method::DELETE },
            &["v1", "library", author, slug],
            None,
        )
        .await
    }
    pub async fn publish(&self, repo: &str, tag: &str, asset: &str) -> Result<Published> {
        self.json(
            Method::POST,
            "/v1/publish",
            Some(json!({"repo":repo,"tag":tag,"asset":asset})),
        )
        .await
    }
    pub async fn keys(&self) -> Result<Vec<PublishKey>> {
        self.get(&["v1", "keys"]).await
    }
    pub async fn create_key(&self, name: &str, username: &str) -> Result<PublishKey> {
        self.json(
            Method::POST,
            "/v1/keys",
            Some(json!({"name":name,"username":username})),
        )
        .await
    }
    pub async fn org(&self, name: &str) -> Result<Org> {
        self.get(&["v1", "orgs", name]).await
    }
    pub async fn create_org(&self, username: &str, bio: &str, link: &str) -> Result<Org> {
        self.json(
            Method::POST,
            "/v1/orgs",
            Some(json!({"username":username,"bio":bio,"link":link})),
        )
        .await
    }
    pub async fn members(&self, name: &str) -> Result<Vec<Member>> {
        self.get(&["v1", "orgs", name, "members"]).await
    }
    pub async fn update_org(
        &self,
        name: &str,
        bio: Option<&str>,
        link: Option<&str>,
    ) -> Result<Org> {
        let mut body = serde_json::Map::new();
        if let Some(bio) = bio {
            body.insert("bio".into(), json!(bio));
        }
        if let Some(link) = link {
            body.insert("link".into(), json!(link));
        }
        self.json_url(
            Method::PATCH,
            &self.endpoint(&["v1", "orgs", name]),
            Some(Value::Object(body)),
        )
        .await
    }
    pub async fn play_days(&self, author: &str, slug: &str, days: i32) -> Result<Vec<PlayDay>> {
        if !(1..=366).contains(&days) {
            return Err(invalid("play-day window must be 1–366"));
        }
        let parts = if slug.is_empty() {
            vec!["v1", "usernames", author, "plays"]
        } else {
            vec!["v1", "games", author, slug, "plays"]
        };
        #[derive(Deserialize)]
        struct Days {
            days: Vec<PlayDay>,
        }
        Ok(self
            .json_url::<Days>(
                Method::GET,
                &format!("{}?days={days}", self.endpoint(&parts)),
                None,
            )
            .await?
            .days)
    }
}
async fn bounded(mut response: reqwest::Response, limit: usize) -> Result<Vec<u8>> {
    if response.content_length().is_some_and(|n| n > limit as u64) {
        return Err(invalid("the marketplace returned malformed data"));
    }
    let mut raw = Vec::new();
    while let Some(chunk) = response.chunk().await.map_err(unreachable)? {
        if raw.len() + chunk.len() > limit {
            return Err(invalid("the marketplace returned malformed data"));
        }
        raw.extend_from_slice(&chunk);
    }
    Ok(raw)
}

#[cfg(test)]
mod tests;
