#[cfg(test)]
mod tests;
mod view;
use crate::{
    backend::{Action, Backend, Reply, Request, Snapshot},
    local::Installed,
    scores::{Scores, Settings},
};
use anyhow::{Result, anyhow};
use crossterm::{
    event::{
        DisableBracketedPaste, DisableFocusChange, EnableBracketedPaste, EnableFocusChange, Event,
        EventStream, KeyCode, KeyEvent, KeyEventKind, KeyModifiers, KeyboardEnhancementFlags,
        PopKeyboardEnhancementFlags, PushKeyboardEnhancementFlags,
    },
    execute,
    terminal::{EnterAlternateScreen, LeaveAlternateScreen, disable_raw_mode, enable_raw_mode},
};
use futures_util::StreamExt;
use ratatui::{Terminal, backend::CrosstermBackend};
use std::{
    io::{self, IsTerminal, Write},
    time::{Duration, Instant},
};
use termcade_manifest::Manifest;
use termcade_plugin::{Runtime, WasmGame};
use termcade_registry::{
    self as registry, CliCredential, DeviceRound, Game, HandleOwner, Member, PlayDay,
};
use termcade_sdk::{Canvas, CellShape, Hud, Key};
use tokio::{sync::mpsc, task::JoinHandle};
#[derive(Clone, Debug, PartialEq, Eq)]
enum Page {
    Market,
    Library,
    Game(String),
    Owner(String),
    Activity(String),
    Settings,
    Members(String),
    Sessions,
    Docs,
}
#[derive(Clone)]
enum FormKind {
    Username,
    Org,
    Member(String),
    Confirm(Action, String),
}
#[derive(Clone)]
struct Field {
    label: String,
    value: String,
    caret: usize,
}
#[derive(Clone)]
struct Form {
    title: String,
    kind: FormKind,
    fields: Vec<Field>,
    index: usize,
}
impl Form {
    fn new(title: &str, kind: FormKind, fields: Vec<(&str, String)>) -> Self {
        Self {
            title: title.into(),
            kind,
            fields: fields
                .into_iter()
                .map(|(label, value)| Field {
                    label: label.into(),
                    caret: value.chars().count(),
                    value,
                })
                .collect(),
            index: 0,
        }
    }
    fn buttons(&self) -> usize {
        if matches!(self.kind, FormKind::Username) {
            3
        } else {
            2
        }
    }
    fn text(&mut self, text: &str) {
        if let Some(field) = self.fields.get_mut(self.index) {
            let mut chars: Vec<char> = field.value.chars().collect();
            let insert: Vec<char> = text.chars().filter(|c| !c.is_control()).collect();
            if chars.len() + insert.len() <= 2048 {
                chars.splice(field.caret..field.caret, insert.iter().copied());
                field.caret += insert.len();
                field.value = chars.into_iter().collect();
            }
        }
    }
    fn edit(&mut self, key: KeyEvent) {
        if key.code == KeyCode::Tab || key.code == KeyCode::BackTab {
            let count = self.fields.len() + self.buttons();
            let delta = if key.code == KeyCode::BackTab {
                count - 1
            } else {
                1
            };
            self.index = (self.index + delta) % count;
            return;
        }
        let Some(field) = self.fields.get_mut(self.index) else {
            return;
        };
        let mut chars: Vec<char> = field.value.chars().collect();
        match key.code {
            KeyCode::Left => field.caret = field.caret.saturating_sub(1),
            KeyCode::Right => field.caret = (field.caret + 1).min(chars.len()),
            KeyCode::Home => field.caret = 0,
            KeyCode::End => field.caret = chars.len(),
            KeyCode::Backspace if field.caret > 0 => {
                chars.remove(field.caret - 1);
                field.caret -= 1;
                field.value = chars.into_iter().collect();
            }
            KeyCode::Delete if field.caret < chars.len() => {
                chars.remove(field.caret);
                field.value = chars.into_iter().collect();
            }
            KeyCode::Char('u') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                field.value.clear();
                field.caret = 0;
            }
            KeyCode::Char(c)
                if !key
                    .modifiers
                    .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT) =>
            {
                self.text(&c.to_string())
            }
            _ => (),
        }
    }
}
#[derive(Clone)]
enum Command {
    None,
    Go(Page),
    Start(String),
    Mutate(Action),
    Form(String, FormKind, Vec<(String, String)>),
    Confirm(String, Action, String),
    Login,
    Pixels,
    Open(String),
    Refresh,
    Back,
}
#[derive(Clone)]
struct Item {
    label: String,
    detail: String,
    command: Command,
}
impl Item {
    fn new(label: impl Into<String>, detail: impl Into<String>, command: Command) -> Self {
        Self {
            label: label.into(),
            detail: detail.into(),
            command,
        }
    }
}
struct Pairing {
    code: String,
    url: String,
    round: Option<DeviceRound>,
    button: usize,
}
#[derive(PartialEq, Eq)]
enum Mode {
    Browse,
    Playing,
    Paused,
    GameOver,
    Crashed,
}
struct ActiveGame {
    guest: WasmGame,
    manifest: Manifest,
    canvas: Canvas,
    hud: Hud,
    score: i64,
    recorded: bool,
}
enum Message {
    Reply {
        generation: u64,
        request: Request,
        result: Result<Reply>,
    },
    Search {
        generation: u64,
        result: Result<Reply>,
    },
    Loaded {
        generation: u64,
        result: Result<ActiveGame>,
    },
    Notice {
        generation: u64,
        result: Result<()>,
    },
}
struct App {
    backend: Backend,
    snapshot: Snapshot,
    page: Page,
    history: Vec<Page>,
    focus: usize,
    selection: usize,
    nav_selection: usize,
    action_selection: usize,
    nav_visible: bool,
    game_nav: bool,
    mutating: bool,
    scroll: u16,
    query: String,
    search_focus: bool,
    search: Option<Vec<Game>>,
    search_gen: u64,
    search_loading: bool,
    game_detail: Option<Game>,
    owner: Option<HandleOwner>,
    days: Vec<PlayDay>,
    members: Vec<Member>,
    sessions: Vec<CliCredential>,
    form: Option<Form>,
    pairing: Option<Pairing>,
    notice: String,
    loading: bool,
    generation: u64,
    job: Option<JoinHandle<()>>,
    search_job: Option<JoinHandle<()>>,
    sync_job: Option<JoinHandle<()>>,
    tx: mpsc::Sender<Message>,
    mode: Mode,
    game: Option<ActiveGame>,
    shape: CellShape,
    scores: Scores,
    pause_selection: usize,
    new_high: bool,
    crash: String,
    last_tick: Instant,
    accum: Duration,
    width: u16,
    height: u16,
}
impl Drop for App {
    fn drop(&mut self) {
        if let Some(job) = &self.job {
            job.abort();
        }
        if let Some(job) = &self.search_job {
            job.abort();
        }
        if let Some(job) = &self.sync_job {
            job.abort();
        }
    }
}
impl App {
    fn new(backend: Backend, scores: Scores, shape: CellShape, tx: mpsc::Sender<Message>) -> Self {
        Self {
            backend,
            snapshot: Snapshot {
                installed: crate::local::discover().unwrap_or_default(),
                ..Snapshot::default()
            },
            page: Page::Market,
            history: Vec::new(),
            focus: 1,
            selection: 0,
            nav_selection: 0,
            action_selection: 0,
            nav_visible: true,
            game_nav: false,
            mutating: false,
            scroll: 0,
            query: String::new(),
            search_focus: false,
            search: None,
            search_gen: 0,
            search_loading: false,
            game_detail: None,
            owner: None,
            days: Vec::new(),
            members: Vec::new(),
            sessions: Vec::new(),
            form: None,
            pairing: None,
            notice: String::new(),
            loading: false,
            generation: 0,
            job: None,
            search_job: None,
            sync_job: None,
            tx,
            mode: Mode::Browse,
            game: None,
            shape,
            scores,
            pause_selection: 0,
            new_high: false,
            crash: String::new(),
            last_tick: Instant::now(),
            accum: Duration::ZERO,
            width: 80,
            height: 24,
        }
    }
    fn cancel(&mut self) {
        self.generation = self.generation.wrapping_add(1);
        if let Some(job) = self.job.take() {
            job.abort();
        }
        self.loading = false;
        self.mutating = false;
    }
    fn request(&mut self, request: Request) {
        self.cancel();
        self.loading = true;
        self.mutating = matches!(&request, Request::Mutate(_));
        let generation = self.generation;
        let backend = self.backend.clone();
        let tx = self.tx.clone();
        let deadline = match &request {
            Request::PairWait(round) => round.expires_in + Duration::from_secs(1),
            _ => Duration::from_secs(45),
        };
        self.job = Some(tokio::spawn(async move {
            let result = tokio::time::timeout(deadline, backend.request(request.clone()))
                .await
                .map_err(|_| anyhow!("Request timed out; refresh to check the current state."))
                .and_then(|r| r);
            let _ = tx
                .send(Message::Reply {
                    generation,
                    request,
                    result,
                })
                .await;
        }));
    }
    fn navigate(&mut self, page: Page, remember: bool) {
        self.cancel();
        if remember && page != self.page {
            self.history.push(self.page.clone());
        }
        self.page = page.clone();
        self.selection = 0;
        self.action_selection = 0;
        self.scroll = 0;
        self.search_focus = false;
        self.focus = 1;
        self.notice.clear();
        self.members.clear();
        self.sessions.clear();
        self.days.clear();
        self.owner = None;
        self.game_detail = None;
        match page {
            Page::Game(id) => {
                self.game_detail = self.metadata(&id);
                self.request(Request::Game(id));
            }
            Page::Owner(id) => self.request(Request::Owner(id)),
            Page::Activity(id) => self.request(Request::Days(id)),
            Page::Members(id) => self.request(Request::Members(id)),
            Page::Sessions => self.request(Request::Sessions),
            Page::Market => {
                if self.snapshot.catalog.is_empty() {
                    self.request(Request::Load);
                }
            }
            _ => (),
        }
    }
    fn metadata(&self, id: &str) -> Option<Game> {
        self.game_detail
            .as_ref()
            .filter(|g| g.id == id)
            .cloned()
            .or_else(|| {
                self.snapshot
                    .catalog
                    .iter()
                    .chain(&self.snapshot.library)
                    .find(|g| g.id == id)
                    .cloned()
            })
            .or_else(|| {
                self.search
                    .as_ref()
                    .and_then(|games| games.iter().find(|g| g.id == id).cloned())
            })
            .or_else(|| {
                self.snapshot
                    .installed
                    .iter()
                    .find(|g| g.id == id)
                    .and_then(|g| g.manifest.as_ref())
                    .map(|m| Game {
                        id: m.game.id.clone(),
                        name: m.game.name.clone(),
                        description: m.game.description.clone(),
                        version: m.game.version.clone(),
                        abi: m.requirements.abi,
                        width: m.requirements.width,
                        height: m.requirements.height,
                        has_package: false,
                        ..Game::default()
                    })
            })
    }
    fn in_library(&self, id: &str) -> bool {
        self.snapshot.library.iter().any(|g| g.id == id)
    }
    fn admin(&self, id: &str) -> bool {
        self.snapshot
            .account
            .as_ref()
            .is_some_and(|a| a.orgs.iter().any(|o| o.username == id && o.admin))
    }
    fn local(&self, id: &str) -> Option<&Installed> {
        self.snapshot.installed.iter().find(|g| g.id == id)
    }
    fn selected_id(&self) -> Option<String> {
        if let Page::Game(id) = &self.page {
            return Some(id.clone());
        }
        self.items()
            .get(self.selection)
            .and_then(|item| match &item.command {
                Command::Go(Page::Game(id)) | Command::Start(id) => Some(id.clone()),
                _ => None,
            })
    }
    fn navigation(&self) -> Vec<Item> {
        let mut items = vec![
            Item::new("Marketplace", "", Command::Go(Page::Market)),
            Item::new("Library", "", Command::Go(Page::Library)),
            Item::new("Docs", "", Command::Go(Page::Docs)),
            Item::new("Settings", "", Command::Go(Page::Settings)),
        ];
        let mut games = self.snapshot.library.clone();
        for local in &self.snapshot.installed {
            if !games.iter().any(|g| g.id == local.id)
                && let Some(g) = self.metadata(&local.id)
            {
                games.push(g);
            }
        }
        for game in games {
            items.push(Item::new(
                format!("  {}", game.name),
                game.id.clone(),
                Command::Go(Page::Game(game.id)),
            ));
        }
        items.push(Item::new(
            self.snapshot
                .account
                .as_ref()
                .map(|a| {
                    if a.username.is_empty() {
                        "Account".into()
                    } else {
                        format!("@{}", a.username)
                    }
                })
                .unwrap_or_else(|| "Sign in / Settings".into()),
            "",
            if self.snapshot.account.is_some() {
                Command::Go(Page::Settings)
            } else {
                Command::Login
            },
        ));
        items
    }
    fn game_item(&self, game: &Game) -> Item {
        let local = self.local(&game.id);
        let state = if let Some(local) = local {
            if local.error.is_empty() {
                "installed"
            } else {
                "broken"
            }
        } else if self.in_library(&game.id) {
            "in library"
        } else if !game.has_package {
            "unpublished"
        } else if game.abi != 1 {
            "incompatible"
        } else {
            ""
        };
        Item::new(
            format!(
                "{}  {}  {}",
                game.id,
                if game.liked { "♥" } else { "♡" },
                game.likes
            ),
            format!(
                "{}{}{}",
                game.description,
                if state.is_empty() { "" } else { " · " },
                state
            ),
            Command::Go(Page::Game(game.id.clone())),
        )
    }
    fn items(&self) -> Vec<Item> {
        let mut items = Vec::new();
        match &self.page {
            Page::Market => {
                items.push(Item::new(
                    format!("Search: {}", self.query),
                    "/ to search; Enter or Esc returns to games",
                    Command::None,
                ));
                for game in self.search.as_ref().unwrap_or(&self.snapshot.catalog) {
                    items.push(self.game_item(game));
                }
            }
            Page::Library => {
                let mut seen = std::collections::HashSet::new();
                for game in &self.snapshot.library {
                    seen.insert(game.id.clone());
                    items.push(self.game_item(game));
                }
                for local in &self.snapshot.installed {
                    if !seen.contains(&local.id) {
                        items.push(Item::new(
                            format!("{} · local", local.title),
                            if local.error.is_empty() {
                                local.id.clone()
                            } else {
                                local.error.clone()
                            },
                            Command::Go(Page::Game(local.id.clone())),
                        ));
                    }
                }
            }
            Page::Game(_) => (),
            Page::Owner(name) => {
                if let Some(owner) = &self.owner {
                    items.push(Item::new(
                        format!(
                            "@{} · {}",
                            owner.name,
                            if owner.is_org { "org" } else { "developer" }
                        ),
                        &owner.bio,
                        Command::None,
                    ));
                    if !owner.link.is_empty() {
                        items.push(Item::new(
                            "Website",
                            &owner.link,
                            Command::Open(owner.link.clone()),
                        ));
                    }
                    items.push(Item::new(
                        "Public play activity",
                        "",
                        Command::Go(Page::Activity(name.clone())),
                    ));
                    for game in &owner.games {
                        items.push(self.game_item(game));
                    }
                }
            }
            Page::Activity(_) => {
                for day in &self.days {
                    items.push(Item::new(
                        format!(
                            "{}  {}  {} plays",
                            day.day,
                            "█".repeat((day.count.max(0) as usize).min(30)),
                            day.count
                        ),
                        "",
                        Command::None,
                    ));
                }
            }
            Page::Settings => {
                if let Some(account) = &self.snapshot.account {
                    items.push(Item::new(&account.email, "", Command::None));
                    items.push(Item::new(
                        format!("Handle: @{}", account.username),
                        "Check availability, claim or rename",
                        Command::Form(
                            "Handle availability / claim / rename".into(),
                            FormKind::Username,
                            vec![("Handle".into(), account.username.clone())],
                        ),
                    ));
                    items.push(Item::new(
                        "Create an org",
                        "",
                        Command::Form(
                            "Create org".into(),
                            FormKind::Org,
                            vec![
                                ("Handle".into(), String::new()),
                                ("Bio (optional)".into(), String::new()),
                                ("Website (optional)".into(), String::new()),
                            ],
                        ),
                    ));
                    for org in &account.orgs {
                        items.push(Item::new(
                            format!(
                                "@{} · {}",
                                org.username,
                                if org.admin { "admin" } else { "member" }
                            ),
                            &org.bio,
                            Command::Go(Page::Members(org.username.clone())),
                        ));
                    }
                    items.push(Item::new(
                        "CLI sessions",
                        "View and revoke login sessions (not publish keys)",
                        Command::Go(Page::Sessions),
                    ));
                    items.push(Item::new(
                        "Sign out",
                        "Revoke this device, then clear locally",
                        Command::Confirm(
                            "Revoke this device and sign out".into(),
                            Action::Logout,
                            "sign out".into(),
                        ),
                    ));
                    items.push(Item::new(
                        "Delete account",
                        "Subject to published-game and org-admin safeguards",
                        Command::Confirm(
                            "Delete account".into(),
                            Action::DeleteAccount,
                            account.email.clone(),
                        ),
                    ));
                } else {
                    items.push(Item::new(
                        "Sign in",
                        "Approve this terminal in your browser",
                        Command::Login,
                    ));
                }
                items.push(Item::new(
                    format!("Pixel style: {}", self.shape.name),
                    "Change for the next game start",
                    Command::Pixels,
                ));
            }
            Page::Members(org) => {
                for member in &self.members {
                    items.push(Item::new(
                        format!(
                            "{} · {}",
                            member.email,
                            if member.admin { "admin" } else { "member" }
                        ),
                        "",
                        if self.admin(org) {
                            Command::Form(
                                "Add member / change role".into(),
                                FormKind::Member(org.clone()),
                                vec![
                                    ("Existing account email".into(), member.email.clone()),
                                    (
                                        "Role: member or admin".into(),
                                        if member.admin { "admin" } else { "member" }.into(),
                                    ),
                                ],
                            )
                        } else {
                            Command::None
                        },
                    ));
                }
            }
            Page::Sessions => {
                for credential in &self.sessions {
                    items.push(Item::new(
                        format!(
                            "{} · {}{}",
                            credential.device_name,
                            if credential.revoked {
                                "revoked"
                            } else {
                                "active"
                            },
                            if credential.id == self.snapshot.credential_id {
                                " · this device"
                            } else {
                                ""
                            }
                        ),
                        format!(
                            "expires {} · last used {} · created {}",
                            credential.expires_at, credential.last_used_at, credential.created_at
                        ),
                        if credential.revoked {
                            Command::None
                        } else {
                            Command::Confirm(
                                format!("Revoke {} ({})", credential.device_name, credential.id),
                                Action::Revoke(credential.id.clone()),
                                "revoke".into(),
                            )
                        },
                    ));
                }
            }
            Page::Docs => {
                for (title, body) in [
                    ("termcade", "One arcade in your terminal and browser."),
                    (
                        "Library",
                        "Games you add follow your account. Play installs a missing package. Local games stay playable offline.",
                    ),
                    (
                        "Removal",
                        "Remove from Library changes your account. Uninstall here removes only this machine's copy.",
                    ),
                    (
                        "Build a Rust game",
                        "termcade dev new author/slug\nrustup target add wasm32-wasip1\ntermcade dev build\ntermcade dev install build/slug.tcade",
                    ),
                    ("Publish", "termcade publish <github-repo> <tag> [asset]"),
                    (
                        "Keyboard",
                        "Ctrl+N toggles navigation. Tab / Shift+Tab changes focus. Arrows select; Enter activates. Esc goes back or pauses; Ctrl+C quits. PgUp/PgDown scrolls text.",
                    ),
                    (
                        "Gameplay",
                        "Arrows/WASD to move · Space/Z action A · X action B · Enter start. P/Esc pauses; pixel style applies at the next game start.",
                    ),
                ] {
                    items.push(Item::new(title, body, Command::None));
                }
            }
        }
        items
    }
    fn actions(&self) -> Vec<Item> {
        let mut items = vec![
            Item::new("Esc Back", "", Command::Back),
            Item::new("R Refresh", "", Command::Refresh),
        ];
        if let Some(id) = self.selected_id() {
            let local = self.local(&id);
            let game = self.metadata(&id).unwrap_or_default();
            if local.is_some_and(|g| g.error.is_empty())
                || local.is_none() && self.in_library(&id) && game.has_package && game.abi == 1
            {
                items.push(Item::new("P Play", "", Command::Start(id.clone())));
            }
            if self.snapshot.account.is_some() && self.snapshot.library_error.is_empty() {
                items.push(Item::new(
                    if self.in_library(&id) {
                        "Remove from Library"
                    } else {
                        "A Add"
                    },
                    "",
                    Command::Mutate(if self.in_library(&id) {
                        Action::Remove(id.clone())
                    } else {
                        Action::Add(id.clone())
                    }),
                ));
            }
            if self.snapshot.account.is_some() {
                items.push(Item::new(
                    if game.liked { "L Unlike" } else { "L Like" },
                    "",
                    Command::Mutate(Action::Like(id.clone(), !game.liked)),
                ));
            }
            if local.is_some() {
                items.push(Item::new(
                    "U Uninstall here",
                    "",
                    Command::Confirm(
                        "Uninstall local copy".into(),
                        Action::Uninstall(id.clone()),
                        id.clone(),
                    ),
                ));
            }
            if let Some((owner, _)) = id.split_once('/') {
                items.push(Item::new(
                    "O Owner",
                    "",
                    Command::Go(Page::Owner(owner.into())),
                ));
            }
            if !game.repo.is_empty() {
                items.push(Item::new("B Repository", "", Command::Open(game.repo)));
            }
            items.push(Item::new("C Activity", "", Command::Go(Page::Activity(id))));
        }
        if let Page::Members(org) = &self.page {
            items.push(Item::new(
                "View org",
                "",
                Command::Go(Page::Owner(org.clone())),
            ));
            if self.admin(org) {
                items.push(Item::new(
                    "Add member",
                    "",
                    Command::Form(
                        "Add member / change role".into(),
                        FormKind::Member(org.clone()),
                        vec![
                            ("Existing account email".into(), String::new()),
                            ("Role: member or admin".into(), "member".into()),
                        ],
                    ),
                ));
                if let Some(member) = self.members.get(self.selection) {
                    items.push(Item::new(
                        "Remove member",
                        "",
                        Command::Confirm(
                            "Remove member".into(),
                            Action::MemberRemove(org.clone(), member.email.clone()),
                            member.email.clone(),
                        ),
                    ));
                }
                items.push(Item::new(
                    "Dissolve org",
                    "",
                    Command::Confirm(
                        "Dissolve org".into(),
                        Action::OrgDelete(org.clone()),
                        org.clone(),
                    ),
                ));
            }
        }
        items
    }
    fn search(&mut self) {
        self.search_gen = self.search_gen.wrapping_add(1);
        if let Some(job) = self.search_job.take() {
            job.abort();
        }
        self.search_loading = false;
        if self.query.trim().is_empty() {
            self.search = None;
            return;
        }
        if self.query.trim().len() > 64 {
            self.search = Some(Vec::new());
            self.notice = "Marketplace search must be 64 UTF-8 bytes or fewer.".into();
            return;
        }
        self.search_loading = true;
        let generation = self.search_gen;
        let backend = self.backend.clone();
        let query = self.query.clone();
        let tx = self.tx.clone();
        self.search_job = Some(tokio::spawn(async move {
            tokio::time::sleep(Duration::from_millis(300)).await;
            let result = tokio::time::timeout(
                Duration::from_secs(45),
                backend.request(Request::Search(query)),
            )
            .await
            .map_err(|_| anyhow!("Search timed out"))
            .and_then(|v| v);
            let _ = tx.send(Message::Search { generation, result }).await;
        }));
    }
    fn sync_activity(&mut self) {
        if self.sync_job.as_ref().is_some_and(|job| !job.is_finished()) {
            return;
        }
        let Ok((client, Some(_))) = self.backend.client(false) else {
            return;
        };
        let mut scores = self.scores.clone();
        let tx = self.tx.clone();
        let generation = self.generation;
        self.sync_job = Some(tokio::spawn(async move {
            let result = tokio::time::timeout(Duration::from_secs(45), scores.sync(&client)).await;
            let notice = match result {
                Ok(Ok(1)) => Some("1 run synced to your account".to_string()),
                Ok(Ok(n)) if n > 1 => Some(format!("{n} runs synced to your account")),
                _ => None,
            };
            if let Some(notice) = notice {
                let _ = tx
                    .send(Message::Reply {
                        generation,
                        request: Request::Search(String::new()),
                        result: Ok(Reply {
                            notice,
                            ..Reply::default()
                        }),
                    })
                    .await;
            }
        }));
    }
    fn clear_private(&mut self) {
        if let Some(job) = self.sync_job.take() {
            job.abort();
        }
        self.snapshot.account = None;
        self.snapshot.library.clear();
        self.snapshot.activity.clear();
        self.snapshot.credential_id.clear();
        self.members.clear();
        self.sessions.clear();
        self.form = None;
    }
    fn begin_play(&mut self, id: String) {
        let Some(local) = self.local(&id).cloned() else {
            if self.in_library(&id) {
                self.request(Request::Mutate(Action::Install(id)));
            } else {
                self.notice = "Add a compatible published game to your Library first".into();
            }
            return;
        };
        let Some(manifest) = local.manifest else {
            self.notice = format!("Broken package: {}", local.error);
            return;
        };
        self.cancel();
        self.loading = true;
        let generation = self.generation;
        let tx = self.tx.clone();
        let runtime = self.backend.runtime.clone();
        let shape = self.shape.clone();
        self.job = Some(tokio::spawn(async move {
            let result = async {
                let path = local.dir.join(termcade_manifest::WASM_NAME);
                anyhow::ensure!(
                    std::fs::metadata(&path)?.len() <= termcade_manifest::MAX_WASM_BYTES as u64,
                    "game.wasm exceeds the byte limit"
                );
                let raw = std::fs::read(path)?;
                let mut guest = runtime
                    .load(&manifest.game.id, &raw, manifest.info(), shape.clone())
                    .await?;
                guest.reset().await?;
                let mut canvas = Canvas::new(
                    manifest.requirements.width as usize,
                    manifest.requirements.height as usize,
                    termcade_sdk::BLACK,
                    shape,
                );
                guest.draw(&mut canvas).await?;
                let hud = guest.hud().await?;
                Ok(ActiveGame {
                    guest,
                    manifest,
                    canvas,
                    hud,
                    score: 0,
                    recorded: false,
                })
            }
            .await;
            let _ = tx.send(Message::Loaded { generation, result }).await;
        }));
    }
    async fn execute(&mut self, command: Command) -> Result<()> {
        if self.loading
            && !matches!(
                command,
                Command::Back | Command::Open(_) | Command::Start(_) | Command::Go(_)
            )
        {
            self.notice = "Wait for the current request, or Esc to cancel it".into();
            return Ok(());
        }
        match command {
            Command::None => (),
            Command::Go(page) => self.navigate(page, true),
            Command::Back => {
                let reconcile = self.mutating;
                self.cancel();
                self.form = None;
                self.pairing = None;
                let page = self.history.pop().unwrap_or(Page::Market);
                self.navigate(page, false);
                if reconcile {
                    self.request(Request::Load);
                }
            }
            Command::Refresh => {
                let page = self.page.clone();
                self.request(Request::Load);
                self.notice = format!("Refreshing {}", view::page_title(&page));
            }
            Command::Start(id) => self.begin_play(id),
            Command::Mutate(action) => {
                if matches!(action, Action::Logout | Action::DeleteAccount) {
                    self.clear_private();
                }
                self.request(Request::Mutate(action));
            }
            Command::Form(title, kind, fields) => {
                self.form = Some(Form::new(
                    &title,
                    kind,
                    fields
                        .iter()
                        .map(|(label, value)| (label.as_str(), value.clone()))
                        .collect(),
                ));
                self.notice.clear();
            }
            Command::Confirm(title, action, confirmation) => {
                if confirmation.is_empty() {
                    self.notice = "A complete account identity is required for confirmation".into();
                } else {
                    self.form = Some(Form::new(
                        &title,
                        FormKind::Confirm(action, confirmation.clone()),
                        vec![(&format!("Type {confirmation} to confirm"), String::new())],
                    ));
                    self.notice.clear();
                }
            }
            Command::Login => {
                self.pairing = Some(Pairing {
                    code: String::new(),
                    url: String::new(),
                    round: None,
                    button: 0,
                });
                self.request(Request::PairStart);
            }
            Command::Pixels => {
                let names = ["quad", "sextant", "half", "ascii"];
                let at = names
                    .iter()
                    .position(|name| *name == self.shape.name)
                    .unwrap_or(0);
                let name = names[(at + 1) % names.len()];
                self.shape = CellShape::lookup(name).expect("pixel cycle");
                Settings {
                    pixels: name.into(),
                }
                .save()?;
                self.notice = format!("pixels: {name}");
            }
            Command::Open(url) => {
                if self.width < 40 || self.height < 20 {
                    self.notice = "Resize to at least 40 × 20 to open a browser".into();
                    return Ok(());
                }
                let generation = self.generation;
                let tx = self.tx.clone();
                tokio::spawn(async move {
                    let result = crate::backend::open_url(&url).await;
                    let _ = tx.send(Message::Notice { generation, result }).await;
                });
            }
        }
        Ok(())
    }
    async fn message(&mut self, message: Message) -> Result<()> {
        match message {
            Message::Notice {
                generation,
                result: Err(e),
            } if generation == self.generation => {
                self.notice = e.to_string();
            }
            Message::Search { generation, result } if generation == self.search_gen => {
                self.search_loading = false;
                match result {
                    Ok(reply) => self.search = reply.catalog,
                    Err(e) => {
                        self.search = Some(Vec::new());
                        self.notice = e.to_string();
                    }
                }
            }
            Message::Loaded { generation, result } if generation == self.generation => {
                self.loading = false;
                match result {
                    Ok(game) => {
                        self.scores.touch(&game.manifest.game.id);
                        self.scores.save()?;
                        self.page = Page::Game(game.manifest.game.id.clone());
                        self.game = Some(game);
                        self.mode = Mode::Playing;
                        self.last_tick = Instant::now();
                        self.accum = Duration::ZERO;
                        self.notice.clear();
                    }
                    Err(e) => self.notice = e.to_string(),
                }
            }
            Message::Reply {
                generation,
                request,
                result,
            } if generation == self.generation => {
                self.loading = false;
                let reply = match result {
                    Ok(reply) => reply,
                    Err(e) => {
                        self.notice = e.to_string();
                        return Ok(());
                    }
                };
                if let Some(snapshot) = reply.snapshot {
                    let gained = self.snapshot.account.is_none() && snapshot.account.is_some();
                    let lost = self.snapshot.account.is_some() && snapshot.account.is_none();
                    if lost {
                        self.clear_private();
                    }
                    for record in &snapshot.activity {
                        self.scores.merge(record);
                    }
                    self.scores.save()?;
                    self.snapshot = snapshot;
                    if gained {
                        self.sync_activity();
                    }
                    if self.snapshot.account.is_none()
                        && matches!(self.page, Page::Members(_) | Page::Sessions)
                    {
                        self.page = Page::Settings;
                        self.selection = 0;
                    }
                }
                if let Some(game) = reply.game {
                    self.game_detail = Some(game);
                }
                if let Some(owner) = reply.owner {
                    self.owner = Some(owner);
                }
                if let Some(days) = reply.days {
                    self.days = days;
                }
                if let Some(members) = reply.members {
                    self.members = members;
                }
                if let Some(sessions) = reply.sessions {
                    self.sessions = sessions;
                }
                if !reply.notice.is_empty() {
                    self.notice = reply.notice;
                }
                if let Some(round) = reply.round
                    && let Some(pair) = &mut self.pairing
                {
                    pair.code = round.user_code.clone();
                    pair.url = registry::pairing_url_with_code(
                        &self.backend.store.pairing_url,
                        &round.user_code,
                    );
                    pair.round = Some(round.clone());
                    self.request(Request::PairWait(round));
                }
                if let Some(session) = reply.session
                    && self.pairing.is_some()
                {
                    self.backend.store.save(&session)?;
                    self.pairing = None;
                    self.clear_private();
                    self.notice = "Signed in".into();
                    self.request(Request::Load);
                }
                if matches!(&request, Request::Load) {
                    match &self.page {
                        Page::Game(id) => self.request(Request::Game(id.clone())),
                        Page::Owner(id) => self.request(Request::Owner(id.clone())),
                        Page::Activity(id) => self.request(Request::Days(id.clone())),
                        Page::Members(id) if self.snapshot.account.is_some() => {
                            self.request(Request::Members(id.clone()))
                        }
                        Page::Sessions if self.snapshot.account.is_some() => {
                            self.request(Request::Sessions)
                        }
                        _ => (),
                    }
                }
                if let Request::Mutate(action) = request {
                    if !matches!(action, Action::CheckUsername(_)) {
                        self.form = None;
                    }
                    if matches!(action, Action::OrgDelete(_)) {
                        self.page = Page::Settings;
                        self.selection = 0;
                        self.focus = 1;
                        self.members.clear();
                    }
                    match &self.page {
                        Page::Members(id) => self.request(Request::Members(id.clone())),
                        Page::Sessions => self.request(Request::Sessions),
                        Page::Game(id) => self.request(Request::Game(id.clone())),
                        _ => (),
                    }
                }
                if let Some(id) = reply.installed {
                    self.begin_play(id);
                }
            }
            _ => (),
        }
        Ok(())
    }
    async fn close_game(&mut self) -> Result<()> {
        if let Some(mut game) = self.game.take()
            && !game.recorded
            && matches!(self.mode, Mode::Playing | Mode::Paused)
            && let Ok(score) = game.guest.score().await
        {
            self.scores.record(
                &game.manifest.game.id,
                score,
                &game.manifest.game.version,
                false,
            );
        }
        self.scores.save()?;
        self.sync_activity();
        self.mode = Mode::Browse;
        self.game_nav = false;
        self.page = Page::Market;
        self.focus = 1;
        self.selection = 0;
        self.request(Request::Load);
        Ok(())
    }
    async fn tick(&mut self) -> Result<()> {
        if self.mode != Mode::Playing {
            return Ok(());
        }
        let now = Instant::now();
        let step = Duration::from_nanos(1_000_000_000 / 60);
        self.accum = (self.accum + now.duration_since(self.last_tick)).min(step * 5);
        self.last_tick = now;
        let Some(game) = &mut self.game else {
            return Ok(());
        };
        let result = async {
            while self.accum >= step {
                self.accum -= step;
                if game.guest.update().await? == 1 {
                    game.score = game.guest.score().await?;
                    self.new_high = self.scores.record(
                        &game.manifest.game.id,
                        game.score,
                        &game.manifest.game.version,
                        true,
                    );
                    self.scores.save()?;
                    game.recorded = true;
                    self.mode = Mode::GameOver;
                    break;
                }
            }
            game.canvas.clear();
            game.guest.draw(&mut game.canvas).await?;
            game.score = game.guest.score().await?;
            game.hud = game.guest.hud().await?;
            Ok::<_, anyhow::Error>(())
        }
        .await;
        if let Err(e) = result {
            self.mode = Mode::Crashed;
            self.crash = e.to_string();
        }
        if self.mode == Mode::GameOver {
            self.sync_activity();
        }
        Ok(())
    }
    async fn key(&mut self, key: KeyEvent) -> Result<bool> {
        if key.kind != KeyEventKind::Release
            && key.code == KeyCode::Char('c')
            && key.modifiers.contains(KeyModifiers::CONTROL)
        {
            return Ok(true);
        }
        if key.kind == KeyEventKind::Repeat
            && self.mode != Mode::Playing
            && (matches!(
                key.code,
                KeyCode::Enter | KeyCode::Tab | KeyCode::BackTab | KeyCode::Esc
            ) || key.modifiers.contains(KeyModifiers::CONTROL)
                && key.code == KeyCode::Char('n'))
        {
            return Ok(false);
        }
        if key.kind == KeyEventKind::Release {
            if matches!(self.mode, Mode::Playing | Mode::Paused) {
                let k = translate(key.code);
                if k != Key::None
                    && let Some(game) = &mut self.game
                    && let Err(e) = game.guest.key(k, false).await
                {
                    self.crash = e.to_string();
                    self.mode = Mode::Crashed;
                }
            }
            return Ok(false);
        }
        if self.form.is_some() {
            self.form_key(key).await?;
            return Ok(false);
        }
        if self.pairing.is_some() {
            match key.code {
                KeyCode::Esc => {
                    self.cancel();
                    self.pairing = None;
                    self.notice.clear();
                }
                _ if self.width < 40 || self.height < 20 => (),
                KeyCode::Tab | KeyCode::BackTab | KeyCode::Left | KeyCode::Right => {
                    let pair = self.pairing.as_mut().expect("pairing");
                    pair.button = (pair.button + 1) % 2;
                }
                KeyCode::Char('r') => self.request(Request::PairStart),
                KeyCode::Enter => {
                    let pair = self.pairing.as_ref().expect("pairing");
                    if pair.button == 1 {
                        self.cancel();
                        self.pairing = None;
                    } else if !pair.url.is_empty() {
                        self.execute(Command::Open(pair.url.clone())).await?;
                    }
                }
                _ => (),
            }
            return Ok(false);
        }
        if matches!(self.mode, Mode::Paused | Mode::GameOver | Mode::Crashed) {
            if key.code == KeyCode::Char('n') && key.modifiers.contains(KeyModifiers::CONTROL) {
                self.game_nav = !self.game_nav;
                return Ok(false);
            }
            if self.game_nav {
                let items = self.navigation();
                match key.code {
                    KeyCode::Esc => self.game_nav = false,
                    KeyCode::Up | KeyCode::BackTab => {
                        self.nav_selection = (self.nav_selection + items.len() - 1) % items.len()
                    }
                    KeyCode::Down | KeyCode::Tab => {
                        self.nav_selection = (self.nav_selection + 1) % items.len()
                    }
                    KeyCode::Enter => {
                        if let Some(item) = items.get(self.nav_selection) {
                            let command = item.command.clone();
                            if !matches!(command, Command::Login) {
                                self.close_game().await?;
                            }
                            self.execute(command).await?;
                        }
                    }
                    _ => (),
                }
                return Ok(false);
            }
        }
        match self.mode {
            Mode::Playing => {
                if matches!(key.code, KeyCode::Esc | KeyCode::Char('p')) {
                    self.mode = Mode::Paused;
                    self.pause_selection = 0;
                } else {
                    let k = translate(key.code);
                    if k != Key::None
                        && let Some(game) = &mut self.game
                        && let Err(e) = game.guest.key(k, true).await
                    {
                        self.crash = e.to_string();
                        self.mode = Mode::Crashed;
                    }
                }
                return Ok(false);
            }
            Mode::Paused => {
                match key.code {
                    KeyCode::Esc | KeyCode::Char('p') => {
                        self.mode = Mode::Playing;
                        self.last_tick = Instant::now();
                        self.accum = Duration::ZERO;
                    }
                    KeyCode::Tab
                    | KeyCode::BackTab
                    | KeyCode::Up
                    | KeyCode::Down
                    | KeyCode::Left
                    | KeyCode::Right => self.pause_selection = (self.pause_selection + 1) % 2,
                    KeyCode::Enter => {
                        if self.pause_selection == 0 {
                            self.mode = Mode::Playing;
                            self.last_tick = Instant::now();
                            self.accum = Duration::ZERO;
                        } else {
                            self.close_game().await?;
                        }
                    }
                    _ => (),
                }
                return Ok(false);
            }
            Mode::GameOver => {
                match key.code {
                    KeyCode::Enter | KeyCode::Char('r') => {
                        let id = self
                            .game
                            .as_ref()
                            .expect("active game")
                            .manifest
                            .game
                            .id
                            .clone();
                        self.game = None;
                        self.mode = Mode::Browse;
                        self.begin_play(id);
                    }
                    KeyCode::Esc | KeyCode::Char('m' | 'q') => self.close_game().await?,
                    _ => (),
                }
                return Ok(false);
            }
            Mode::Crashed => {
                self.close_game().await?;
                return Ok(false);
            }
            Mode::Browse => (),
        }
        if self.search_focus {
            match key.code {
                KeyCode::Enter | KeyCode::Esc => {
                    self.search_focus = false;
                    self.selection = 1;
                }
                KeyCode::Backspace => {
                    self.query.pop();
                    self.search();
                }
                KeyCode::Char('u') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                    self.query.clear();
                    self.search();
                }
                KeyCode::Char(c)
                    if !key
                        .modifiers
                        .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT) =>
                {
                    self.query.push(c);
                    self.search();
                }
                _ => (),
            };
            return Ok(false);
        }
        if key.code == KeyCode::Char('n') && key.modifiers.contains(KeyModifiers::CONTROL) {
            self.nav_visible = !self.nav_visible;
            self.focus = if self.nav_visible { 0 } else { 1 };
            return Ok(false);
        }
        match key.code {
            KeyCode::Esc => self.execute(Command::Back).await?,
            KeyCode::Char('q') => return Ok(true),
            KeyCode::Tab => self.focus = (self.focus + 1) % 3,
            KeyCode::BackTab => self.focus = (self.focus + 2) % 3,
            KeyCode::Up | KeyCode::Char('k') => self.move_selection(-1),
            KeyCode::Down | KeyCode::Char('j') => self.move_selection(1),
            KeyCode::PageUp => self.scroll = self.scroll.saturating_sub(10),
            KeyCode::PageDown => self.scroll = self.scroll.saturating_add(10),
            KeyCode::Home => {
                self.selection = 0;
                self.scroll = 0;
            }
            KeyCode::End => {
                self.selection = self.items().len().saturating_sub(1);
            }
            KeyCode::Left if self.focus == 2 => self.move_selection(-1),
            KeyCode::Right if self.focus == 2 => self.move_selection(1),
            KeyCode::Char('/') if self.page == Page::Market => {
                self.search_focus = true;
                self.selection = 0;
                self.focus = 1;
            }
            KeyCode::Char('m') => self.navigate(Page::Market, true),
            KeyCode::Enter => {
                if self.focus == 1 && self.page == Page::Market && self.selection == 0 {
                    self.search_focus = true;
                } else {
                    let items = match self.focus {
                        0 => self.navigation(),
                        2 => self.actions(),
                        _ => self.items(),
                    };
                    let index = match self.focus {
                        0 => self.nav_selection,
                        2 => self.action_selection,
                        _ => self.selection,
                    };
                    if let Some(item) = items.get(index) {
                        self.execute(item.command.clone()).await?;
                    }
                }
            }
            KeyCode::Char('r') => self.execute(Command::Refresh).await?,
            KeyCode::Char('s') => self.navigate(Page::Settings, true),
            KeyCode::Char(c) => {
                let prefix = match c.to_ascii_lowercase() {
                    'p' => "P ",
                    'a' => "A ",
                    'l' => "L ",
                    'u' => "U ",
                    'o' => "O ",
                    'c' => "C ",
                    'b' => "B ",
                    _ => "",
                };
                if !prefix.is_empty()
                    && let Some(item) = self
                        .actions()
                        .into_iter()
                        .find(|i| i.label.starts_with(prefix))
                {
                    self.execute(item.command).await?;
                }
            }
            _ => (),
        }
        Ok(false)
    }
    fn move_selection(&mut self, delta: i32) {
        let (len, index) = match self.focus {
            0 => (self.navigation().len(), &mut self.nav_selection),
            2 => (self.actions().len(), &mut self.action_selection),
            _ => (self.items().len(), &mut self.selection),
        };
        if len > 0 {
            *index = ((*index as i64 + delta as i64).rem_euclid(len as i64)) as usize;
        } else {
            *index = 0;
        }
    }
    async fn form_key(&mut self, key: KeyEvent) -> Result<()> {
        if key.code == KeyCode::Esc {
            let refresh = self.loading;
            self.cancel();
            self.form = None;
            if refresh {
                self.request(Request::Load);
            }
            return Ok(());
        }
        if self.loading || self.width < 40 || self.height < 20 {
            return Ok(());
        }
        let form = self.form.as_mut().expect("form");
        if key.code != KeyCode::Enter || form.index < form.fields.len() {
            form.edit(key);
            return Ok(());
        }
        let button = form.index - form.fields.len();
        if button == form.buttons() - 1 {
            self.form = None;
            return Ok(());
        }
        let values: Vec<String> = form
            .fields
            .iter()
            .map(|f| f.value.trim().to_string())
            .collect();
        let action = match &form.kind {
            FormKind::Username => {
                if values[0].is_empty() {
                    self.notice = "Handle is required".into();
                    return Ok(());
                }
                if button == 1 {
                    Action::CheckUsername(values[0].clone())
                } else {
                    Action::Username(values[0].clone())
                }
            }
            FormKind::Org => {
                if values[0].is_empty() {
                    self.notice = "Org handle is required".into();
                    return Ok(());
                }
                Action::OrgCreate(values[0].clone(), values[1].clone(), values[2].clone())
            }
            FormKind::Member(org) => {
                if !matches!(values[1].as_str(), "admin" | "member") {
                    self.notice = "Role must be admin or member".into();
                    return Ok(());
                }
                Action::MemberSet(org.clone(), values[0].clone(), values[1] == "admin")
            }
            FormKind::Confirm(action, confirmation) => {
                if form.fields[0].value != *confirmation {
                    self.notice = "Confirmation does not match".into();
                    return Ok(());
                }
                action.clone()
            }
        };
        self.execute(Command::Mutate(action)).await?;
        Ok(())
    }
}
fn translate(code: KeyCode) -> Key {
    match code {
        KeyCode::Left | KeyCode::Char('a' | 'A') => Key::Left,
        KeyCode::Right | KeyCode::Char('d' | 'D') => Key::Right,
        KeyCode::Up | KeyCode::Char('w' | 'W') => Key::Up,
        KeyCode::Down | KeyCode::Char('s' | 'S') => Key::Down,
        KeyCode::Char(' ' | 'z' | 'Z') => Key::A,
        KeyCode::Char('x' | 'X') => Key::B,
        KeyCode::Enter => Key::Start,
        _ => Key::None,
    }
}
struct TerminalGuard {
    active: bool,
}
impl TerminalGuard {
    fn enter() -> Result<Self> {
        anyhow::ensure!(
            io::stdin().is_terminal() && io::stdout().is_terminal(),
            "the arcade needs an interactive terminal; use `termcade help` for commands"
        );
        let mut guard = Self { active: true };
        enable_raw_mode()?;
        if let Err(e) = execute!(
            io::stdout(),
            EnterAlternateScreen,
            EnableBracketedPaste,
            EnableFocusChange,
            PushKeyboardEnhancementFlags(
                KeyboardEnhancementFlags::DISAMBIGUATE_ESCAPE_CODES
                    | KeyboardEnhancementFlags::REPORT_EVENT_TYPES
                    | KeyboardEnhancementFlags::REPORT_ALL_KEYS_AS_ESCAPE_CODES
            )
        ) {
            guard.restore();
            return Err(e.into());
        }
        Ok(guard)
    }
    fn restore(&mut self) {
        if self.active {
            let _ = execute!(
                io::stdout(),
                PopKeyboardEnhancementFlags,
                DisableBracketedPaste,
                DisableFocusChange,
                LeaveAlternateScreen,
                crossterm::cursor::Show
            );
            let _ = disable_raw_mode();
            let _ = io::stdout().flush();
            self.active = false;
        }
    }
}
impl Drop for TerminalGuard {
    fn drop(&mut self) {
        self.restore();
    }
}
pub async fn run() -> Result<()> {
    if let Err(e) = crate::local::seed() {
        eprintln!("termcade: starter pack: {e}");
    }
    let scores = Scores::load().unwrap_or_else(|e| {
        eprintln!("termcade: score file unavailable: {e}");
        Scores::empty()
    });
    let saved = Settings::load().unwrap_or_default();
    let shape = std::env::var("TERMCADE_PIXELS")
        .ok()
        .and_then(|name| {
            let shape = CellShape::lookup(&name);
            if shape.is_none() {
                eprintln!("termcade: unknown TERMCADE_PIXELS {name:?}; ignoring");
            }
            shape
        })
        .or_else(|| CellShape::lookup(&saved.pixels))
        .unwrap_or_else(CellShape::quadrant);
    let backend = Backend::new(Runtime::new()?)?;
    let (tx, mut rx) = mpsc::channel(16);
    let mut app = App::new(backend, scores, shape, tx);
    let _guard = TerminalGuard::enter()?;
    let mut terminal = Terminal::new(CrosstermBackend::new(io::stdout()))?;
    terminal.clear()?;
    app.request(Request::Load);
    let mut events = EventStream::new();
    let mut tick = tokio::time::interval(Duration::from_nanos(1_000_000_000 / 60));
    tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        terminal.draw(|frame| {
            app.width = frame.area().width;
            app.height = frame.area().height;
            view::draw(&mut app, frame);
        })?;
        tokio::select! {
            Some(event)=events.next()=>match event?{
                Event::Key(key)=>if app.key(key).await?{break;},
                Event::Resize(_,_)=>terminal.clear()?,
                Event::FocusLost if app.mode==Mode::Playing=>{app.mode=Mode::Paused;app.pause_selection=0;},
                Event::Paste(text)=>{if let Some(form)=&mut app.form{form.text(&text);}else if app.search_focus{let safe=registry::safe_text(&text,256);app.query.push_str(&safe);app.search();}},_=>(),
            },
            Some(message)=rx.recv()=>app.message(message).await?,
            _=tick.tick()=>app.tick().await?,
            _=crate::shutdown()=>break,
        }
    }
    app.close_game().await?;
    terminal.show_cursor()?;
    Ok(())
}
