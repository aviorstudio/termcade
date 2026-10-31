//! The manifest, never the guest module, is authoritative for game identity.
#[cfg(test)]
mod tests;
use serde::{Deserialize, Serialize};
use std::{
    collections::BTreeMap,
    fs,
    io::{Cursor, Read, Write},
    path::{Path, PathBuf},
};
pub const FILE_NAME: &str = "termcade.toml";
pub const WASM_NAME: &str = "game.wasm";
pub const MAX_WASM_BYTES: usize = 64 << 20;
pub const MAX_MANIFEST_BYTES: usize = 1 << 20;
// Local packages can contain an uncompressible maximum-size module plus a
// maximum-size manifest. Network downloads keep their separate 64 MiB ceiling.
pub const MAX_PACKAGE_BYTES: usize = MAX_WASM_BYTES + MAX_MANIFEST_BYTES + (1 << 20);

#[derive(Debug, thiserror::Error)]
#[error("{0}")]
pub struct Error(String);
pub type Result<T> = std::result::Result<T, Error>;
fn error(message: impl Into<String>) -> Error {
    Error(message.into())
}
impl From<std::io::Error> for Error {
    fn from(e: std::io::Error) -> Self {
        Self(e.to_string())
    }
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Manifest {
    pub game: Game,
    pub requirements: Requirements,
    pub controls: BTreeMap<String, String>,
}
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Game {
    pub id: String,
    pub name: String,
    pub version: String,
    pub description: String,
}
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Requirements {
    pub abi: i32,
    pub width: i32,
    pub height: i32,
}

fn id_part(value: &str) -> bool {
    let mut bytes = value.bytes();
    bytes
        .next()
        .is_some_and(|c| c.is_ascii_lowercase() || c.is_ascii_digit())
        && bytes.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'-')
}
pub fn valid_id(value: &str) -> bool {
    value.len() <= 64
        && value
            .split_once('/')
            .is_some_and(|(a, b)| id_part(a) && id_part(b))
}
fn text(name: &str, value: &str, max: usize) -> Result<()> {
    if value.chars().count() > max {
        return Err(error(format!("{name} exceeds {max} characters")));
    }
    if value.chars().any(char::is_control) {
        return Err(error(format!("{name} contains control characters")));
    }
    Ok(())
}
impl Manifest {
    pub fn parse(raw: &[u8]) -> Result<Self> {
        if raw.len() > MAX_MANIFEST_BYTES {
            return Err(error("termcade.toml exceeds the byte limit"));
        }
        let input = std::str::from_utf8(raw).map_err(|_| error("termcade.toml must be UTF-8"))?;
        let value: Self =
            toml::from_str(input).map_err(|e| error(format!("termcade.toml: {e}")))?;
        value
            .validate()
            .map_err(|e| error(format!("termcade.toml: {e}")))?;
        Ok(value)
    }
    pub fn validate(&self) -> Result<()> {
        let g = &self.game;
        let r = &self.requirements;
        if g.id.is_empty() {
            return Err(error("game.id is required"));
        }
        if g.id.len() > 64 {
            return Err(error("game.id exceeds 64 characters"));
        }
        if !valid_id(&g.id) {
            return Err(error(format!(
                "game.id {:?} must be author/slug, lowercase [a-z0-9-]",
                g.id
            )));
        }
        if g.name.is_empty() {
            return Err(error("game.name is required"));
        }
        if g.version.is_empty() {
            return Err(error("game.version is required"));
        }
        if r.abi < 1 {
            return Err(error("requirements.abi is required"));
        }
        if r.width < 1 || r.height < 1 {
            return Err(error("requirements.width/height are required"));
        }
        if r.width > 200 || r.height > 120 {
            return Err(error("playfield exceeds the 200x120 maximum"));
        }
        if r.height % 2 != 0 {
            return Err(error(
                "requirements.height must be even (a cell spans two units)",
            ));
        }
        if self.controls.len() > 12 {
            return Err(error("more than 12 controls entries"));
        }
        text("game.name", &g.name, 32)?;
        text("game.version", &g.version, 32)?;
        text("game.description", &g.description, 200)?;
        for (key, value) in &self.controls {
            text("controls key", key, 48)?;
            text("controls value", value, 48)?;
        }
        Ok(())
    }
    pub fn author(&self) -> &str {
        self.game.id.split_once('/').expect("validated manifest").0
    }
    pub fn slug(&self) -> &str {
        self.game.id.split_once('/').expect("validated manifest").1
    }
    pub fn compatible_abi(&self) -> bool {
        self.requirements.abi == termcade_sdk::ABI_VERSION
    }
    pub fn info(&self) -> termcade_sdk::Info {
        termcade_sdk::Info {
            id: self.game.id.clone(),
            title: self.game.name.to_uppercase(),
            pixel_w: self.requirements.width as usize,
            pixel_h: self.requirements.height as usize,
        }
    }
    pub fn encode(&self) -> Result<Vec<u8>> {
        self.validate()?;
        Ok(toml::to_string(self)
            .map_err(|e| error(e.to_string()))?
            .into_bytes())
    }
}

