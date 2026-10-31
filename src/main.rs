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
    option_env!("VERSION").unwrap_or("dev")
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
    let args: Vec<String> = std::env::args().skip(1).collect();
    let result = if args.is_empty() {
        ui::run().await
    } else {
        tokio::select! {result=cli::run(&args)=>result,_=shutdown()=>Err(anyhow::anyhow!(if args[0]=="login"{"login canceled"}else{"command canceled"}))}
    };
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
