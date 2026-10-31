//! Export a Rust game as an ABI-v1 WASIp1 reactor with
//! `termcade_sdk::termcade_game!(MyGame::new)` and a `cdylib` Cargo target.
#[cfg(target_arch = "wasm32")]
mod reactor {
    use crate::{BLACK, Canvas, CellShape, Game, Key};
    use std::cell::RefCell;
    struct State {
        game: Box<dyn Game>,
        canvas: Canvas,
        hud: Vec<u8>,
    }
    thread_local! {static STATE:RefCell<Option<State>>=const {RefCell::new(None)};}
    fn with_state<T>(f: impl FnOnce(&mut State) -> T) -> T {
        STATE.with(|slot| {
            f(slot
                .borrow_mut()
                .as_mut()
                .expect("termcade_init must run first"))
        })
    }
    fn pack(high: u32, low: u32) -> i64 {
        ((u64::from(high) << 32) | u64::from(low)) as i64
    }
    pub fn initialize(cols: i32, rows: i32, game: Box<dyn Game>) -> i64 {
        if !(1..=3).contains(&cols) || !(1..=3).contains(&rows) {
            return 0;
        }
        let info = game.info();
        let canvas = Canvas::new(
            info.pixel_w,
            info.pixel_h,
            BLACK,
            CellShape::host(cols as usize, rows as usize),
        );
        let pointer = canvas.pixels().as_ptr() as usize as u32;
        let bytes =
            u32::try_from(std::mem::size_of_val(canvas.pixels())).expect("ABI pixel byte length");
        STATE.with(|slot| {
            *slot.borrow_mut() = Some(State {
                game,
                canvas,
                hud: Vec::new(),
            })
        });
        pack(pointer, bytes)
    }
    pub fn playfield() -> i64 {
        with_state(|s| {
            let info = s.game.info();
            pack(info.pixel_w as u32, info.pixel_h as u32)
        })
    }
    pub fn reset() {
        with_state(|s| s.game.reset());
    }
    pub fn key(key: i32, down: i32) {
        with_state(|s| {
            if down != 0 {
                s.game.handle_key(Key::from_i32(key));
            } else {
                s.game.handle_key_up(Key::from_i32(key));
            }
        });
    }
    pub fn update() -> i32 {
        with_state(|s| s.game.update() as i32)
    }
    pub fn draw() {
        with_state(|s| {
            s.canvas.clear();
            s.game.draw(&mut s.canvas);
        });
    }
    pub fn score() -> i64 {
        with_state(|s| s.game.score())
    }
    pub fn hud() -> i64 {
        with_state(|s| {
            s.hud.clear();
            if serde_json::to_writer(&mut s.hud, &s.game.hud()).is_err() || s.hud.is_empty() {
                return 0;
            }
            pack(
                s.hud.as_ptr() as usize as u32,
                u32::try_from(s.hud.len()).expect("ABI HUD length"),
            )
        })
    }
}
#[cfg(target_arch = "wasm32")]
pub use reactor::*;

/// Export the frozen nine-function termcade ABI. Factory invocation is lazy:
/// creating a fresh instance and calling `termcade_init` creates a fresh run.
#[macro_export]
macro_rules! termcade_game {
    ($factory:expr) => {
        #[cfg(target_arch = "wasm32")]
        mod __termcade_exports {
            #[allow(unused_imports)]
            use super::*;
            #[unsafe(no_mangle)]
            pub extern "C" fn termcade_abi() -> i32 {
                $crate::ABI_VERSION
            }
            #[unsafe(no_mangle)]
            pub extern "C" fn termcade_init(cols: i32, rows: i32) -> i64 {
                $crate::tcgame::initialize(cols, rows, Box::new(($factory)()))
            }
            #[unsafe(no_mangle)]
            pub extern "C" fn termcade_playfield() -> i64 {
                $crate::tcgame::playfield()
            }
            #[unsafe(no_mangle)]
            pub extern "C" fn termcade_reset() {
                $crate::tcgame::reset()
            }
            #[unsafe(no_mangle)]
            pub extern "C" fn termcade_key(key: i32, down: i32) {
                $crate::tcgame::key(key, down)
            }
            #[unsafe(no_mangle)]
            pub extern "C" fn termcade_update() -> i32 {
                $crate::tcgame::update()
            }
            #[unsafe(no_mangle)]
            pub extern "C" fn termcade_draw() {
                $crate::tcgame::draw()
            }
            #[unsafe(no_mangle)]
            pub extern "C" fn termcade_score() -> i64 {
                $crate::tcgame::score()
            }
            #[unsafe(no_mangle)]
            pub extern "C" fn termcade_hud() -> i64 {
                $crate::tcgame::hud()
            }
        }
    };
}