pub struct Package {
    pub manifest: Manifest,
    pub wasm: Vec<u8>,
}
impl Package {
    pub fn open(path: &Path) -> Result<Self> {
        if fs::metadata(path)?.len() > MAX_PACKAGE_BYTES as u64 {
            return Err(error("package exceeds the byte limit"));
        }
        Self::read(&fs::read(path)?)
    }
    pub fn read(raw: &[u8]) -> Result<Self> {
        if raw.len() > MAX_PACKAGE_BYTES {
            return Err(error("package exceeds the byte limit"));
        }
        check_directory_count(raw)?;
        let mut archive = zip::ZipArchive::new(Cursor::new(raw))
            .map_err(|e| error(format!("opening package: {e}")))?;
        if archive.len() != 2 || archive.has_duplicate_names() {
            return Err(error("duplicate or unexpected package entries"));
        }
        let (mut manifest, mut wasm) = (None, None);
        for index in 0..archive.len() {
            let metadata = archive
                .by_index_data(index)
                .map_err(|e| error(e.to_string()))?;
            let name = metadata
                .name()
                .map_err(|e| error(e.to_string()))?
                .into_owned();
            if name != FILE_NAME && name != WASM_NAME {
                return Err(error(format!(
                    "unexpected or nested package entry {name:?}"
                )));
            }
            if metadata.encrypted()
                || !metadata.is_file()
                || metadata
                    .unix_mode()
                    .is_some_and(|m| !matches!(m & 0o170000, 0 | 0o100000))
            {
                return Err(error(format!(
                    "package entry {name:?} must be an unencrypted regular file"
                )));
            }
            let limit = if name == FILE_NAME {
                MAX_MANIFEST_BYTES
            } else {
                MAX_WASM_BYTES
            };
            if metadata.size() > limit as u64 {
                return Err(error(format!("{name} exceeds the {limit} byte limit")));
            }
            let mut data = Vec::new();
            archive
                .by_index(index)
                .map_err(|e| error(e.to_string()))?
                .take(limit as u64 + 1)
                .read_to_end(&mut data)?;
            if data.len() > limit {
                return Err(error(format!("{name} exceeds the {limit} byte limit")));
            }
            if name == FILE_NAME {
                manifest = Some(Manifest::parse(&data)?);
            } else {
                if !data.starts_with(b"\0asm") {
                    return Err(error("game.wasm is not a wasm module"));
                }
                wasm = Some(data);
            }
        }
        Ok(Self {
            manifest: manifest.ok_or_else(|| error("package has no termcade.toml at its root"))?,
            wasm: wasm.ok_or_else(|| error("package has no game.wasm at its root"))?,
        })
    }
    pub fn write(path: &Path, manifest: &[u8], wasm: &[u8]) -> Result<()> {
        let mut archive = zip::ZipWriter::new(Cursor::new(Vec::new()));
        let options = zip::write::SimpleFileOptions::default()
            .compression_method(zip::CompressionMethod::Deflated)
            .unix_permissions(0o644);
        for (name, bytes) in [(FILE_NAME, manifest), (WASM_NAME, wasm)] {
            archive
                .start_file(name, options)
                .map_err(|e| error(e.to_string()))?;
            archive.write_all(bytes)?;
        }
        fs::write(
            path,
            archive
                .finish()
                .map_err(|e| error(e.to_string()))?
                .into_inner(),
        )?;
        Ok(())
    }
    pub fn install(&self, games: &Path) -> Result<PathBuf> {
        self.manifest.validate()?;
        fs::create_dir_all(games)?;
        let games = fs::canonicalize(games)?;
        let author = games.join(self.manifest.author());
        if author.exists() {
            let metadata = fs::symlink_metadata(&author)?;
            if metadata.file_type().is_symlink() || !metadata.is_dir() {
                return Err(error("game author directory must be a real directory"));
            }
        } else {
            fs::create_dir(&author)?;
        }
        let destination = author.join(self.manifest.slug());
        if destination.is_symlink() {
            return Err(error("linked game destination refused"));
        }
        let stage = tempfile::Builder::new()
            .prefix(&format!(".{}-", self.manifest.slug()))
            .tempdir_in(&author)?;
        fs::write(stage.path().join(FILE_NAME), self.manifest.encode()?)?;
        fs::write(stage.path().join(WASM_NAME), &self.wasm)?;
        if destination.exists() {
            let backup = tempfile::Builder::new()
                .prefix(".previous-")
                .tempdir_in(&author)?;
            let previous = backup.path().join("game");
            fs::rename(&destination, &previous)?;
            if let Err(e) = fs::rename(stage.path(), &destination) {
                // Retain the previous installation if activation cannot complete.
                if fs::rename(&previous, &destination).is_err() {
                    let retained = backup.keep();
                    return Err(error(format!(
                        "game activation failed; prior files retained at {}: {e}",
                        retained.display()
                    )));
                }
                return Err(e.into());
            }
        } else {
            fs::rename(stage.path(), &destination)?;
        }
        Ok(destination)
    }
}

