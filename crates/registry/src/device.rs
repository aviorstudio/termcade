use crate::{Client, Error, Me, Result, Session, invalid, safe_text};
use reqwest::Method;
use serde::Deserialize;
use serde_json::json;
use std::time::Duration;
use time::{OffsetDateTime, format_description::well_known::Rfc3339};
use tokio::time::Instant;
use url::Url;
pub const PAIRING_URI: &str = "https://app.termca.de/pair";
fn credential(value: &str, prefix: &str, min: usize, max: usize) -> bool {
    value.strip_prefix(prefix).is_some_and(|v| {
        (min..=max).contains(&v.len())
            && v.bytes()
                .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'-'))
    })
}
pub fn is_cli_token(value: &str) -> bool {
    credential(value, "tcc_", 43, 43)
}
pub fn is_publish_key(value: &str) -> bool {
    credential(value, "ak_", 20, 512)
}
fn user_code(value: &str) -> bool {
    value.len() == 9
        && value.as_bytes()[4] == b'-'
        && value
            .bytes()
            .enumerate()
            .all(|(i, b)| i == 4 || b"ABCDEFGHJKMNPQRSTUVWXYZ23456789".contains(&b))
}
pub fn pairing_url_with_code(base: &str, code: &str) -> String {
    let code = code.trim().to_ascii_uppercase();
    if !user_code(&code) {
        return base.into();
    }
    let Ok(mut url) = Url::parse(base) else {
        return base.into();
    };
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || url.path().trim_end_matches('/') != "/pair"
    {
        return base.into();
    }
    url.set_path(&format!("/pair/{code}"));
    url.to_string()
}
#[derive(Clone, Debug)]
pub struct DeviceRound {
    pub(crate) device_code: String,
    pub user_code: String,
    pub expires_in: Duration,
    pub interval: Duration,
}
#[derive(Deserialize, Default)]
#[serde(default)]
struct Start {
    device_code: String,
    user_code: String,
    verification_uri: String,
    expires_in: i64,
    interval: i64,
}
#[derive(Deserialize, Default)]
#[serde(default)]
struct Poll {
    status: String,
    token: String,
    credential_id: String,
    expires_at: String,
    interval: i64,
}
fn expiry(raw: &str) -> Result<OffsetDateTime> {
    let value = OffsetDateTime::parse(raw, &Rfc3339)
        .map_err(|_| invalid("the marketplace returned an unsafe CLI credential expiry"))?;
    let remaining = value - OffsetDateTime::now_utc();
    if remaining <= time::Duration::ZERO || remaining > time::Duration::days(31) {
        return Err(invalid(
            "the marketplace returned an unsafe CLI credential expiry",
        ));
    }
    Ok(value)
}
fn backoff(value: Duration) -> Duration {
    (value * 2).clamp(Duration::from_secs(1), Duration::from_secs(30))
}
impl Client {
    pub async fn start_device(&self, name: &str) -> Result<DeviceRound> {
        let name = safe_text(name, 200);
        let name = if name.is_empty() {
            "termcade CLI"
        } else {
            &name
        };
        let out: Start = self
            .json(
                Method::POST,
                "/v1/device/start",
                Some(json!({"client_id":"termcade-cli", "device_name":name})),
            )
            .await?;
        let code = out.user_code.trim().to_ascii_uppercase();
        if !credential(&out.device_code, "tcd_", 43, 43) {
            return Err(invalid("the marketplace returned a malformed device code"));
        }
        if !user_code(&code) {
            return Err(invalid("the marketplace returned a malformed pairing code"));
        }
        if out.verification_uri != PAIRING_URI {
            return Err(invalid(
                "the marketplace returned an untrusted pairing address",
            ));
        }
        if !(30..=900).contains(&out.expires_in) {
            return Err(invalid("the marketplace returned an unsafe pairing expiry"));
        }
        if !(1..=30).contains(&out.interval) {
            return Err(invalid(
                "the marketplace returned an unsafe polling interval",
            ));
        }
        Ok(DeviceRound {
            device_code: out.device_code,
            user_code: code,
            expires_in: Duration::from_secs(out.expires_in as u64),
            interval: Duration::from_secs(out.interval as u64),
        })
    }
    async fn poll_device(&self, device: &str) -> Result<Poll> {
        if !credential(device, "tcd_", 43, 43) {
            return Err(invalid("refusing to poll with a malformed device code"));
        }
        let result: Poll = self
            .json(
                Method::POST,
                "/v1/device/poll",
                Some(json!({"device_code":device})),
            )
            .await?;
        match result.status.as_str() {
            "pending" if !(1..=30).contains(&result.interval) => {
                return Err(invalid(
                    "the marketplace returned an unsafe polling interval",
                ));
            }
            "pending" => {}
            "expired" if !result.token.is_empty() => {
                return Err(invalid(
                    "the marketplace returned a credential in an expired response",
                ));
            }
            "expired" => {}
            "approved" => {
                if !is_cli_token(&result.token) {
                    return Err(invalid(
                        "the marketplace returned a malformed CLI credential",
                    ));
                }
                if result.credential_id.is_empty()
                    || safe_text(&result.credential_id, 200) != result.credential_id
                {
                    return Err(invalid(
                        "the marketplace returned a malformed credential identifier",
                    ));
                }
                expiry(&result.expires_at)?;
            }
            _ => return Err(invalid("the marketplace returned an unknown device status")),
        }
        Ok(result)
    }
    pub async fn complete_device(&self, round: &DeviceRound) -> Result<Session> {
        let deadline = Instant::now() + round.expires_in;
        let mut interval = round.interval;
        let mut delay = interval;
        let approved = loop {
            if tokio::time::timeout_at(deadline, tokio::time::sleep(delay))
                .await
                .is_err()
            {
                return Err(invalid("device authorization expired"));
            }
            let result = tokio::time::timeout_at(deadline, self.poll_device(&round.device_code))
                .await
                .map_err(|_| invalid("device authorization expired"))?;
            let poll = match result {
                Ok(poll) => poll,
                Err(e) if e.transient() => {
                    delay = backoff(delay);
                    continue;
                }
                Err(e) => return Err(e),
            };
            match poll.status.as_str() {
                "pending" => {
                    interval = Duration::from_secs(poll.interval as u64);
                    delay = interval;
                }
                "expired" => return Err(invalid("device authorization expired")),
                "approved" => break poll,
                _ => unreachable!(),
            }
        };
        let client = Client::new(&self.base, &approved.token)?;
        let expires = expiry(&approved.expires_at)?;
        let verify_deadline = Instant::now()
            + Duration::try_from(expires - OffsetDateTime::now_utc())
                .map_err(|_| invalid("device authorization expired"))?;
        let mut delay = Duration::from_secs(1);
        let me: Me = loop {
            match tokio::time::timeout_at(verify_deadline, client.me())
                .await
                .map_err(|_| invalid("verifying issued CLI credential: credential expired"))?
            {
                Ok(me) => break me,
                Err(e) if e.transient() => {
                    tokio::time::timeout_at(verify_deadline, tokio::time::sleep(delay))
                        .await
                        .map_err(|_| {
                            invalid("verifying issued CLI credential: credential expired")
                        })?;
                    delay = backoff(delay);
                }
                Err(e) => return Err(invalid(format!("verifying issued CLI credential: {e}"))),
            }
        };
        if me.email.trim().is_empty() {
            return Err(invalid(
                "the marketplace returned an incomplete account identity",
            ));
        }
        Ok(Session {
            registry: self.base.clone(),
            email: me.email,
            username: me.username,
            token: approved.token,
            credential_id: approved.credential_id,
            expires_at: approved.expires_at,
            notice: String::new(),
        })
    }
    pub async fn device_login(
        &self,
        name: &str,
        mut display: impl FnMut(&str, &str),
    ) -> Result<Session> {
        let mut start_delay = Duration::from_secs(1);
        let mut expiry_delay = Duration::from_secs(1);
        loop {
            let round = match self.start_device(name).await {
                Ok(round) => round,
                Err(e) if e.transient() => {
                    tokio::time::sleep(start_delay).await;
                    start_delay = backoff(start_delay);
                    continue;
                }
                Err(e) => return Err(e),
            };
            display(PAIRING_URI, &round.user_code);
            match self.complete_device(&round).await {
                Err(Error::Invalid(message)) if message == "device authorization expired" => {
                    tokio::time::sleep(expiry_delay).await;
                    expiry_delay = backoff(expiry_delay);
                }
                result => return result,
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tests::Mock;
    fn start() -> serde_json::Value {
        json!({"device_code":format!("tcd_{}", "a".repeat(43)), "user_code":"ABCD-EFGH", "verification_uri":PAIRING_URI, "expires_in":60, "interval":1})
    }
    #[tokio::test]
    async fn rejects_api_selected_pairing_urls_and_unsafe_device_limits() {
        for (field, value) in [
            ("verification_uri", json!("https://evil.example/pair")),
            ("device_code", json!("other")),
            ("user_code", json!("ABCI-EFGH")),
            ("expires_in", json!(29)),
            ("expires_in", json!(901)),
            ("interval", json!(0)),
            ("interval", json!(31)),
        ] {
            let mut response = start();
            response[field] = value;
            let mock = Mock::json(vec![response]);
            assert!(
                Client::new(&mock.base, "")
                    .unwrap()
                    .start_device("termcade CLI")
                    .await
                    .is_err(),
                "accepted {field}"
            );
        }
    }
    #[tokio::test]
    async fn approved_credential_is_verified_before_it_becomes_a_session() {
        let token = format!("tcc_{}", "b".repeat(43));
        let expires = (OffsetDateTime::now_utc() + time::Duration::hours(1))
            .format(&Rfc3339)
            .unwrap();
        let mock = Mock::json(vec![
            start(),
            json!({"status":"approved","token":token,"credential_id":"cli_123","expires_at":expires}),
            json!({"email":"player@example.org","username":"player","handles":["player"],"orgs":[]}),
        ]);
        let client = Client::new(&mock.base, "").unwrap();
        let mut round = client.start_device("termcade CLI").await.unwrap();
        round.interval = Duration::ZERO;
        let session = client.complete_device(&round).await.unwrap();
        assert_eq!(session.email, "player@example.org");
        assert_eq!(session.token, token);
        let requests = mock.requests.lock().unwrap();
        assert!(!requests[0].to_ascii_lowercase().contains("authorization:"));
        assert!(requests[1].contains("tcd_"));
        assert!(!requests[1].to_ascii_lowercase().contains("authorization:"));
        assert!(
            requests[2]
                .to_ascii_lowercase()
                .contains("authorization: bearer tcc_")
        );
    }
    #[tokio::test]
    async fn poll_refuses_wrong_credential_classes_and_expiries() {
        let expires = (OffsetDateTime::now_utc() + time::Duration::hours(1))
            .format(&Rfc3339)
            .unwrap();
        let approved = json!({"status":"approved","token":format!("tcc_{}","b".repeat(43)),"credential_id":"cli_123","expires_at":expires});
        for (field, value) in [
            ("token", json!("ak_abcdefghijklmnopqrst")),
            ("credential_id", json!("\u{1b}[evil")),
            ("expires_at", json!("2000-01-01T00:00:00Z")),
            ("expires_at", json!("2099-01-01T00:00:00Z")),
            ("status", json!("unexpected")),
        ] {
            let mut response = approved.clone();
            response[field] = value;
            let mock = Mock::json(vec![response]);
            assert!(
                Client::new(&mock.base, "")
                    .unwrap()
                    .poll_device(&format!("tcd_{}", "a".repeat(43)))
                    .await
                    .is_err()
            );
        }
        let mock = Mock::json(vec![json!({"status":"expired","token":"present"})]);
        assert!(
            Client::new(&mock.base, "")
                .unwrap()
                .poll_device(&format!("tcd_{}", "a".repeat(43)))
                .await
                .is_err()
        );
    }
}
