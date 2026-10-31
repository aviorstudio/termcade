use anyhow::{Result, bail};
use reqwest::Method;
use serde_json::json;
use std::{collections::HashSet, fs};
use termcade_plugin::Runtime;
use termcade_registry::{self as registry, Client, Error, HandleOwner, Session};
pub const USAGE: &str = include_str!("usage.txt");
fn require(why: &str) -> Result<(Client, Session)> {
    let session = registry::load_session()?.ok_or_else(|| anyhow::anyhow!("{why}"))?;
    let client = Client::new(&registry::registry_url(Some(&session)), &session.token)?;
    Ok((client, session))
}
fn out(value: &str) -> String {
    registry::safe_text(value, 4096)
}
pub async fn run(args: &[String]) -> Result<()> {
    match args[0].as_str() {
        "help" | "-h" | "--help" => print!("{USAGE}"),
        "version" | "-v" | "--version" => println!("termcade {}", crate::version()),
        "add" | "install" => add(&args[1..]).await?,
        "remove" | "uninstall" => remove(&args[1..]).await?,
        "list" => list()?,
        "sync" => sync().await?,
        "login" => login(&args[1..]).await?,
        "logout" => {
            registry::clear_session()?;
            println!("logged out");
        }
        "publish" => publish(&args[1..]).await?,
        "keys" => keys(&args[1..]).await?,
        "org" => org(&args[1..]).await?,
        "whoami" => whoami().await?,
        "username" => username(&args[1..]).await?,
        "account" => {
            anyhow::ensure!(
                args.get(1).is_some_and(|s| s == "delete"),
                "unknown account subcommand; try: termcade account delete <username>"
            );
            delete_account(&args[2..]).await?;
        }
        "dev" => match args.get(1).map(String::as_str) {
            Some("new") => crate::scaffold::new(&args[2..])?,
            Some("build") => crate::scaffold::build(&args[2..], &Runtime::new()?).await?,
            Some("install") => {
                anyhow::ensure!(args.len() == 3, "usage: termcade dev install <file.tcade>");
                anyhow::ensure!(
                    fs::metadata(&args[2])?.len() <= termcade_manifest::MAX_PACKAGE_BYTES as u64,
                    "package exceeds the byte limit"
                );
                let (m, dest) = crate::local::install(&fs::read(&args[2])?, &Runtime::new()?)?;
                println!(
                    "added {} {} → {}",
                    m.game.id,
                    m.game.version,
                    dest.display()
                );
            }
            _ => bail!(
                "unknown dev subcommand; try: termcade dev new <author/slug> · termcade dev build [dir] · termcade dev install <file>"
            ),
        },
        command => return Err(crate::UnknownCommand(command.into()).into()),
    }
    Ok(())
}
async fn install_from(client: &Client, id: &str, runtime: &Runtime) -> Result<()> {
    let (author, slug) = crate::local::parts(id)?;
    let file = client.download(author, slug).await.map_err(expired)?;
    let (m, dest) = crate::local::install(&fs::read(file.path())?, runtime)?;
    println!(
        "added {} {} → {}",
        m.game.id,
        m.game.version,
        dest.display()
    );
    Ok(())
}
fn expired(error: Error) -> anyhow::Error {
    if matches!(error, Error::LoginRequired) {
        anyhow::anyhow!("your session has expired — run `termcade login`")
    } else {
        error.into()
    }
}
async fn add(args: &[String]) -> Result<()> {
    anyhow::ensure!(args.len() == 1, "usage: termcade add <author/slug>");
    let id = &args[0];
    if let Some((bare, _)) = id.split_once('@')
        && termcade_manifest::valid_id(bare)
    {
        bail!(
            "versions cannot be pinned — `termcade add {bare}` installs what {bare} currently ships"
        );
    }
    if !id.contains('/') && slug_only(id) {
        bail!(
            "{id:?} is missing an author — marketplace ids look like author/slug, e.g. aviorstudio/{id}"
        );
    }
    anyhow::ensure!(
        termcade_manifest::valid_id(id),
        "add installs marketplace ids only; use `termcade dev install <file>` for a local package"
    );
    let (client, _) = require("installing a game requires an account — run `termcade login`")?;
    let (author, slug) = crate::local::parts(id)?;
    client.library_set(author, slug, true).await?;
    install_from(&client, id, &Runtime::new()?).await
}
fn slug_only(id: &str) -> bool {
    !id.is_empty()
        && id
            .bytes()
            .next()
            .is_some_and(|b| b.is_ascii_lowercase() || b.is_ascii_digit())
        && id
            .bytes()
            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-')
}
async fn remove(args: &[String]) -> Result<()> {
    anyhow::ensure!(args.len() == 1, "usage: termcade remove <author/slug>");
    let id = &args[0];
    let (author, slug) = crate::local::parts(id)?;
    if let Ok(Some(session)) = registry::load_session() {
        let client = Client::new(&registry::registry_url(Some(&session)), &session.token)?;
        match client.library_set(author, slug, false).await {
            Ok(()) => (),
            Err(e) if e.not_found() => (),
            Err(e) => return Err(expired(e)),
        }
    }
    crate::local::remove(id)?;
    println!("removed {id}");
    Ok(())
}
fn list() -> Result<()> {
    if let Err(e) = crate::local::seed() {
        eprintln!("termcade: starter pack: {e}");
    }
    let games = crate::local::discover()?;
    if games.is_empty() {
        println!("no games installed — run `termcade` and press m for the marketplace");
    }
    for game in games {
        if game.error.is_empty() {
            println!("{:<24} {:<28} installed", out(&game.title), out(&game.id));
        } else {
            println!(
                "{:<24} {:<28} broken: {}",
                out(&game.title),
                out(&game.id),
                out(&game.error)
            );
        }
    }
    Ok(())
}
pub async fn sync() -> Result<()> {
    let (client, _) = require("syncing your library requires an account — run `termcade login`")?;
    let library = client.library().await.map_err(expired)?;
    if library.is_empty() {
        println!("nothing in your library yet — add a game and it will follow you");
        return Ok(());
    }
    if let Err(e) = crate::local::seed() {
        eprintln!("termcade: starter pack: {e}");
    }
    let installed: HashSet<String> = crate::local::discover()?
        .into_iter()
        .map(|g| g.id)
        .collect();
    let runtime = Runtime::new()?;
    let mut added = 0;
    let mut failed = 0;
    for game in library {
        if installed.contains(&game.id) || crate::local::parts(&game.id).is_err() {
            continue;
        }
        if !game.has_package {
            eprintln!(
                "skipping {}: nothing published to install yet",
                out(&game.id)
            );
            continue;
        }
        match install_from(&client, &game.id, &runtime).await {
            Ok(()) => added += 1,
            Err(e) => {
                eprintln!(
                    "could not restore {}: {}",
                    out(&game.id),
                    out(&e.to_string())
                );
                failed += 1;
            }
        }
    }
    if failed > 0 {
        bail!("restored {added} game(s); {failed} could not be installed");
    }
    if added == 0 {
        println!("your library is already here");
    } else {
        println!("restored {added} game(s)");
    }
    Ok(())
}
async fn login(args: &[String]) -> Result<()> {
    anyhow::ensure!(args.is_empty(), "usage: termcade login");
    let client = Client::new(&registry::registry_url(None), "")?;
    let session=client.device_login("termcade CLI",|uri,code| {
        println!("\nUsing a browser, visit:\n  {uri}\n\nAnd enter this code:\n  {code}\n\nWaiting for approval (Ctrl+C to cancel)…");
    }).await?;
    registry::save_session(&session)?;
    println!(
        "logged in as {} ({})",
        out(&session.email),
        out(&session.registry)
    );
    if let Err(e) = sync().await {
        eprintln!(
            "note: could not restore your library: {}",
            out(&e.to_string())
        );
    }
    Ok(())
}
async fn publish(args: &[String]) -> Result<()> {
    anyhow::ensure!(
        (2..=3).contains(&args.len()),
        "usage: termcade publish <repo-url> <tag> [asset]"
    );
    let session = registry::load_session()?;
    let env = std::env::var("TERMCADE_TOKEN").unwrap_or_default();
    let env = env.trim();
    let token = if !env.is_empty() {
        anyhow::ensure!(
            registry::is_publish_key(env),
            "TERMCADE_TOKEN must be a scoped publish key created by `termcade keys new`"
        );
        env
    } else if let Some(ref session) = session {
        &session.token
    } else {
        bail!(
            "publishing requires an account — run `termcade login`, or set TERMCADE_TOKEN to a publish key (`termcade keys new`)"
        );
    };
    let asset = if let Some(asset) = args.get(2) {
        asset.clone()
    } else {
        let raw = fs::read(termcade_manifest::FILE_NAME).map_err(|_| {
            anyhow::anyhow!("no asset name given, and no termcade.toml here to infer one from")
        })?;
        format!("{}.tcade", termcade_manifest::Manifest::parse(&raw)?.slug())
    };
    let published = Client::new(&registry::registry_url(session.as_ref()), token)?
        .publish(&args[0], &args[1], &asset)
        .await?;
    println!(
        "published {} {}\n  from   {} @ {} ({})\n  sha256 {}",
        out(&published.id),
        out(&published.version),
        out(&published.repo),
        out(&published.tag),
        out(&published.asset),
        out(&published.sha256)
    );
    Ok(())
}
async fn keys(args: &[String]) -> Result<()> {
    let (client, _) = require("managing keys requires an account — run `termcade login`")?;
    match args.first().map(String::as_str).unwrap_or("list") {
        "list" => {
            let keys = client.keys().await?;
            if keys.is_empty() {
                println!("no publish keys — create one with `termcade keys new <name> <username>`");
            }
            for key in keys {
                let used = if key.last_used_at.is_empty() {
                    "never used".into()
                } else {
                    format!("last used {}", out(&key.last_used_at))
                };
                println!(
                    "{:<24} {:<20} {used}\n  {}",
                    out(&key.name),
                    out(&key.username),
                    out(&key.id)
                );
            }
        }
        "new" => {
            anyhow::ensure!(
                args.len() == 3,
                "usage: termcade keys new <name> <username>"
            );
            let key = client.create_key(&args[1], &args[2]).await?;
            anyhow::ensure!(
                registry::is_publish_key(&key.token),
                "the marketplace returned a malformed publish key"
            );
            println!(
                "created {:?}, publishing as {}\n\n  {}\n",
                out(&key.name),
                out(&key.username),
                key.token
            );
            eprintln!(
                "that token is shown once and cannot be recovered — put it somewhere safe now"
            );
        }
        "revoke" => {
            anyhow::ensure!(args.len() == 2, "usage: termcade keys revoke <id>");
            client
                .empty(Method::DELETE, &["v1", "keys", &args[1]], None)
                .await?;
            println!("revoked");
        }
        _ => bail!("unknown keys subcommand; try: list · new <name> <username> · revoke <id>"),
    }
    Ok(())
}
fn role(admin: bool) -> &'static str {
    if admin { "admin" } else { "member" }
}
async fn org(args: &[String]) -> Result<()> {
    let (client, _) = require("managing studios requires an account — run `termcade login`")?;
    match args.first().map(String::as_str).unwrap_or("list") {
        "list" => {
            let me = client.me().await?;
            if !me.username.is_empty() {
                println!("{:<24} you", out(&me.username));
            }
            for org in &me.orgs {
                println!("{:<24} studio ({})", out(&org.username), role(org.admin));
            }
            if me.orgs.is_empty() {
                println!("\nno studios — create one with `termcade org new <username>`");
            }
        }
        "new" => {
            anyhow::ensure!(
                (2..=3).contains(&args.len()),
                "usage: termcade org new <username> [bio]"
            );
            let org = client
                .create_org(&args[1], args.get(2).map(String::as_str).unwrap_or(""), "")
                .await?;
            println!(
                "created {} — you are its admin\npublish as {}/<game>, and add people with `termcade org add {} <email>`",
                out(&org.username),
                out(&org.username),
                out(&org.username)
            );
        }
        "show" => {
            anyhow::ensure!(args.len() == 2, "usage: termcade org show <username>");
            let org = client.org(&args[1]).await?;
            println!("{}", out(&org.username));
            if !org.bio.is_empty() {
                println!("  {}", out(&org.bio));
            }
            if org.games.is_empty() {
                println!("  no published games");
            }
            for game in org.games {
                println!("  {}", out(&game));
            }
            if let Ok(members) = client.members(&args[1]).await {
                if !members.is_empty() {
                    println!();
                }
                for m in members {
                    println!("  {:<32} {}", out(&m.email), role(m.admin));
                }
            }
        }
        "add" => {
            anyhow::ensure!(
                (3..=4).contains(&args.len()),
                "usage: termcade org add <org> <email> [admin]"
            );
            let admin = args.get(3).is_some_and(|s| s == "admin");
            client
                .empty(
                    Method::POST,
                    &["v1", "orgs", &args[1], "members"],
                    Some(json!({"email":args[2],"admin":admin})),
                )
                .await?;
            println!(
                "{} is now {} of {}",
                out(&args[2]),
                if admin { "an admin" } else { "a member" },
                out(&args[1])
            );
        }
        "edit" => {
            anyhow::ensure!(args.len() == 3, "usage: termcade org edit <org> <bio>");
            let org = client.update_org(&args[1], Some(&args[2]), None).await?;
            println!("updated {}", out(&org.username));
        }
        "remove" => {
            anyhow::ensure!(args.len() == 3, "usage: termcade org remove <org> <email>");
            client
                .empty(
                    Method::DELETE,
                    &["v1", "orgs", &args[1], "members", &args[2]],
                    None,
                )
                .await?;
            println!("removed {} from {}", out(&args[2]), out(&args[1]));
        }
        "delete" => {
            let name = args.get(1).map(String::as_str).unwrap_or("<org>");
            anyhow::ensure!(
                args.len() == 3 && args[2] == name,
                "this dissolves the studio and releases its handle for anyone to claim.\nit cannot be undone. to confirm:\n\n  termcade org delete {name} {name}"
            );
            client
                .empty(Method::DELETE, &["v1", "orgs", name], None)
                .await?;
            println!("dissolved {}", out(name));
        }
        _ => bail!(
            "unknown org subcommand; try: list · new <username> [bio] · show <username> · edit <org> <bio> · add <org> <email> [admin] · remove <org> <email> · delete <org> <org>"
        ),
    }
    Ok(())
}
async fn whoami() -> Result<()> {
    let (client, session) = require("not signed in — run `termcade login`")?;
    let me = client.me().await?;
    println!("{} ({})", out(&me.email), out(&session.registry));
    if me.username.is_empty() {
        println!("\nno username yet — claim one with `termcade username <name>`");
    } else {
        println!("\npublish as:\n  {:<24} you", out(&me.username));
    }
    for org in me.orgs {
        println!("  {:<24} studio ({})", out(&org.username), role(org.admin));
    }
    Ok(())
}
async fn username(args: &[String]) -> Result<()> {
    anyhow::ensure!(args.len() == 1, "usage: termcade username <name>");
    let name = &args[0];
    let session = registry::load_session()?;
    let public = Client::new(&registry::registry_url(session.as_ref()), "")?;
    let owner = match public.owner(name).await {
        Ok(owner) => Some(owner),
        Err(e) if e.not_found() || e.to_string().contains("is available") => None,
        Err(e) => return Err(e.into()),
    };
    if let Some(mut session) = session {
        let client = Client::new(&registry::registry_url(Some(&session)), &session.token)?;
        let claimed: HandleOwner = client
            .json(
                Method::PATCH,
                "/v1/me/username",
                Some(json!({"username":name})),
            )
            .await?;
        session.username = claimed.name;
        registry::save_session(&session)?;
        println!(
            "you are {} — publish as {}/<game>",
            out(&session.username),
            out(&session.username)
        );
    } else {
        if let Some(owner) = owner {
            bail!(
                "{name} is taken (by {})",
                if owner.is_org {
                    "a studio"
                } else {
                    "an account"
                }
            );
        }
        println!(
            "{name} is available — create an account in the app, then claim it after `termcade login`"
        );
    }
    Ok(())
}
async fn delete_account(args: &[String]) -> Result<()> {
    let (client, session) = require("not signed in — run `termcade login`")?;
    anyhow::ensure!(
        args.len() == 1 && args[0] == session.username,
        "this deletes {} and releases the handle {:?} for anyone to claim.\nit cannot be undone. to confirm:\n\n  termcade account delete {}",
        out(&session.email),
        out(&session.username),
        out(&session.username)
    );
    client.empty(Method::DELETE, &["v1", "me"], None).await?;
    registry::clear_session()?;
    println!("account deleted");
    Ok(())
}