// Bound central-directory allocation before the ZIP reader constructs it.
fn check_directory_count(raw: &[u8]) -> Result<()> {
    let start = raw.len().saturating_sub(65535 + 22);
    let end = (start..raw.len().saturating_sub(21))
        .rev()
        .find(|&i| {
            raw.get(i..i + 4) == Some(b"PK\x05\x06")
                && raw.len() == i + 22 + u16::from_le_bytes([raw[i + 20], raw[i + 21]]) as usize
        })
        .ok_or_else(|| error("invalid package ZIP footer"))?;
    let count = u16::from_le_bytes([raw[end + 10], raw[end + 11]]);
    if count == 2 && u16::from_le_bytes([raw[end + 8], raw[end + 9]]) == 2 {
        return Ok(());
    }
    if count == u16::MAX && end >= 20 && raw.get(end - 20..end - 16) == Some(b"PK\x06\x07") {
        let offset = u64::from_le_bytes(raw[end - 12..end - 4].try_into().expect("ZIP64 offset"));
        let offset = usize::try_from(offset).map_err(|_| error("invalid ZIP64 offset"))?;
        if let Some(record) = raw.get(offset..offset.saturating_add(56))
            && &record[..4] == b"PK\x06\x06"
            && u64::from_le_bytes(record[24..32].try_into().expect("ZIP64 count")) == 2
            && u64::from_le_bytes(record[32..40].try_into().expect("ZIP64 count")) == 2
        {
            return Ok(());
        }
    }
    Err(error(
        "a .tcade contains exactly termcade.toml and game.wasm",
    ))
}
