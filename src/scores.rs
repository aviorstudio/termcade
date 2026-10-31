use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::{
    collections::BTreeMap,
    fs,
    io::Write,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
};
use termcade_registry::{Activity, Client, Error, Run};
use time::{OffsetDateTime, format_description::well_known::Rfc3339};
fn now() -> String {
    OffsetDateTime::now_utc()
        .format(&Rfc3339)
        .expect("RFC3339 UTC")
}
fn null_default<'de, D: serde::Deserializer<'de>, T: Deserialize<'de> + Default>(
    d: D,
) -> std::result::Result<T, D::Error> {
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}
#[derive(Clone, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct Entry {
    pub high_score: i64,
    pub updated_at: String,
    pub last_played: String,
    pub version: String,
}
#[derive(Clone, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct Pending {
    pub id: String,
    pub game: String,
    pub score: i64,
    pub played_at: String,
    pub completed: bool,
    pub version: String,
}
#[derive(Default, Deserialize, Serialize)]
#[serde(default)]
pub struct Data {
    pub version: i32,
    #[serde(deserialize_with = "null_default")]
    pub games: BTreeMap<String, Entry>,
    #[serde(deserialize_with = "null_default")]
    pub pending: Vec<Pending>,
}
#[derive(Clone)]
pub struct Scores {
    path: Option<PathBuf>,
    data: Arc<Mutex<Data>>,
}
impl Scores {
    pub fn empty() -> Self {
        Self {
            path: termcade_registry::config_dir()
                .ok()
                .map(|p| p.join("scores.json")),
            data: Arc::new(Mutex::new(Data {
                version: 3,
                ..Data::default()
            })),
        }
    }
    pub fn load() -> Result<Self> {
        Self::load_at(termcade_registry::config_dir()?.join("scores.json"))
    }
    pub fn load_at(path: PathBuf) -> Result<Self> {
        let mut data: Data = match fs::read(&path) {
            Ok(raw) => {
                anyhow::ensure!(raw.len() <= 8 << 20, "score file exceeds the byte limit");
                serde_json::from_slice(&raw)
                    .with_context(|| format!("parsing {}", path.display()))?
            }
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Data::default(),
            Err(e) => return Err(e.into()),
        };
        if data.version <= 1 {
            data.games = data
                .games
                .into_iter()
                .map(|(key, value)| {
                    (
                        if key.contains('/') {
                            key
                        } else {
                            format!("aviorstudio/{key}")
                        },
                        value,
                    )
                })
                .collect();
        }
        data.version = 3;
        Ok(Self {
            path: Some(path),
            data: Arc::new(Mutex::new(data)),
        })
    }
    pub fn high(&self, id: &str) -> i64 {
        self.data
            .lock()
            .expect("score lock")
            .games
            .get(id)
            .map(|e| e.high_score)
            .unwrap_or(0)
    }
    pub fn touch(&mut self, id: &str) {
        self.data
            .lock()
            .expect("score lock")
            .games
            .entry(id.into())
            .or_default()
            .last_played = now();
    }
    pub fn record(&mut self, id: &str, score: i64, version: &str, completed: bool) -> bool {
        let mut data = self.data.lock().expect("score lock");
        let stamp = now();
        let entry = data.games.entry(id.into()).or_default();
        let new_high = score > entry.high_score;
        if new_high {
            entry.high_score = score;
            entry.updated_at = stamp.clone();
        }
        entry.last_played = stamp.clone();
        if !version.is_empty() {
            entry.version = version.into();
        }
        let id_run = uuid::Uuid::new_v4().simple().to_string();
        data.pending.push(Pending {
            id: id_run,
            game: id.into(),
            score,
            played_at: stamp,
            completed,
            version: version.into(),
        });
        let excess = data.pending.len().saturating_sub(500);
        data.pending.drain(..excess);
        new_high
    }
    pub fn merge(&mut self, activity: &Activity) {
        let mut data = self.data.lock().expect("score lock");
        let entry = data.games.entry(activity.id.clone()).or_default();
        if activity.personal_best > entry.high_score {
            entry.high_score = activity.personal_best;
            entry.updated_at = now();
        }
        if let Ok(remote) = OffsetDateTime::parse(&activity.last_played, &Rfc3339) {
            let local = OffsetDateTime::parse(&entry.last_played, &Rfc3339)
                .unwrap_or(OffsetDateTime::UNIX_EPOCH);
            if remote > local {
                entry.last_played = activity.last_played.clone();
            }
        }
    }
    pub fn save(&self) -> Result<()> {
        atomic_write(
            self.path
                .as_ref()
                .context("scores: no config path available")?,
            &serde_json::to_vec_pretty(&*self.data.lock().expect("score lock"))?,
        )
    }
    fn ack(&self, id: &str) {
        self.data
            .lock()
            .expect("score lock")
            .pending
            .retain(|run| run.id != id);
    }
    pub async fn sync(&mut self, client: &Client) -> Result<usize> {
        let mut sent = 0;
        loop {
            let run = {
                self.data
                    .lock()
                    .expect("score lock")
                    .pending
                    .first()
                    .cloned()
            };
            let Some(run) = run else {
                break;
            };
            let Ok((author, slug)) = crate::local::parts(&run.game) else {
                self.ack(&run.id);
                self.save()?;
                continue;
            };
            let body = Run {
                run: run.id.clone(),
                score: run.score,
                played_at: run.played_at,
                completed: run.completed,
                version: run.version,
            };
            let result = client
                .json::<Activity>(
                    reqwest::Method::POST,
                    &format!("/v1/activity/{author}/{slug}"),
                    Some(serde_json::to_value(body)?),
                )
                .await;
            match result {
                Ok(_) => sent += 1,
                Err(Error::Response { status: 404, .. }) => (),
                Err(e) => return Err(e.into()),
            }
            self.ack(&run.id);
            self.save()?;
        }
        for activity in client.activity().await? {
            self.merge(&activity);
        }
        self.save()?;
        Ok(sent)
    }
}
pub fn atomic_write(path: &Path, raw: &[u8]) -> Result<()> {
    let parent = path.parent().context("file has no parent")?;
    fs::create_dir_all(parent)?;
    let mut temp = tempfile::NamedTempFile::new_in(parent)?;
    temp.write_all(raw)?;
    temp.as_file().sync_all()?;
    temp.persist(path)?;
    Ok(())
}
#[derive(Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Settings {
    pub pixels: String,
}
impl Settings {
    pub fn load() -> Result<Self> {
        let path = termcade_registry::config_dir()?.join("settings.json");
        match fs::read(path) {
            Ok(raw) => Ok(serde_json::from_slice(&raw)?),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(Self::default()),
            Err(e) => Err(e.into()),
        }
    }
    pub fn save(&self) -> Result<()> {
        atomic_write(
            &termcade_registry::config_dir()?.join("settings.json"),
            &serde_json::to_vec_pretty(self)?,
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn legacy_scores_migrate_without_losing_last_played_or_lowering_highs() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("scores.json");
        fs::write(&path,br#"{"version":1,"games":{"asteroid":{"high_score":100,"updated_at":"2026-01-01T00:00:00Z","last_played":"2026-10-30T00:00:00Z","version":"old"}},"pending":null}"#).unwrap();
        let mut scores = Scores::load_at(path.clone()).unwrap();
        assert_eq!(scores.high("aviorstudio/asteroid"), 100);
        scores.merge(&Activity {
            id: "aviorstudio/asteroid".into(),
            personal_best: 50,
            last_played: "2026-10-01T00:00:00Z".into(),
            last_version: "remote".into(),
            ..Default::default()
        });
        scores.save().unwrap();
        let data: Data = serde_json::from_slice(&fs::read(path).unwrap()).unwrap();
        let entry = &data.games["aviorstudio/asteroid"];
        assert_eq!(entry.high_score, 100);
        assert_eq!(entry.version, "old");
        assert_eq!(entry.last_played, "2026-10-30T00:00:00Z");
        assert_eq!(data.version, 3);
    }
    #[test]
    fn offline_queue_caps_history_keeps_unique_run_ids_and_shares_atomic_saves() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("scores.json");
        let mut scores = Scores::load_at(path.clone()).unwrap();
        for score in 0..501 {
            scores.record("author/game", score, "1.0", true);
        }
        let first_id = {
            let data = scores.data.lock().unwrap();
            assert_eq!(data.pending.len(), 500);
            assert_eq!(data.pending[0].score, 1);
            assert_eq!(
                data.pending
                    .iter()
                    .map(|r| &r.id)
                    .collect::<std::collections::HashSet<_>>()
                    .len(),
                500
            );
            data.pending[0].id.clone()
        };
        let peer = scores.clone();
        peer.ack(&first_id);
        scores.record("author/game", 12, "1.1", false);
        peer.save().unwrap();
        let reopened = Scores::load_at(path).unwrap();
        let data = reopened.data.lock().unwrap();
        assert_eq!(data.pending.len(), 500);
        assert!(!data.pending.iter().any(|r| r.id == first_id));
        assert_eq!(data.games["author/game"].high_score, 500);
        assert_eq!(data.games["author/game"].version, "1.1");
    }
}
