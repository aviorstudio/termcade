//! Isolated ABI-v1 WASI reactors. Guests receive clocks, entropy and discarded
//! standard output; no files, process arguments, environment or network access.
#[cfg(test)]
mod tests;
use anyhow::{Context, Result, bail};
use sha2::{Digest, Sha256};
use std::{
    collections::HashMap,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread,
    time::Duration,
};
use termcade_sdk::{ABI_VERSION, Canvas, CellShape, Color, Hud, Info, Key};
use wasmtime::{
    Config, Engine, ExternType, Instance, Linker, Memory, Module, Store, StoreLimits,
    StoreLimitsBuilder, TypedFunc, WasmParams, WasmResults,
};
use wasmtime_wasi::{
    WasiCtxBuilder,
    p2::pipe::SinkOutputStream,
    preview1::{self, WasiP1Ctx},
};

const MEMORY_LIMIT: usize = 64 << 20;
const INIT_TIMEOUT: Duration = Duration::from_secs(5);
const CALL_TIMEOUT: Duration = Duration::from_millis(500);
const EPOCH_PERIOD: Duration = Duration::from_millis(10);
const ABI_EXPORTS: [&str; 9] = [
    "termcade_abi",
    "termcade_init",
    "termcade_playfield",
    "termcade_reset",
    "termcade_key",
    "termcade_update",
    "termcade_draw",
    "termcade_score",
    "termcade_hud",
];

