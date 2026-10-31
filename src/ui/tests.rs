use super::*;
use ratatui::backend::TestBackend;
fn app() -> (App, tempfile::TempDir, mpsc::Receiver<Message>) {
    let dir = tempfile::tempdir().unwrap();
    let (tx, rx) = mpsc::channel(16);
    let backend = Backend {
        config_error: None,
        runtime: Runtime::new().unwrap(),
        store: registry::TuiStore {
            registry: "http://127.0.0.1:9".into(),
            pairing_url: registry::PAIRING_URI.into(),
            path: dir.path().join("session.json"),
        },
    };
    let scores = Scores::load_at(dir.path().join("scores.json")).unwrap();
    (
        App::new(backend, scores, CellShape::quadrant(), tx),
        dir,
        rx,
    )
}
fn key(code: KeyCode) -> KeyEvent {
    KeyEvent::new(code, KeyModifiers::NONE)
}
#[tokio::test]
async fn canceled_signin_cannot_persist_a_late_credential_or_restore_private_views() {
    let (mut app, _dir, _rx) = app();
    app.generation = 10;
    app.pairing = Some(Pairing {
        code: "ABCD-EFGH".into(),
        url: registry::PAIRING_URI.into(),
        round: None,
        button: 0,
    });
    app.key(key(KeyCode::Esc)).await.unwrap();
    assert!(app.pairing.is_none());
    let session = registry::Session {
        registry: app.backend.store.registry.clone(),
        email: "player@example.org".into(),
        token: format!("tcc_{}", "a".repeat(43)),
        ..Default::default()
    };
    app.message(Message::Reply {
        generation: 10,
        request: Request::Load,
        result: Ok(Reply {
            session: Some(session),
            snapshot: Some(Snapshot {
                account: Some(registry::Me {
                    email: "old@example.org".into(),
                    ..Default::default()
                }),
                ..Default::default()
            }),
            ..Default::default()
        }),
    })
    .await
    .unwrap();
    assert!(!app.backend.store.path.exists());
    assert!(app.snapshot.account.is_none());
}
#[tokio::test]
async fn only_latest_search_results_can_replace_the_catalog() {
    let (mut app, _dir, _rx) = app();
    app.search_gen = 7;
    app.message(Message::Search {
        generation: 6,
        result: Ok(Reply {
            catalog: Some(vec![Game {
                id: "old/game".into(),
                ..Default::default()
            }]),
            ..Default::default()
        }),
    })
    .await
    .unwrap();
    assert!(app.search.is_none());
    app.message(Message::Search {
        generation: 7,
        result: Ok(Reply {
            catalog: Some(vec![Game {
                id: "new/game".into(),
                ..Default::default()
            }]),
            ..Default::default()
        }),
    })
    .await
    .unwrap();
    assert_eq!(app.search.as_ref().unwrap()[0].id, "new/game");
}
#[tokio::test]
async fn destructive_forms_require_the_full_confirmation_and_keep_editing_unicode_safe() {
    let (mut app, _dir, _rx) = app();
    app.execute(Command::Confirm(
        "Delete account".into(),
        Action::DeleteAccount,
        "player@example.org".into(),
    ))
    .await
    .unwrap();
    app.form.as_mut().unwrap().text("player");
    app.key(key(KeyCode::Tab)).await.unwrap();
    app.key(key(KeyCode::Enter)).await.unwrap();
    assert_eq!(app.notice, "Confirmation does not match");
    assert!(!app.loading);
    assert!(app.form.is_some());
    let mut form = Form::new(
        "Unicode",
        FormKind::Username,
        vec![("Handle", "雪a🦀".into())],
    );
    form.edit(key(KeyCode::Left));
    form.edit(key(KeyCode::Backspace));
    form.text("é");
    assert_eq!(form.fields[0].value, "雪é🦀");
    form.edit(key(KeyCode::Home));
    form.edit(key(KeyCode::Delete));
    assert_eq!(form.fields[0].value, "é🦀");
}
#[tokio::test]
async fn narrow_terminal_does_not_activate_hidden_pairing_controls() {
    let (mut app, _dir, _rx) = app();
    app.width = 30;
    app.height = 10;
    app.pairing = Some(Pairing {
        code: "ABCD-EFGH".into(),
        url: "file:///forbidden".into(),
        round: None,
        button: 0,
    });
    assert!(!app.key(key(KeyCode::Enter)).await.unwrap());
    assert!(app.job.is_none());
    assert!(app.pairing.is_some());
    app.key(key(KeyCode::Esc)).await.unwrap();
    assert!(app.pairing.is_none());
}
#[tokio::test]
async fn library_and_game_actions_keep_account_removal_separate_from_local_uninstall() {
    let (mut app, _dir, _rx) = app();
    app.page = Page::Game("author/game".into());
    let m = Manifest {
        game: termcade_manifest::Game {
            id: "author/game".into(),
            name: "Game".into(),
            version: "1.0".into(),
            ..Default::default()
        },
        requirements: termcade_manifest::Requirements {
            abi: 1,
            width: 16,
            height: 8,
        },
        ..Default::default()
    };
    app.snapshot.installed.push(Installed {
        id: "author/game".into(),
        title: "GAME".into(),
        manifest: Some(m),
        dir: std::path::PathBuf::from("/unused"),
        error: String::new(),
    });
    app.snapshot.account = Some(registry::Me {
        email: "p@example.org".into(),
        ..Default::default()
    });
    app.snapshot.library.push(Game {
        id: "author/game".into(),
        ..Default::default()
    });
    let actions = app.actions();
    assert!(
        actions
            .iter()
            .any(|i| matches!(&i.command,Command::Mutate(Action::Remove(id))if id=="author/game"))
    );
    assert!(actions.iter().any(|i|matches!(&i.command,Command::Confirm(_,Action::Uninstall(id),confirmation)if id=="author/game"&&confirmation==id)));
    app.clear_private();
    assert!(app.snapshot.library.is_empty());
    assert_eq!(app.snapshot.installed.len(), 1);
    assert!(
        app.actions()
            .iter()
            .any(|i| matches!(&i.command, Command::Start(_)))
    );
}
#[test]
fn every_page_and_modal_renders_within_small_and_wide_terminal_bounds() {
    let (mut app, _dir, _rx) = app();
    app.snapshot.catalog.push(Game {
        id: "author/game".into(),
        name: "Snow 雪 🦀".into(),
        description: "\x1b[31munsafe\ntext".into(),
        ..Default::default()
    });
    for (width, height) in [(10, 5), (30, 10), (40, 20), (80, 24), (140, 50)] {
        for page in [
            Page::Market,
            Page::Library,
            Page::Game("author/game".into()),
            Page::Owner("author".into()),
            Page::Activity("author/game".into()),
            Page::Settings,
            Page::Members("studio".into()),
            Page::Sessions,
            Page::Docs,
        ] {
            app.page = page;
            app.width = width;
            app.height = height;
            let mut terminal = Terminal::new(TestBackend::new(width, height)).unwrap();
            terminal.draw(|frame| view::draw(&mut app, frame)).unwrap();
        }
        app.form = Some(Form::new(
            "Confirm",
            FormKind::Username,
            vec![("Unicode", "雪".repeat(200))],
        ));
        let mut terminal = Terminal::new(TestBackend::new(width, height)).unwrap();
        terminal.draw(|frame| view::draw(&mut app, frame)).unwrap();
        app.form = None;
        app.pairing = Some(Pairing {
            code: "ABCD-EFGH".into(),
            url: registry::PAIRING_URI.into(),
            round: None,
            button: 0,
        });
        terminal.draw(|frame| view::draw(&mut app, frame)).unwrap();
        app.pairing = None;
    }
}

