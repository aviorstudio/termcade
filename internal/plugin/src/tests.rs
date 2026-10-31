use super::*;
use std::{fs, path::PathBuf, time::Instant};
use termcade_manifest::Package;
use termcade_sdk::{BLACK, Status};

fn info() -> Info {
    Info {
        id: "test/probe".into(),
        title: "PROBE".into(),
        pixel_w: 16,
        pixel_h: 8,
    }
}
fn fixture(name: &str) -> PathBuf {
    let target = std::env::var_os("CARGO_TARGET_DIR")
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../target"));
    target
        .join("wasm32-wasip1/release")
        .join(format!("termcade_{name}_fixture.wasm"))
}
#[tokio::test]
async fn rust_sdk_guest_roundtrip_keeps_ticks_keys_pixels_hud_gameover_and_reset() {
    let runtime = Runtime::new().unwrap();
    let wasm = fs::read(fixture("probe")).expect("build the locked Rust WASIp1 probe first");
    let mut game = runtime
        .load("probe", &wasm, info(), CellShape::quadrant())
        .await
        .unwrap();
    let mut canvas = Canvas::new(16, 8, BLACK, CellShape::quadrant());
    game.reset().await.unwrap();
    game.draw(&mut canvas).await.unwrap();
    assert!(canvas.pixels().iter().all(|&p| p == BLACK));
    for _ in 0..5 {
        assert_eq!(game.update().await.unwrap(), Status::Running as i32);
    }
    game.key(Key::A, true).await.unwrap();
    assert_eq!(game.score().await.unwrap(), 10);
    game.draw(&mut canvas).await.unwrap();
    assert_eq!(canvas.pixels().iter().filter(|&&p| p != BLACK).count(), 6);
    game.key(Key::A, false).await.unwrap();
    game.draw(&mut canvas).await.unwrap();
    assert_eq!(canvas.pixels().iter().filter(|&&p| p != BLACK).count(), 5);
    let hud = game.hud().await.unwrap();
    assert_eq!(hud.fields.len(), 1);
    assert_eq!(hud.fields[0].value, "10");
    assert_eq!(hud.hint, "probe");
    for _ in 0..24 {
        assert_eq!(game.update().await.unwrap(), Status::Running as i32);
    }
    assert_eq!(game.update().await.unwrap(), Status::GameOver as i32);
    game.reset().await.unwrap();
    assert_eq!(game.score().await.unwrap(), 0);
    assert_eq!(game.update().await.unwrap(), Status::Running as i32);
}

#[tokio::test]
async fn original_published_go_starters_run_in_the_rust_host_at_every_density() {
    let runtime = Runtime::new().unwrap();
    let packs = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../starter/packs");
    let mut count = 0;
    for entry in fs::read_dir(packs).unwrap() {
        let path = entry.unwrap().path();
        if path.extension().is_none_or(|x| x != "tcade") {
            continue;
        }
        let package = Package::open(&path).unwrap();
        for name in ["half", "quad", "sextant", "ascii"] {
            let shape = CellShape::lookup(name).unwrap();
            let info = package.manifest.info();
            let mut game = runtime
                .load(
                    &package.manifest.game.id,
                    &package.wasm,
                    info.clone(),
                    shape.clone(),
                )
                .await
                .unwrap();
            game.reset().await.unwrap();
            let mut canvas = Canvas::new(info.pixel_w, info.pixel_h, BLACK, shape);
            for _ in 0..3 {
                game.update().await.unwrap();
            }
            game.draw(&mut canvas).await.unwrap();
            game.score().await.unwrap();
            game.hud().await.unwrap();
            assert!(game.error().is_none());
            assert_eq!(canvas.render().lines().count(), info.pixel_h / 2);
        }
        count += 1;
    }
    assert_eq!(count, 3);
}

fn module(imports: &str, update: &str, memory: &str, init: u64) -> Vec<u8> {
    wat::parse_str(format!(
        r#"(module
        {imports}
        (memory (export "memory") {memory})
        (func (export "termcade_abi") (result i32) i32.const 1)
        (func (export "termcade_init") (param i32 i32) (result i64) i64.const {init})
        (func (export "termcade_playfield") (result i64) i64.const 34359738376)
        (func (export "termcade_reset"))
        (func (export "termcade_key") (param i32 i32))
        (func (export "termcade_update") (result i32) {update})
        (func (export "termcade_draw"))
        (func (export "termcade_score") (result i64) i64.const 0)
        (func (export "termcade_hud") (result i64) i64.const 0)
    )"#
    ))
    .unwrap()
}
fn small_info() -> Info {
    Info {
        id: "test/small".into(),
        title: "SMALL".into(),
        pixel_w: 8,
        pixel_h: 8,
    }
}
const PIXELS: u64 = (8192_u64 << 32) | 512;

