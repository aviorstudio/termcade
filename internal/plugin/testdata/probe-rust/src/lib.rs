use termcade_sdk::*;

#[derive(Default)]
struct Probe {
    ticks: i32,
    score: i64,
    pressed: bool,
}
impl Game for Probe {
    fn info(&self) -> Info {
        Info {
            id: "test/probe".into(),
            title: "PROBE".into(),
            pixel_w: 16,
            pixel_h: 8,
        }
    }
    fn reset(&mut self) {
        *self = Self::default();
    }
    fn handle_key(&mut self, key: Key) {
        if key == Key::A {
            self.pressed = true;
            self.score += 10;
        }
    }
    fn handle_key_up(&mut self, key: Key) {
        if key == Key::A {
            self.pressed = false;
        }
    }
    fn update(&mut self) -> Status {
        self.ticks += 1;
        if self.ticks >= 30 {
            Status::GameOver
        } else {
            Status::Running
        }
    }
    fn draw(&mut self, canvas: &mut Canvas) {
        for index in 0..self.ticks.min(16) {
            canvas.set_pixel(i64::from(index), 0, WHITE);
        }
        if self.pressed {
            canvas.set_pixel(0, 1, WHITE);
        }
    }
    fn score(&self) -> i64 {
        self.score
    }
    fn hud(&self) -> Hud {
        Hud {
            fields: vec![HudField {
                label: "SCORE".into(),
                value: self.score.to_string(),
                accent: true,
            }],
            hint: "probe".into(),
        }
    }
}
termcade_sdk::termcade_game!(Probe::default);