#[tokio::test]
async fn rust_guest_plays_pauses_finishes_and_queues_a_run_once() {
    let (mut app, dir, mut rx) = app();
    let target = std::env::var_os("CARGO_TARGET_DIR")
        .map(std::path::PathBuf::from)
        .unwrap_or_else(|| std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("target"));
    let fixture = target.join("wasm32-wasip1/release/termcade_probe_fixture.wasm");
    std::fs::copy(fixture, dir.path().join("game.wasm"))
        .expect("build the independent Rust guest first");
    let manifest = Manifest {
        game: termcade_manifest::Game {
            id: "test/probe".into(),
            name: "Probe".into(),
            version: "0.1.0".into(),
            ..Default::default()
        },
        requirements: termcade_manifest::Requirements {
            abi: 1,
            width: 16,
            height: 8,
        },
        ..Default::default()
    };
    app.snapshot.installed.push(Installed {
        id: "test/probe".into(),
        title: "PROBE".into(),
        manifest: Some(manifest),
        dir: dir.path().to_path_buf(),
        error: String::new(),
    });
    app.begin_play("test/probe".into());
    let message = tokio::time::timeout(Duration::from_secs(10), rx.recv())
        .await
        .unwrap()
        .unwrap();
    app.message(message).await.unwrap();
    assert!(app.mode == Mode::Playing, "{}", app.notice);
    app.key(key(KeyCode::Char('z'))).await.unwrap();
    assert_eq!(app.game.as_mut().unwrap().guest.score().await.unwrap(), 10);
    app.key(key(KeyCode::Esc)).await.unwrap();
    assert!(app.mode == Mode::Paused);
    app.tick().await.unwrap();
    assert_eq!(app.game.as_mut().unwrap().guest.score().await.unwrap(), 10);
    app.key(key(KeyCode::Esc)).await.unwrap();
    assert!(app.mode == Mode::Playing);
    for _ in 0..30 {
        app.last_tick = Instant::now() - Duration::from_nanos(1_000_000_000 / 60);
        app.tick().await.unwrap();
    }
    assert!(app.mode == Mode::GameOver);
    assert_eq!(app.scores.high("test/probe"), 10);
    let score_path = dir.path().join("scores.json");
    let data: serde_json::Value =
        serde_json::from_slice(&std::fs::read(&score_path).unwrap()).unwrap();
    assert_eq!(data["pending"].as_array().unwrap().len(), 1);
    assert_eq!(data["pending"][0]["completed"], true);
    app.close_game().await.unwrap();
    let data: serde_json::Value =
        serde_json::from_slice(&std::fs::read(score_path).unwrap()).unwrap();
    assert_eq!(data["pending"].as_array().unwrap().len(), 1);
}
