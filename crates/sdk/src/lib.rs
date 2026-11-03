//! The game contract and pixel renderer shared by native tools and WASI games.
mod canvas;
mod shape;
pub mod tcgame;
pub use canvas::{Canvas, Cell};
use serde::{Deserialize, Serialize};
pub use shape::CellShape;
use std::ops::{Add, Sub};

pub const TPS: u32 = 60;
pub const DT: f64 = 1.0 / TPS as f64;
pub const ABI_VERSION: i32 = 1;
pub const DEFAULT_HOLD_TICKS: u64 = 8;

/// A truecolor pixel. Its memory representation is the ABI's little-endian u32.
#[repr(transparent)]
#[derive(Clone, Copy, Default, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(transparent)]
pub struct Color(pub u32);
impl Color {
    pub const fn rgb(self) -> (u8, u8, u8) {
        ((self.0 >> 16) as u8, (self.0 >> 8) as u8, self.0 as u8)
    }
}
pub const BLACK: Color = Color(0x000000);
pub const WHITE: Color = Color(0xf2f2f2);
pub const GRAY: Color = Color(0x8a8a8a);
pub const DIM_GRAY: Color = Color(0x3a3a3a);
pub const RED: Color = Color(0xe64545);
pub const ORANGE: Color = Color(0xf08c28);
pub const YELLOW: Color = Color(0xe6c945);
pub const GREEN: Color = Color(0x4fc964);
pub const CYAN: Color = Color(0x3fc4c9);
pub const BLUE: Color = Color(0x4f7de0);
pub const PURPLE: Color = Color(0xa06ae0);
pub const PINK: Color = Color(0xe066a8);

#[derive(Clone, Copy, Default, Debug, PartialEq)]
pub struct Vec2 {
    pub x: f64,
    pub y: f64,
}
impl Vec2 {
    pub const fn new(x: f64, y: f64) -> Self {
        Self { x, y }
    }
    pub fn scale(self, factor: f64) -> Self {
        Self::new(self.x * factor, self.y * factor)
    }
    pub fn len(self) -> f64 {
        self.x.hypot(self.y)
    }
    pub fn rotate(self, theta: f64) -> Self {
        let (sin, cos) = theta.sin_cos();
        Self::new(self.x * cos - self.y * sin, self.x * sin + self.y * cos)
    }
}
impl Add for Vec2 {
    type Output = Self;
    fn add(self, other: Self) -> Self {
        Self::new(self.x + other.x, self.y + other.y)
    }
}
impl Sub for Vec2 {
    type Output = Self;
    fn sub(self, other: Self) -> Self {
        Self::new(self.x - other.x, self.y - other.y)
    }
}

#[repr(i32)]
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Key {
    None = 0,
    Left = 1,
    Right = 2,
    Up = 3,
    Down = 4,
    A = 5,
    B = 6,
    Start = 7,
}
impl Key {
    pub const fn from_i32(value: i32) -> Self {
        match value {
            1 => Self::Left,
            2 => Self::Right,
            3 => Self::Up,
            4 => Self::Down,
            5 => Self::A,
            6 => Self::B,
            7 => Self::Start,
            _ => Self::None,
        }
    }
}
pub struct KeyTracker {
    last_press: [Option<u64>; 8],
    down: [bool; 8],
    frame: u64,
    hold_ticks: u64,
    exact: bool,
}
impl KeyTracker {
    pub fn new(hold_ticks: u64) -> Self {
        Self {
            last_press: [None; 8],
            down: [false; 8],
            frame: 0,
            hold_ticks,
            exact: false,
        }
    }
    pub fn press(&mut self, key: Key) {
        self.last_press[key as usize] = Some(self.frame);
        self.down[key as usize] = true;
    }
    pub fn release(&mut self, key: Key) {
        self.exact = true;
        self.down[key as usize] = false;
    }
    pub fn tick(&mut self) {
        self.frame = self.frame.wrapping_add(1);
    }
    pub fn held(&self, key: Key) -> bool {
        if self.exact {
            self.down[key as usize]
        } else {
            self.last_press[key as usize]
                .is_some_and(|frame| self.frame.wrapping_sub(frame) < self.hold_ticks)
        }
    }
    pub fn exact(&self) -> bool {
        self.exact
    }
    pub fn reset(&mut self) {
        self.last_press.fill(None);
        self.down.fill(false);
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Info {
    pub id: String,
    pub title: String,
    pub pixel_w: usize,
    pub pixel_h: usize,
}
#[repr(i32)]
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Status {
    Running = 0,
    GameOver = 1,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct HudField {
    #[serde(rename = "Label", alias = "label", deserialize_with = "null_default")]
    pub label: String,
    #[serde(rename = "Value", alias = "value", deserialize_with = "null_default")]
    pub value: String,
    #[serde(rename = "Accent", alias = "accent", deserialize_with = "null_default")]
    pub accent: bool,
}
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Hud {
    #[serde(rename = "Fields", alias = "fields", deserialize_with = "hud_fields")]
    pub fields: Vec<HudField>,
    #[serde(rename = "Hint", alias = "hint", deserialize_with = "null_default")]
    pub hint: String,
}

fn null_default<'de, D, T>(decoder: D) -> Result<T, D::Error>
where
    D: serde::Deserializer<'de>,
    T: Deserialize<'de> + Default,
{
    Option::<T>::deserialize(decoder).map(Option::unwrap_or_default)
}
fn hud_fields<'de, D: serde::Deserializer<'de>>(decoder: D) -> Result<Vec<HudField>, D::Error> {
    Option::<Vec<Option<HudField>>>::deserialize(decoder).map(|fields| {
        fields
            .unwrap_or_default()
            .into_iter()
            .map(Option::unwrap_or_default)
            .collect()
    })
}

/// The simulation advances once per fixed 1/60-second tick. Rendering never
/// changes this clock. Terminal presentation and sandboxing belong to the host.
pub trait Game {
    fn info(&self) -> Info;
    fn reset(&mut self);
    fn handle_key(&mut self, key: Key);
    fn handle_key_up(&mut self, key: Key);
    fn update(&mut self) -> Status;
    fn draw(&mut self, canvas: &mut Canvas);
    fn score(&self) -> i64;
    fn hud(&self) -> Hud;
}