struct Host {
    wasi: WasiP1Ctx,
    limits: StoreLimits,
}
struct Shared {
    engine: Engine,
    modules: Mutex<HashMap<[u8; 32], Module>>,
    stop: Arc<AtomicBool>,
    clock: Mutex<Option<thread::JoinHandle<()>>>,
}
impl Drop for Shared {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::Release);
        if let Some(clock) = self.clock.get_mut().expect("epoch clock lock").take() {
            let _ = clock.join();
        }
    }
}
#[derive(Clone)]
pub struct Runtime {
    shared: Arc<Shared>,
}
impl Runtime {
    pub fn new() -> Result<Self> {
        let mut config = Config::new();
        config
            .async_support(true)
            .epoch_interruption(true)
            .max_wasm_stack(512 * 1024)
            .wasm_memory64(false)
            .wasm_multi_memory(false);
        let engine = Engine::new(&config)?;
        let ticking = engine.clone();
        let stop = Arc::new(AtomicBool::new(false));
        let stopped = stop.clone();
        let clock = thread::Builder::new()
            .name("termcade-epoch".into())
            .spawn(move || {
                while !stopped.load(Ordering::Acquire) {
                    thread::sleep(EPOCH_PERIOD);
                    ticking.increment_epoch();
                }
            })?;
        Ok(Self {
            shared: Arc::new(Shared {
                engine,
                modules: Mutex::new(HashMap::new()),
                stop,
                clock: Mutex::new(Some(clock)),
            }),
        })
    }
    pub fn compile(&self, _key: &str, wasm: &[u8]) -> Result<Module> {
        if wasm.len() > MEMORY_LIMIT {
            bail!("game.wasm exceeds the 64 MiB byte limit");
        }
        let digest: [u8; 32] = Sha256::digest(wasm).into();
        if let Some(module) = self
            .shared
            .modules
            .lock()
            .expect("module cache")
            .get(&digest)
        {
            return Ok(module.clone());
        }
        let module = Module::new(&self.shared.engine, wasm).context("compiling wasm")?;
        for name in ABI_EXPORTS {
            if !module
                .exports()
                .any(|export| export.name() == name && matches!(export.ty(), ExternType::Func(_)))
            {
                bail!("wasm module missing export {name:?} (not a termcade game?)");
            }
        }
        let mut modules = self.shared.modules.lock().expect("module cache");
        // Modules are immutable and keyed by content, so replacing an installed
        // game cannot accidentally reuse the prior version's compiled code.
        if modules.len() >= 32 {
            modules.clear();
        }
        modules.insert(digest, module.clone());
        Ok(module)
    }
    pub async fn load(
        &self,
        key: &str,
        wasm: &[u8],
        info: Info,
        shape: CellShape,
    ) -> Result<WasmGame> {
        if !(1..=200).contains(&info.pixel_w)
            || !(2..=120).contains(&info.pixel_h)
            || !info.pixel_h.is_multiple_of(2)
        {
            bail!("invalid host playfield requirements");
        }
        if shape.cols == 0 || shape.rows == 0 || shape.cols > 32 || shape.rows > 32 {
            bail!("invalid host cell subdivision");
        }
        let module = self.compile(key, wasm)?;
        let mut wasi = WasiCtxBuilder::new();
        wasi.stdout(SinkOutputStream)
            .stderr(SinkOutputStream)
            .allow_tcp(false)
            .allow_udp(false)
            .allow_ip_name_lookup(false);
        let limits = StoreLimitsBuilder::new()
            .memory_size(MEMORY_LIMIT)
            .table_elements(1 << 20)
            .memories(1)
            .tables(1)
            .instances(1)
            .build();
        let mut store = Store::new(
            &self.shared.engine,
            Host {
                wasi: wasi.build_p1(),
                limits,
            },
        );
        store.limiter(|host| &mut host.limits);
        store.set_epoch_deadline(500);
        let mut linker = Linker::new(&self.shared.engine);
        preview1::add_to_linker_async(&mut linker, |host: &mut Host| &mut host.wasi)?;
        let instance =
            tokio::time::timeout(INIT_TIMEOUT, linker.instantiate_async(&mut store, &module))
                .await
                .context("wasm instantiation timed out")??;
        if instance.get_func(&mut store, "_initialize").is_some() {
            let initialize = instance.get_typed_func::<(), ()>(&mut store, "_initialize")?;
            timed(&mut store, initialize, (), INIT_TIMEOUT)
                .await
                .context("wasm _initialize")?;
        }
        let abi = instance.get_typed_func::<(), i32>(&mut store, "termcade_abi")?;
        let actual = timed(&mut store, abi, (), INIT_TIMEOUT).await?;
        if actual != ABI_VERSION {
            bail!("game speaks ABI v{actual}; this termcade speaks v{ABI_VERSION}");
        }
        let initialize = instance.get_typed_func::<(i32, i32), i64>(&mut store, "termcade_init")?;
        let buffer = timed(
            &mut store,
            initialize,
            (shape.cols as i32, shape.rows as i32),
            INIT_TIMEOUT,
        )
        .await? as u64;
        let pointer = (buffer >> 32) as u32;
        let length = buffer as u32;
        if length == 0 {
            bail!("game did not register (termcade_init returned no buffer)");
        }
        let playfield = instance.get_typed_func::<(), i64>(&mut store, "termcade_playfield")?;
        let playfield = timed(&mut store, playfield, (), INIT_TIMEOUT).await? as u64;
        let (width, height) = ((playfield >> 32) as u32 as usize, playfield as u32 as usize);
        if (width, height) != (info.pixel_w, info.pixel_h) {
            bail!(
                "playfield is {width}x{height} but the manifest says {}x{}",
                info.pixel_w,
                info.pixel_h
            );
        }
        let expected = width * shape.cols * height * shape.rows / 2 * 4;
        if length as usize != expected {
            bail!("pixel buffer is {length} bytes, want {expected}");
        }
        let memory = instance
            .get_memory(&mut store, "memory")
            .context("game module has no exported memory")?;
        check_slice(memory.data(&store), pointer, length)
            .context("wasm pixel buffer out of bounds")?;
        let functions = Functions::new(&instance, &mut store)?;
        Ok(WasmGame {
            _runtime: self.clone(),
            store,
            memory,
            info,
            pointer,
            length,
            functions,
            error: None,
        })
    }
}

