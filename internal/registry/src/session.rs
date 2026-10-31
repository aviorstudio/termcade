use crate::{DEFAULT_URL, Session, invalid, is_cli_token};
use sha2::{Digest, Sha256};
use std::{
    fs,
    io::Write,
    path::{Path, PathBuf},
};
use url::Url;

pub fn config_dir() -> anyhow::Result<PathBuf> {
    #[cfg(target_os = "windows")]
    let path = std::env::var_os("AppData").map(PathBuf::from);
    #[cfg(target_os = "macos")]
    let path =
        std::env::var_os("HOME").map(|p| PathBuf::from(p).join("Library/Application Support"));
    #[cfg(not(any(target_os = "windows", target_os = "macos")))]
    let path = std::env::var_os("XDG_CONFIG_HOME")
        .filter(|p| !p.is_empty())
        .map(PathBuf::from)
        .or_else(|| std::env::var_os("HOME").map(|p| PathBuf::from(p).join(".config")));
    Ok(path
        .ok_or_else(|| anyhow::anyhow!("cannot determine user configuration directory"))?
        .join("termcade"))
}
pub fn session_path() -> anyhow::Result<PathBuf> {
    Ok(config_dir()?.join("session.json"))
}
pub fn registry_url(session: Option<&Session>) -> String {
    if let Ok(value) = std::env::var("TERMCADE_REGISTRY")
        && !value.trim().is_empty()
    {
        return value.trim().trim_end_matches('/').into();
    }
    session
        .filter(|s| !s.registry.is_empty())
        .map(|s| s.registry.clone())
        .unwrap_or_else(|| DEFAULT_URL.into())
}
pub fn load_session() -> anyhow::Result<Option<Session>> {
    load_at(&session_path()?)
}
pub fn save_session(session: &Session) -> anyhow::Result<()> {
    save_at(&session_path()?, session)
}
pub fn clear_session() -> anyhow::Result<()> {
    clear_at(&session_path()?)
}
fn load_at(path: &Path) -> anyhow::Result<Option<Session>> {
    let raw = match fs::read(path) {
        Ok(raw) => raw,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(e) => return Err(e.into()),
    };
    anyhow::ensure!(raw.len() <= 1 << 20, "saved login exceeds the byte limit");
    let session: Session = serde_json::from_slice(&raw)?;
    if session.token.is_empty() {
        return Ok(None);
    }
    anyhow::ensure!(
        is_cli_token(&session.token),
        "the saved login uses an unsupported credential — run `termcade login`"
    );
    Ok(Some(session))
}
fn save_at(path: &Path, session: &Session) -> anyhow::Result<()> {
    anyhow::ensure!(
        is_cli_token(&session.token),
        "refusing to save a non-CLI credential"
    );
    let parent = path
        .parent()
        .ok_or_else(|| anyhow::anyhow!("session path has no parent"))?;
    fs::create_dir_all(parent)?;
    let mut temp = tempfile::NamedTempFile::new_in(parent)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        temp.as_file()
            .set_permissions(fs::Permissions::from_mode(0o600))?;
    }
    let mut persisted = session.clone();
    persisted.notice.clear();
    temp.write_all(&serde_json::to_vec_pretty(&persisted)?)?;
    temp.as_file().sync_all()?;
    temp.persist(path)?;
    Ok(())
}
fn clear_at(path: &Path) -> anyhow::Result<()> {
    match fs::remove_file(path) {
        Ok(()) => Ok(()),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(e) => Err(e.into()),
    }
}
pub fn validate_registry(raw: &str) -> crate::Result<()> {
    let url = Url::parse(raw).map_err(|_| {
        invalid("registry must be an HTTP(S) URL without credentials, query or fragment")
    })?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err(invalid(
            "registry must be an HTTP(S) URL without credentials, query or fragment",
        ));
    }
    Ok(())
}
fn loopback_origin(raw: &str) -> anyhow::Result<Url> {
    let url = Url::parse(raw)?;
    anyhow::ensure!(
        url.scheme() == "http"
            && url.username().is_empty()
            && url.password().is_none()
            && matches!(url.path(), "" | "/")
            && url.query().is_none()
            && url.fragment().is_none(),
        "local pairing requires an HTTP loopback origin, without credentials or a path"
    );
    let host = url.host_str().unwrap_or_default().trim_matches(['[', ']']);
    let ip: std::net::IpAddr = host
        .parse()
        .map_err(|_| anyhow::anyhow!("local pairing requires a literal loopback host and port"))?;
    anyhow::ensure!(
        ip.is_loopback() && url.port().is_some_and(|p| p > 0),
        "local pairing requires a literal loopback host and port"
    );
    Ok(url)
}
#[derive(Clone)]
pub struct TuiStore {
    pub registry: String,
    pub pairing_url: String,
    pub path: PathBuf,
}
impl TuiStore {
    pub fn new() -> anyhow::Result<Self> {
        let mut registry = registry_url(None);
        validate_registry(&registry)?;
        let mut path = session_path()?;
        if let Ok(local) = std::env::var("TERMCADE_DEV_APP_URL")
            && !local.is_empty()
        {
            let local = local.trim_end_matches('/');
            let a = loopback_origin(&registry)?;
            let b = loopback_origin(local)?;
            anyhow::ensure!(
                a.host() == b.host(),
                "local registry and app must use the same literal loopback host"
            );
            let sum = format!("{:x}", Sha256::digest(registry.as_bytes()));
            path = path
                .parent()
                .expect("session directory")
                .join("local")
                .join(&sum[..24])
                .join("session.json");
            return Ok(Self {
                registry,
                pairing_url: format!("{local}/pair"),
                path,
            });
        }
        if std::env::var("TERMCADE_REGISTRY")
            .unwrap_or_default()
            .is_empty()
            && let Ok(Some(session)) = load_at(&path)
        {
            registry = registry_url(Some(&session));
        }
        validate_registry(&registry)?;
        Ok(Self {
            registry,
            pairing_url: crate::PAIRING_URI.into(),
            path,
        })
    }
    pub fn load(&self) -> anyhow::Result<Option<Session>> {
        let session = load_at(&self.path)?;
        if let Some(ref s) = session {
            anyhow::ensure!(
                s.registry.trim_end_matches('/') == self.registry.trim_end_matches('/'),
                "saved credentials belong to another registry; sign in here separately"
            );
        }
        Ok(session)
    }
    pub fn save(&self, session: &Session) -> anyhow::Result<()> {
        anyhow::ensure!(
            session.registry == self.registry,
            "refusing credentials for another registry"
        );
        save_at(&self.path, session)
    }
    pub fn clear(&self) -> anyhow::Result<()> {
        clear_at(&self.path)
    }
    pub fn can_pair(&self) -> anyhow::Result<()> {
        anyhow::ensure!(
            !(loopback_origin(&self.registry).is_ok() && self.pairing_url == crate::PAIRING_URI),
            "set TERMCADE_DEV_APP_URL to the local app origin before pairing with a local registry"
        );
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn reads_legacy_session_and_preserves_private_atomic_credentials() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("session.json");
        assert!(load_at(&path).unwrap().is_none());
        let raw = format!(
            r#"{{"registry":"https://api.termca.de","email":"player@example.org","token":"tcc_{}","username":"player","credential_id":"cli_123","notice":"old notice"}}"#,
            "a".repeat(43)
        );
        fs::write(&path, raw).unwrap();
        let session = load_at(&path).unwrap().unwrap();
        save_at(&path, &session).unwrap();
        let saved = load_at(&path).unwrap().unwrap();
        assert_eq!(saved.token, session.token);
        assert_eq!(saved.username, "player");
        assert!(saved.notice.is_empty());
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                0o600
            );
        }
        let mut wrong = session;
        wrong.token = "ak_abcdefghijklmnopqrst".into();
        assert!(save_at(&path, &wrong).is_err());
        assert_eq!(load_at(&path).unwrap().unwrap().token, saved.token);
        let other = TuiStore {
            registry: "http://127.0.0.1:8080".into(),
            pairing_url: crate::PAIRING_URI.into(),
            path: path.clone(),
        };
        assert!(other.load().is_err());
        assert!(other.save(&saved).is_err());
        assert!(other.can_pair().is_err());
        clear_at(&path).unwrap();
        clear_at(&path).unwrap();
    }
    #[test]
    fn local_pairing_requires_literal_loopback_and_an_explicit_port() {
        for url in ["http://127.0.0.1:8080", "http://[::1]:8080"] {
            assert!(loopback_origin(url).is_ok());
        }
        for url in [
            "http://localhost:8080",
            "https://127.0.0.1:8080",
            "http://127.0.0.1",
            "http://127.0.0.1:8080/remote",
            "http://127.0.0.1:8080?next=evil",
            "http://192.168.1.1:8080",
        ] {
            assert!(loopback_origin(url).is_err(), "accepted {url}");
        }
    }
}
