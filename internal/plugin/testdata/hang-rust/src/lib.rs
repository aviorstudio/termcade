use termcade_sdk::*;
struct Hang;
impl Game for Hang {
    fn info(&self) -> Info {
        Info {
            id: "test/hang".into(),
            title: "HANG".into(),
            pixel_w: 8,
            pixel_h: 8,
        }
    }
    fn reset(&mut self) {}
    fn handle_key(&mut self, _: Key) {}
    fn handle_key_up(&mut self, _: Key) {}
    fn update(&mut self) -> Status {
        loop {
            std::hint::spin_loop();
        }
    }
    fn draw(&mut self, _: &mut Canvas) {}
    fn score(&self) -> i64 {
        0
    }
    fn hud(&self) -> Hud {
        Hud::default()
    }
}
termcade_sdk::termcade_game!(|| Hang);