#[tokio::test]
async fn watchdog_stops_compute_and_blocking_wasi_poll_and_latches_each_failure() {
    let runtime = Runtime::new().unwrap();
    let compute = module("", "(loop $again br $again) i32.const 0", "1 1024", PIXELS);
    let imports = r#"(import "wasi_snapshot_preview1" "poll_oneoff" (func $poll (param i32 i32 i32 i32) (result i32)))"#;
    let update = r#"
        (i32.store (i32.const 16) (i32.const 1))
        (i64.store (i32.const 24) (i64.const 3600000000000))
        (drop (call $poll (i32.const 0) (i32.const 64) (i32.const 1) (i32.const 128)))
        i32.const 0"#;
    let polling = module(imports, update, "1 1024", PIXELS);
    for (key, wasm) in [("compute", compute), ("poll", polling)] {
        let mut game = runtime
            .load(key, &wasm, small_info(), CellShape::quadrant())
            .await
            .unwrap();
        let started = Instant::now();
        assert!(game.update().await.is_err(), "{key} escaped watchdog");
        assert!(
            started.elapsed() < Duration::from_secs(3),
            "{key} stalled the host"
        );
        assert!(game.error().is_some());
        let started = Instant::now();
        assert!(game.reset().await.is_err());
        assert!(started.elapsed() < Duration::from_millis(100));
    }
}

#[tokio::test]
async fn guest_has_no_parent_environment_arguments_or_more_than_64_mib_linear_memory() {
    let runtime = Runtime::new().unwrap();
    let imports = r#"
        (import "wasi_snapshot_preview1" "args_sizes_get" (func $args (param i32 i32) (result i32)))
        (import "wasi_snapshot_preview1" "environ_sizes_get" (func $env (param i32 i32) (result i32)))"#;
    let update = r#"(drop (call $args (i32.const 0) (i32.const 4))) (drop (call $env (i32.const 8) (i32.const 12)))
        (i32.add (i32.load (i32.const 0)) (i32.load (i32.const 8)))"#;
    let mut game = runtime
        .load(
            "empty-context",
            &module(imports, update, "1 2048", PIXELS),
            small_info(),
            CellShape::quadrant(),
        )
        .await
        .unwrap();
    assert_eq!(game.update().await.unwrap(), 0);
    let mut game = runtime
        .load(
            "growth",
            &module("", "(memory.grow (i32.const 1024))", "1 2048", PIXELS),
            small_info(),
            CellShape::quadrant(),
        )
        .await
        .unwrap();
    assert_eq!(game.update().await.unwrap(), -1);
    assert!(
        runtime
            .load(
                "initial-memory",
                &module("", "i32.const 0", "1025 2048", PIXELS),
                small_info(),
                CellShape::quadrant()
            )
            .await
            .is_err()
    );
}

#[tokio::test]
async fn missing_exports_bad_pointers_wrong_playfields_and_unknown_imports_are_refused() {
    let runtime = Runtime::new().unwrap();
    assert!(runtime.compile("garbage", b"not wasm").is_err());
    assert!(
        runtime
            .compile("missing", &wat::parse_str("(module)").unwrap())
            .is_err()
    );
    let valid = module("", "i32.const 0", "1 1024", PIXELS);
    runtime.compile("same-key", &valid).unwrap();
    assert!(
        runtime
            .compile("same-key", b"changed invalid module")
            .is_err()
    );
    assert!(
        runtime
            .load(
                "bad-pointer",
                &module(
                    "",
                    "i32.const 0",
                    "1 1024",
                    (u64::from(u32::MAX) << 32) | 512
                ),
                small_info(),
                CellShape::quadrant()
            )
            .await
            .is_err()
    );
    assert!(
        runtime
            .load("wrong-size", &valid, info(), CellShape::quadrant())
            .await
            .is_err()
    );
    let unknown = module(
        r#"(import "env" "read_host_file" (func))"#,
        "i32.const 0",
        "1 1024",
        PIXELS,
    );
    assert!(
        runtime
            .load(
                "unknown-import",
                &unknown,
                small_info(),
                CellShape::quadrant()
            )
            .await
            .is_err()
    );
}
