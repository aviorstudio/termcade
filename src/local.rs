use anyhow::{Context, Result, bail};
use std::{
    collections::BTreeSet,
    fs,
    path::{Path, PathBuf},
};
use termcade_manifest::{Manifest, Package};
use termcade_plugin::Runtime;

pub fn games_dir() -> Result<PathBuf> {
    if let Some(path) = std::env::var_os("XDG_DATA_HOME").filter(|p| !p.is_empty()) {
        return Ok(PathBuf::from(path).join("termcade/games"));
    }
    let home = std::env::var_os(if cfg!(windows) { "USERPROFILE" } else { "HOME" })
        .context("resolving data dir: home directory unavailable")?;
    let home = PathBuf::from(home);
    let base = if cfg!(target_os = "macos") {
        home.join("Library/Application Support")
    } else if cfg!(windows) {
        std::env::var_os("AppData")
            .map(PathBuf::from)
            .unwrap_or_else(|| home.join("AppData/Roaming"))
    } else {
        home.join(".local/share")
    };
    Ok(base.join("termcade/games"))
}
#[derive(Clone)]
pub struct Installed {
    pub id: String,
    pub title: String,
    pub manifest: Option<Manifest>,
    pub dir: PathBuf,
    pub error: String,
}
impl Installed {
    pub fn version(&self) -> &str {
        self.manifest
            .as_ref()
            .map(|m| m.game.version.as_str())
            .unwrap_or_default()
    }
}
pub fn discover() -> Result<Vec<Installed>> {
    discover_at(&games_dir()?)
}
pub fn discover_at(base: &Path) -> Result<Vec<Installed>> {
    let mut games = Vec::new();
    if !base.exists() {
        return Ok(games);
    }
    for author in fs::read_dir(base)?
        .flatten()
        .filter(|e| e.file_type().is_ok_and(|t| t.is_dir()))
    {
        for slug in fs::read_dir(author.path())?
            .flatten()
            .filter(|e| e.file_type().is_ok_and(|t| t.is_dir()))
        {
            let dir = slug.path();
            if !dir.join(termcade_manifest::FILE_NAME).exists() {
                continue;
            }
            let id = format!(
                "{}/{}",
                author.file_name().to_string_lossy(),
                slug.file_name().to_string_lossy()
            );
            let result = (|| -> Result<Manifest> {
                let m = Manifest::parse(&fs::read(dir.join(termcade_manifest::FILE_NAME))?)?;
                anyhow::ensure!(
                    m.game.id == id,
                    "installed directory does not match the game's identity"
                );
                anyhow::ensure!(
                    m.compatible_abi(),
                    "needs ABI v{}; this termcade speaks v1",
                    m.requirements.abi
                );
                anyhow::ensure!(
                    dir.join(termcade_manifest::WASM_NAME).is_file(),
                    "missing game.wasm"
                );
                Ok(m)
            })();
            let (manifest, title, error) = match result {
                Ok(m) => {
                    let title = m.info().title;
                    (Some(m), title, String::new())
                }
                Err(e) => (None, id.clone(), e.to_string()),
            };
            games.push(Installed {
                id,
                title,
                manifest,
                dir,
                error,
            });
        }
    }
    games.sort_by(|a, b| (!a.error.is_empty(), &a.title).cmp(&(!b.error.is_empty(), &b.title)));
    Ok(games)
}
pub fn install(raw: &[u8], runtime: &Runtime) -> Result<(Manifest, PathBuf)> {
    let pkg = Package::read(raw)?;
    anyhow::ensure!(
        pkg.manifest.compatible_abi(),
        "{} needs ABI v{}; this termcade speaks v1",
        pkg.manifest.game.id,
        pkg.manifest.requirements.abi
    );
    runtime.compile(&pkg.manifest.game.id, &pkg.wasm)?;
    let path = pkg.install(&games_dir()?)?;
    Ok((pkg.manifest, path))
}
pub fn parts(id: &str) -> Result<(&str, &str)> {
    anyhow::ensure!(
        termcade_manifest::valid_id(id),
        "id must look like author/slug"
    );
    Ok(id.split_once('/').expect("validated id"))
}
pub fn remove(id: &str) -> Result<()> {
    remove_at(&games_dir()?, id)
}
fn remove_at(base: &Path, id: &str) -> Result<()> {
    let (author, slug) = parts(id)?;
    let author_path = base.join(author);
    let dir = author_path.join(slug);
    for path in [&author_path, &dir] {
        if fs::symlink_metadata(path).is_ok_and(|m| m.file_type().is_symlink()) {
            bail!("refusing a symlinked game directory");
        }
    }
    anyhow::ensure!(
        dir.join(termcade_manifest::FILE_NAME).is_file(),
        "{id} is not in your arcade"
    );
    fs::remove_dir_all(dir)?;
    Ok(())
}
pub fn seed() -> Result<()> {
    seed_at(&games_dir()?)
}
pub fn seed_at(base: &Path) -> Result<()> {
    let marker = base.join(".starter");
    let mut seeded: BTreeSet<String> = match fs::read_to_string(&marker) {
        Ok(raw) => raw
            .lines()
            .map(str::trim)
            .filter(|s| !s.is_empty())
            .map(str::to_string)
            .collect(),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
            if base.join(".starter-v1").exists() {
                ["aviorstudio/asteroid".into(), "aviorstudio/tetris".into()].into()
            } else {
                BTreeSet::new()
            }
        }
        Err(e) => return Err(e.into()),
    };
    fs::create_dir_all(base)?;
    let mut changed = false;
    for raw in [
        include_bytes!("../internal/starter/packs/asteroid.tcade").as_slice(),
        include_bytes!("../internal/starter/packs/tetris.tcade").as_slice(),
        include_bytes!("../internal/starter/packs/brickough.tcade").as_slice(),
    ] {
        let pkg = Package::read(raw)?;
        if seeded.contains(&pkg.manifest.game.id) {
            continue;
        }
        pkg.install(base)?;
        seeded.insert(pkg.manifest.game.id);
        changed = true;
    }
    if changed {
        crate::scores::atomic_write(
            &marker,
            (seeded.into_iter().collect::<Vec<_>>().join("\n") + "\n").as_bytes(),
        )?;
        match fs::remove_file(base.join(".starter-v1")) {
            Ok(()) => (),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => (),
            Err(e) => return Err(e.into()),
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn starter_markers_do_not_resurrect_removed_games_and_migrate_legacy_offers() {
        let dir = tempfile::tempdir().unwrap();
        seed_at(dir.path()).unwrap();
        let games = discover_at(dir.path()).unwrap();
        assert_eq!(games.len(), 3);
        remove_at(dir.path(), "aviorstudio/asteroid").unwrap();
        seed_at(dir.path()).unwrap();
        assert_eq!(discover_at(dir.path()).unwrap().len(), 2);
        let legacy = tempfile::tempdir().unwrap();
        fs::write(legacy.path().join(".starter-v1"), b"").unwrap();
        seed_at(legacy.path()).unwrap();
        let games = discover_at(legacy.path()).unwrap();
        assert_eq!(games.len(), 1);
        assert_eq!(games[0].id, "aviorstudio/brickough");
        assert!(!legacy.path().join(".starter-v1").exists());
    }
    #[cfg(unix)]
    #[test]
    fn uninstall_refuses_path_traversal_and_symlinked_authors() {
        let dir = tempfile::tempdir().unwrap();
        let external = tempfile::tempdir().unwrap();
        fs::create_dir(external.path().join("game")).unwrap();
        fs::write(external.path().join("game/termcade.toml"), b"keep").unwrap();
        std::os::unix::fs::symlink(external.path(), dir.path().join("author")).unwrap();
        for id in [
            "author/game",
            "../game",
            "a/../../outside",
            "/root",
            "a/b/c",
        ] {
            assert!(remove_at(dir.path(), id).is_err(), "accepted {id}");
        }
        assert_eq!(
            fs::read(external.path().join("game/termcade.toml")).unwrap(),
            b"keep"
        );
    }
}
