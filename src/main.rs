#[macro_export]
macro_rules! notice {
    ($($argument:tt)*) => {
        if $crate::cli::machine() { eprintln!($($argument)*); }
        else { println!($($argument)*); }
    };
}
mod backend;
mod cli;
mod local;
mod scaffold;
mod scores;
mod ui;
#[derive(Debug, thiserror::Error)]
#[error("unknown command {0:?}")]
struct UnknownCommand(String);
fn version() -> &'static str {
    option_env!("VERSION").unwrap_or(if cfg!(debug_assertions) {
        "dev"
    } else {
        concat!("v", env!("CARGO_PKG_VERSION"))
    })
}
fn terminal_text(value: &str, max: usize) -> String {
    let mut out = String::new();
    for c in value
        .chars()
        .filter(|c| !c.is_control() || matches!(c, '\n' | '\t'))
    {
        out.push(c);
        if out.len() >= max {
            break;
        }
    }
    out
}
#[tokio::main]
async fn main() {
    let mut args: Vec<String> = std::env::args().skip(1).collect();
    let machine = args.first().is_some_and(|arg| arg == "--json");
    if machine {
        args.remove(0);
    }
    cli::configure(machine);
    let result = if args.is_empty() {
        if machine {
            Err(anyhow::anyhow!(
                "--json requires a finite command; the default interface is Ratatui"
            ))
        } else {
            ui::run().await
        }
    } else {
        tokio::select! {result=cli::run(&args)=>result,_=shutdown()=>Err(anyhow::anyhow!(if args[0]=="login"{"login canceled"}else{"command canceled"}))}
    };
    if machine {
        match &result {
            Ok(()) => println!(
                "{}",
                serde_json::json!({"v":1,"ok":true,"result":cli::take_result()})
            ),
            Err(error) => {
                let mut problem = serde_json::json!({"code":if error.downcast_ref::<UnknownCommand>().is_some(){"invalid_usage"}else{"operation_failed"},"message":terminal_text(&error.to_string(),16000)});
                if let Some(termcade_registry::Error::Response {
                    status,
                    code,
                    request_id,
                    ..
                }) = error.downcast_ref::<termcade_registry::Error>()
                {
                    problem["status"] = serde_json::json!(status);
                    if !code.is_empty() {
                        problem["code"] = serde_json::json!(code);
                    }
                    if !request_id.is_empty() {
                        problem["request_id"] = serde_json::json!(request_id);
                    }
                }
                println!("{}", serde_json::json!({"v":1,"ok":false,"error":problem}));
            }
        }
    }
    if let Err(error) = result {
        if let Some(UnknownCommand(command)) = error.downcast_ref::<UnknownCommand>() {
            eprintln!("termcade: unknown command {command:?}\n\n{}", cli::USAGE);
            std::process::exit(2);
        }
        eprintln!("termcade: {}", terminal_text(&error.to_string(), 16000));
        std::process::exit(1);
    }
}

async fn shutdown() {
    #[cfg(unix)]
    {
        use tokio::signal::unix::{SignalKind, signal};
        if let (Ok(mut term), Ok(mut hup)) = (
            signal(SignalKind::terminate()),
            signal(SignalKind::hangup()),
        ) {
            tokio::select! {_=tokio::signal::ctrl_c()=>(),_=term.recv()=>(),_=hup.recv()=>()};
            return;
        }
    }
    let _ = tokio::signal::ctrl_c().await;
}
