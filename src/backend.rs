use anyhow::{Result, anyhow, bail};
use reqwest::Method;
use serde_json::json;
use termcade_plugin::Runtime;
use termcade_registry::{
    self as registry, Activity, CliCredential, Client, DeviceRound, Game, HandleOwner, Me, Member,
    PlayDay, Session, TuiStore,
};
#[derive(Default)]
pub struct Snapshot {
    pub catalog: Vec<Game>,
    pub library: Vec<Game>,
    pub installed: Vec<crate::local::Installed>,
    pub account: Option<Me>,
    pub credential_id: String,
    pub catalog_error: String,
    pub account_error: String,
    pub library_error: String,
    pub activity: Vec<Activity>,
}
#[derive(Clone)]
pub enum Action {
    Add(String),
    Remove(String),
    Install(String),
    Like(String, bool),
    Uninstall(String),
    Username(String),
    CheckUsername(String),
    OrgCreate(String, String, String),
    MemberSet(String, String, bool),
    MemberRemove(String, String),
    OrgDelete(String),
    Revoke(String),
    DeleteAccount,
    Logout,
}
#[derive(Clone)]
pub enum Request {
    Load,
    Search(String),
    Game(String),
    Owner(String),
    Days(String),
    Members(String),
    Sessions,
    PairStart,
    PairWait(DeviceRound),
    Mutate(Action),
}
#[derive(Default)]
pub struct Reply {
    pub snapshot: Option<Snapshot>,
    pub catalog: Option<Vec<Game>>,
    pub game: Option<Game>,
    pub owner: Option<HandleOwner>,
    pub days: Option<Vec<PlayDay>>,
    pub members: Option<Vec<Member>>,
    pub sessions: Option<Vec<CliCredential>>,
    pub round: Option<DeviceRound>,
    pub session: Option<Session>,
    pub notice: String,
    pub installed: Option<String>,
}
#[derive(Clone)]
pub struct Backend {
    pub store: TuiStore,
    pub runtime: Runtime,
    pub config_error: Option<String>,
}
impl Backend {
    pub fn new(runtime: Runtime) -> Result<Self> {
        match TuiStore::new() {
            Ok(store) => Ok(Self {
                store,
                runtime,
                config_error: None,
            }),
            Err(e) => Ok(Self {
                store: TuiStore {
                    registry: registry::registry_url(None),
                    pairing_url: registry::PAIRING_URI.into(),
                    path: registry::session_path().unwrap_or_default(),
                },
                runtime,
                config_error: Some(e.to_string()),
            }),
        }
    }
    pub fn client(&self, required: bool) -> Result<(Client, Option<Session>)> {
        if let Some(error) = &self.config_error {
            bail!("{error}");
        }
        let session = self.store.load()?;
        if required && session.is_none() {
            bail!("login required");
        }
        let client = Client::new(
            &self.store.registry,
            session
                .as_ref()
                .map(|s| s.token.as_str())
                .unwrap_or_default(),
        )?;
        Ok((client, session))
    }
    pub async fn snapshot(&self) -> Snapshot {
        let mut out = Snapshot {
            installed: crate::local::discover().unwrap_or_default(),
            ..Snapshot::default()
        };
        let (client, session) = match self.client(false) {
            Ok(pair) => pair,
            Err(e) => {
                out.account_error = e.to_string();
                match Client::new(&self.store.registry, "") {
                    Ok(client) => (client, None),
                    Err(_) => return out,
                }
            }
        };
        let (catalog, me) = tokio::join!(client.search_catalog(""), async {
            if session.is_some() {
                client.me().await.map(Some)
            } else {
                Ok(None)
            }
        });
        match catalog {
            Ok(games) => out.catalog = games,
            Err(e) => out.catalog_error = e.to_string(),
        };
        match me {
            Ok(account) => out.account = account,
            Err(e) => out.account_error = e.to_string(),
        };
        if out.account.is_some() {
            out.credential_id = session.map(|s| s.credential_id).unwrap_or_default();
            let (library, activity) = tokio::join!(client.library(), client.activity());
            match library {
                Ok(games) => out.library = games,
                Err(e) => out.library_error = e.to_string(),
            };
            out.activity = activity.unwrap_or_default();
        }
        out
    }
    pub async fn request(&self, request: Request) -> Result<Reply> {
        if let Some(error) = &self.config_error {
            bail!("{error}");
        }
        let public = Client::new(&self.store.registry, "")?;
        let mut reply = Reply::default();
        match request {
            Request::Load => reply.snapshot = Some(self.snapshot().await),
            Request::Search(query) => {
                anyhow::ensure!(
                    query.trim().len() <= 64,
                    "Marketplace search must be 64 UTF-8 bytes or fewer."
                );
                reply.catalog = Some(public.search_catalog(&query).await?);
            }
            Request::Game(id) => {
                let (author, slug) = crate::local::parts(&id)?;
                let client = self
                    .client(false)
                    .map(|(client, _)| client)
                    .unwrap_or(public.clone());
                reply.game = Some(client.game(author, slug).await?);
                reply.days = Some(public.play_days(author, slug, OffsetYear::days()).await?);
            }
            Request::Owner(name) => {
                reply.owner = Some(public.owner(&name).await?);
                reply.days = Some(public.play_days(&name, "", OffsetYear::days()).await?);
            }
            Request::Days(id) => {
                let (author, slug) = id.split_once('/').unwrap_or((&id, ""));
                reply.days = Some(public.play_days(author, slug, OffsetYear::days()).await?);
            }
            Request::Members(org) => {
                reply.members = Some(self.client(true)?.0.members(&org).await?);
            }
            Request::Sessions => {
                reply.sessions = Some(self.client(true)?.0.get(&["v1", "cli-credentials"]).await?);
            }
            Request::PairStart => {
                self.store.can_pair()?;
                reply.round = Some(public.start_device("termcade TUI").await?);
            }
            Request::PairWait(round) => {
                self.store.can_pair()?;
                reply.session = Some(public.complete_device(&round).await?);
            }
            Request::Mutate(action) => return self.mutate(action).await,
        }
        Ok(reply)
    }
    async fn mutate(&self, action: Action) -> Result<Reply> {
        let mut reply = Reply::default();
        if let Action::Uninstall(id) = action {
            crate::local::remove(&id)?;
            reply.snapshot = Some(self.snapshot().await);
            reply.notice = "Uninstalled on this machine".into();
            return Ok(reply);
        }
        if matches!(action, Action::Logout) {
            let client = self.client(false);
            self.store.clear()?;
            reply.notice=match client{
                Ok((client,Some(session))) if !session.credential_id.is_empty()=>match client.empty(Method::DELETE,&["v1","cli-credentials",&session.credential_id],None).await{Ok(())=>"Signed out".into(),Err(_)=>"Signed out locally; remote revocation failed. Use session management to revoke it.".into()},
                Ok((_,Some(_)))=>"Signed out locally. Legacy session has no credential ID; revoke it in session management.".into(),
                _=>"Local credentials cleared".into(),
            };
            reply.snapshot = Some(self.snapshot().await);
            return Ok(reply);
        }
        let (client, session) = self.client(true)?;
        let adding = matches!(&action, Action::Add(_));
        match action {
            Action::Add(id) | Action::Remove(id) => {
                let (author, slug) = crate::local::parts(&id)?;
                let added = adding;
                match client.library_set(author, slug, added).await {
                    Ok(()) => (),
                    Err(e) if !added && e.not_found() => (),
                    Err(e) => return Err(e.into()),
                };
            }
            Action::Like(id, liked) => {
                let (author, slug) = crate::local::parts(&id)?;
                client
                    .empty(
                        if liked { Method::PUT } else { Method::DELETE },
                        &["v1", "games", author, slug, "like"],
                        None,
                    )
                    .await?;
            }
            Action::Install(id) => {
                let (author, slug) = crate::local::parts(&id)?;
                anyhow::ensure!(
                    client.library().await?.iter().any(|g| g.id == id),
                    "add this game to your library first"
                );
                let path = crate::local::games_dir()?.join(author).join(slug);
                anyhow::ensure!(
                    !path.try_exists()?,
                    "a local copy already exists; refresh Library instead of overwriting it"
                );
                let file = client.download(author, slug).await?;
                let raw = std::fs::read(file.path())?;
                crate::local::install(&raw, &self.runtime)?;
                reply.installed = Some(id);
            }
            Action::Username(name) => {
                let owner: HandleOwner = client
                    .json(
                        Method::PATCH,
                        "/v1/me/username",
                        Some(json!({"username":name})),
                    )
                    .await?;
                if let Some(mut session) = session {
                    session.username = owner.name;
                    self.store.save(&session)?;
                }
            }
            Action::CheckUsername(name) => {
                reply.notice = match client.owner(&name).await {
                    Ok(_) => "Handle is taken".into(),
                    Err(e) if e.not_found() || e.to_string().contains("is available") => {
                        "Handle is available".into()
                    }
                    Err(e) => return Err(e.into()),
                };
                return Ok(reply);
            }
            Action::OrgCreate(name, bio, link) => {
                client.create_org(&name, &bio, &link).await?;
            }
            Action::MemberSet(org, email, admin) => {
                client
                    .empty(
                        Method::POST,
                        &["v1", "orgs", &org, "members"],
                        Some(json!({"email":email,"admin":admin})),
                    )
                    .await?
            }
            Action::MemberRemove(org, email) => {
                client
                    .empty(
                        Method::DELETE,
                        &["v1", "orgs", &org, "members", &email],
                        None,
                    )
                    .await?
            }
            Action::OrgDelete(org) => {
                client
                    .empty(Method::DELETE, &["v1", "orgs", &org], None)
                    .await?
            }
            Action::Revoke(id) => {
                anyhow::ensure!(!id.is_empty(), "missing credential id");
                client
                    .empty(Method::DELETE, &["v1", "cli-credentials", &id], None)
                    .await?;
                if session.is_some_and(|s| s.credential_id == id) {
                    self.store.clear()?;
                    reply.notice = "This device was revoked; signed out".into();
                }
            }
            Action::DeleteAccount => {
                client.empty(Method::DELETE, &["v1", "me"], None).await?;
                self.store.clear()?;
            }
            Action::Logout | Action::Uninstall(_) => unreachable!(),
        }
        if reply.notice.is_empty() {
            reply.notice = "Done".into();
        }
        reply.snapshot = Some(self.snapshot().await);
        Ok(reply)
    }
}
struct OffsetYear;
impl OffsetYear {
    fn days() -> i32 {
        time::OffsetDateTime::now_utc().ordinal() as i32
    }
}
pub async fn open_url(raw: &str) -> Result<()> {
    let raw = raw.trim();
    let raw = if !raw.contains(':') {
        format!("https://{raw}")
    } else {
        raw.into()
    };
    let url = url::Url::parse(&raw)?;
    anyhow::ensure!(
        matches!(url.scheme(), "http" | "https")
            && url.host_str().is_some()
            && url.username().is_empty()
            && url.password().is_none(),
        "only HTTP(S) links can be opened"
    );
    #[cfg(target_os = "macos")]
    let mut command = tokio::process::Command::new("open");
    #[cfg(target_os = "windows")]
    let mut command = {
        let mut cmd = tokio::process::Command::new("rundll32");
        cmd.arg("url.dll,FileProtocolHandler");
        cmd
    };
    #[cfg(not(any(target_os = "macos", target_os = "windows")))]
    let mut command = tokio::process::Command::new("xdg-open");
    let mut child = command
        .arg(url.as_str())
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .spawn()
        .map_err(|_| anyhow!("could not open a browser; copy the displayed URL instead"))?;
    // Reap long-lived launchers too; an explicit link action must never block the UI.
    let wait = tokio::spawn(async move { child.wait().await });
    match tokio::time::timeout(std::time::Duration::from_secs(1), wait).await {
        Ok(Ok(Ok(status))) if !status.success() => {
            bail!("the browser launcher failed; copy the displayed URL instead")
        }
        _ => Ok(()),
    }
}