async fn timed<P: WasmParams + Sync, R: WasmResults + Sync>(
    store: &mut Store<Host>,
    function: TypedFunc<P, R>,
    arguments: P,
    timeout: Duration,
) -> Result<R> {
    store.set_epoch_deadline(timeout.as_millis().div_ceil(EPOCH_PERIOD.as_millis()) as u64);
    // Epoch interruption stops compute loops. An async timeout also cancels
    // blocking WASI polls, which epochs alone cannot interrupt inside a hostcall.
    tokio::time::timeout(timeout, function.call_async(store, arguments))
        .await
        .context("wasm call timed out")?
}
fn check_slice(memory: &[u8], pointer: u32, length: u32) -> Result<&[u8]> {
    let start = pointer as usize;
    let end = start
        .checked_add(length as usize)
        .context("guest buffer address overflow")?;
    memory.get(start..end).context("guest buffer out of bounds")
}
struct Functions {
    reset: TypedFunc<(), ()>,
    key: TypedFunc<(i32, i32), ()>,
    update: TypedFunc<(), i32>,
    draw: TypedFunc<(), ()>,
    score: TypedFunc<(), i64>,
    hud: TypedFunc<(), i64>,
}
impl Functions {
    fn new(instance: &Instance, store: &mut Store<Host>) -> Result<Self> {
        Ok(Self {
            reset: instance.get_typed_func(&mut *store, "termcade_reset")?,
            key: instance.get_typed_func(&mut *store, "termcade_key")?,
            update: instance.get_typed_func(&mut *store, "termcade_update")?,
            draw: instance.get_typed_func(&mut *store, "termcade_draw")?,
            score: instance.get_typed_func(&mut *store, "termcade_score")?,
            hud: instance.get_typed_func(&mut *store, "termcade_hud")?,
        })
    }
}
pub struct WasmGame {
    _runtime: Runtime,
    store: Store<Host>,
    memory: Memory,
    info: Info,
    pointer: u32,
    length: u32,
    functions: Functions,
    error: Option<String>,
}
impl WasmGame {
    pub fn info(&self) -> &Info {
        &self.info
    }
    pub fn error(&self) -> Option<&str> {
        self.error.as_deref()
    }
    async fn invoke<P: WasmParams + Sync, R: WasmResults + Sync>(
        &mut self,
        name: &str,
        function: TypedFunc<P, R>,
        arguments: P,
    ) -> Result<R> {
        if let Some(error) = &self.error {
            bail!("{error}");
        }
        match timed(&mut self.store, function, arguments, CALL_TIMEOUT).await {
            Ok(value) => Ok(value),
            Err(error) => {
                let message = format!("wasm {name}: {error:#}");
                self.error = Some(message.clone());
                bail!("{message}");
            }
        }
    }
    pub async fn reset(&mut self) -> Result<()> {
        self.invoke("termcade_reset", self.functions.reset.clone(), ())
            .await
    }
    pub async fn key(&mut self, key: Key, down: bool) -> Result<()> {
        self.invoke(
            "termcade_key",
            self.functions.key.clone(),
            (key as i32, i32::from(down)),
        )
        .await
    }
    pub async fn update(&mut self) -> Result<i32> {
        self.invoke("termcade_update", self.functions.update.clone(), ())
            .await
    }
    pub async fn score(&mut self) -> Result<i64> {
        self.invoke("termcade_score", self.functions.score.clone(), ())
            .await
    }
    pub async fn draw(&mut self, canvas: &mut Canvas) -> Result<()> {
        self.invoke("termcade_draw", self.functions.draw.clone(), ())
            .await?;
        let raw = match check_slice(self.memory.data(&self.store), self.pointer, self.length) {
            Ok(raw) => raw,
            Err(error) => {
                let message = format!("wasm pixel buffer: {error}");
                self.error = Some(message.clone());
                bail!("{message}");
            }
        };
        for (pixel, bytes) in canvas.pixels_mut().iter_mut().zip(raw.chunks_exact(4)) {
            *pixel = Color(u32::from_le_bytes(
                bytes.try_into().expect("four-byte pixel"),
            ));
        }
        Ok(())
    }
    pub async fn hud(&mut self) -> Result<Hud> {
        let buffer = self
            .invoke("termcade_hud", self.functions.hud.clone(), ())
            .await? as u64;
        let (pointer, length) = ((buffer >> 32) as u32, buffer as u32);
        if length == 0 || length > 65536 {
            return Ok(Hud::default());
        }
        let raw = match check_slice(self.memory.data(&self.store), pointer, length) {
            Ok(raw) => raw,
            Err(error) => {
                let message = format!("wasm HUD buffer: {error}");
                self.error = Some(message.clone());
                bail!("{message}");
            }
        };
        Ok(serde_json::from_slice(raw).unwrap_or_default())
    }
}
