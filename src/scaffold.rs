use anyhow::{Context, Result};
use std::{fs, path::Path, process::Stdio};
use termcade_manifest::{Game, Manifest, Package, Requirements};
use termcade_plugin::Runtime;
pub fn new(args: &[String]) -> Result<()> {
    anyhow::ensure!(
        (1..=2).contains(&args.len()),
        "usage: termcade dev new <author/slug> [dir]"
    );
    let id = &args[0];
    let (_, slug) = crate::local::parts(id)?;
    let name = slug
        .split('-')
        .map(|w| {
            let mut chars = w.chars();
            chars
                .next()
                .map(|c| c.to_uppercase().to_string() + chars.as_str())
                .unwrap_or_default()
        })
        .collect::<Vec<_>>()
        .join(" ");
    let m = Manifest {
        game: Game {
            id: id.clone(),
            name,
            version: "0.1.0".into(),
            description: "Describe your game in one line".into(),
        },
        requirements: Requirements {
            abi: 1,
            width: 64,
            height: 40,
        },
        controls: [("←/→".into(), "move paddle".into())].into(),
    };
    m.validate()?;
    let dir = Path::new(args.get(1).map(String::as_str).unwrap_or(slug));
    if dir.exists() {
        anyhow::ensure!(
            fs::read_dir(dir)?.next().is_none(),
            "{} already exists and is not empty",
            dir.display()
        );
    }
    fs::create_dir_all(dir.join("src"))?;
    let sdk_rev = option_env!("COMMIT")
        .filter(|v| v.len() == 40 && v.bytes().all(|b| b.is_ascii_hexdigit()))
        .unwrap_or("601a465c8a1ecaa4a973ba3c1c758f0bb3826066");
    let cargo = format!(
        "[package]\nname = {}\nversion = \"0.1.0\"\nedition = \"2024\"\nrust-version = \"1.94\"\n\n[lib]\ncrate-type = [\"cdylib\"]\n\n[dependencies]\ntermcade-sdk = {{ git = \"https://github.com/aviorstudio/termcade\", rev = \"{sdk_rev}\" }}\n\n[profile.release]\npanic = \"abort\"\n\n[workspace]\n",
        serde_json::to_string(&format!("game-{slug}"))?
    );
    let source = include_str!("scaffold_game.rs.txt")
        .replace("@ID@", id)
        .replace("@TITLE@", &m.info().title);
    for (file, raw) in [
        ("termcade.toml", m.encode()?),
        ("Cargo.toml", cargo.into_bytes()),
        ("src/lib.rs", source.into_bytes()),
        (".gitignore", b"/target/\n/build/\n".to_vec()),
    ] {
        use std::io::Write;
        let mut out = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(dir.join(file))?;
        out.write_all(&raw)?;
    }
    println!(
        "created {}/\n  termcade.toml   identity, playfield, controls\n  src/lib.rs      the game — start here\n  Cargo.toml      Rust SDK and WASI reactor\n\nnext:\n  cd {}\n  rustup target add wasm32-wasip1\n  termcade dev build && termcade dev install build/{slug}.tcade && termcade",
        dir.display(),
        dir.display()
    );
    Ok(())
}
pub async fn build(args: &[String], runtime: &Runtime) -> Result<()> {
    anyhow::ensure!(args.len() <= 1, "usage: termcade dev build [dir]");
    let dir = Path::new(args.first().map(String::as_str).unwrap_or("."));
    let raw = fs::read(dir.join(termcade_manifest::FILE_NAME))
        .context("a game directory needs a termcade.toml")?;
    let m = Manifest::parse(&raw)?;
    anyhow::ensure!(
        dir.join("Cargo.toml").is_file(),
        "a game directory needs a Cargo.toml and a cdylib entrypoint"
    );
    let target = tempfile::tempdir()?;
    let mut command = tokio::process::Command::new("cargo");
    command
        .args([
            "build",
            "--release",
            "--target",
            "wasm32-wasip1",
            "--message-format=json-render-diagnostics",
        ])
        .current_dir(dir)
        .env("CARGO_TARGET_DIR", target.path().join("target"))
        .env("CARGO_TERM_COLOR", "never")
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true);
    #[cfg(unix)]
    command.process_group(0);
    let child = command.spawn().context("starting cargo build")?;
    let mut child = BuildChild::new(child);
    let stdout = child
        .child
        .stdout
        .take()
        .context("cargo stdout unavailable")?;
    let stderr = child
        .child
        .stderr
        .take()
        .context("cargo stderr unavailable")?;
    let operation = async {
        tokio::try_join!(
            async { Ok::<_, anyhow::Error>(child.child.wait().await?) },
            read_bounded(stdout, 16 << 20),
            read_bounded(stderr, 1 << 20)
        )
    };
    let (status, output, errors) =
        tokio::time::timeout(std::time::Duration::from_secs(300), operation)
            .await
            .context("cargo build exceeded five minutes")??;
    child.done = true;
    let log = String::from_utf8(output).context("cargo emitted non-UTF8 metadata")?;
    if !status.success() {
        let diagnostics = log
            .lines()
            .filter_map(|line| serde_json::from_str::<serde_json::Value>(line).ok())
            .filter(|value| value["reason"] == "compiler-message")
            .filter_map(|value| value["message"]["rendered"].as_str().map(str::to_owned))
            .collect::<Vec<_>>()
            .join("\n");
        anyhow::bail!(
            "cargo build failed: {status}\n{}\n{}",
            crate::terminal_text(&diagnostics, 12000),
            crate::terminal_text(&String::from_utf8_lossy(&errors), 4000)
        );
    }
    let artifacts: Vec<_> = log
        .lines()
        .filter_map(|line| serde_json::from_str::<serde_json::Value>(line).ok())
        .filter(|v| v["reason"] == "compiler-artifact")
        .flat_map(|v| v["filenames"].as_array().cloned().unwrap_or_default())
        .filter_map(|v| v.as_str().map(str::to_string))
        .filter(|p| p.ends_with(".wasm"))
        .collect();
    anyhow::ensure!(
        artifacts.len() == 1,
        "a game directory must produce exactly one WASI cdylib"
    );
    let wasm = fs::read(&artifacts[0])?;
    runtime.compile(&m.game.id, &wasm)?;
    let build_dir = dir.join("build");
    fs::create_dir_all(&build_dir)?;
    let path = build_dir.join(format!("{}.tcade", m.slug()));
    Package::write(&path, &raw, &wasm)?;
    println!(
        "built {} {} → {}",
        m.game.id,
        m.game.version,
        path.display()
    );
    Ok(())
}

struct BuildChild {
    child: tokio::process::Child,
    #[cfg(unix)]
    group: Option<u32>,
    done: bool,
}
impl BuildChild {
    fn new(child: tokio::process::Child) -> Self {
        #[cfg(unix)]
        let group = child.id();
        Self {
            child,
            #[cfg(unix)]
            group,
            done: false,
        }
    }
}
impl Drop for BuildChild {
    fn drop(&mut self) {
        if !self.done {
            #[cfg(unix)]
            if let Some(group) = self.group {
                let _ = nix::sys::signal::killpg(
                    nix::unistd::Pid::from_raw(group as i32),
                    nix::sys::signal::Signal::SIGKILL,
                );
            }
            let _ = self.child.start_kill();
        }
    }
}
async fn read_bounded(reader: impl tokio::io::AsyncRead + Unpin, limit: usize) -> Result<Vec<u8>> {
    use tokio::io::AsyncReadExt;
    let mut output = Vec::new();
    reader
        .take(limit as u64 + 1)
        .read_to_end(&mut output)
        .await?;
    anyhow::ensure!(
        output.len() <= limit,
        "cargo build output exceeded the byte limit"
    );
    Ok(output)
}
