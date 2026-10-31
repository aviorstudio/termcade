use crate::{CellShape, Color, Vec2};
use std::fmt::Write;

/// A terminal cell after palette reduction, ready for Ratatui or ANSI output.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Cell {
    pub glyph: char,
    pub foreground: Color,
    pub background: Color,
}
pub struct Canvas {
    pub w: usize,
    pub h: usize,
    shape: CellShape,
    sx: f64,
    sy: f64,
    fw: usize,
    fh: usize,
    pixels: Vec<Color>,
    background: Color,
}
impl Canvas {
    pub fn new(w: usize, h: usize, background: Color, shape: CellShape) -> Self {
        assert!(
            w > 0 && h > 0 && h.is_multiple_of(2),
            "canvas needs a positive width and positive even height"
        );
        assert!(
            shape.cols > 0 && shape.rows > 0 && shape.cols * shape.rows <= 9,
            "invalid cell subdivision"
        );
        let fw = w.checked_mul(shape.cols).expect("canvas width overflow");
        let fh = h.checked_mul(shape.rows).expect("canvas height overflow") / 2;
        let count = fw.checked_mul(fh).expect("canvas size overflow");
        Self {
            w,
            h,
            sx: shape.cols as f64,
            sy: shape.rows as f64 / 2.0,
            shape,
            fw,
            fh,
            pixels: vec![background; count],
            background,
        }
    }
    pub fn shape(&self) -> &CellShape {
        &self.shape
    }
    pub fn pixel_size(&self) -> (usize, usize) {
        (self.fw, self.fh)
    }
    pub fn pixels(&self) -> &[Color] {
        &self.pixels
    }
    pub fn pixels_mut(&mut self) -> &mut [Color] {
        &mut self.pixels
    }
    pub fn clear(&mut self) {
        self.pixels.fill(self.background);
    }
    pub fn set_pixel(&mut self, x: i64, y: i64, color: Color) {
        if x >= 0 && y >= 0 && (x as usize) < self.fw && (y as usize) < self.fh {
            self.pixels[y as usize * self.fw + x as usize] = color;
        }
    }
    pub fn at_pixel(&self, x: i64, y: i64) -> Color {
        if x < 0 || y < 0 || x as usize >= self.fw || y as usize >= self.fh {
            self.background
        } else {
            self.pixels[y as usize * self.fw + x as usize]
        }
    }
    pub fn set(&mut self, x: i32, y: i32, color: Color) {
        self.fill_rect_f(f64::from(x), f64::from(y), 1.0, 1.0, color);
    }
    pub fn at(&self, x: i32, y: i32) -> Color {
        if x < 0 || y < 0 || x as usize >= self.w || y as usize >= self.h {
            return self.background;
        }
        self.at_pixel(
            (f64::from(x) * self.sx).floor() as i64,
            (f64::from(y) * self.sy).floor() as i64,
        )
    }
    pub fn set_f(&mut self, x: f64, y: f64, color: Color) {
        self.set_pixel(
            (x * self.sx).floor() as i64,
            (y * self.sy).floor() as i64,
            color,
        );
    }
    pub fn fill_rect(&mut self, x: i32, y: i32, w: i32, h: i32, color: Color) {
        self.fill_rect_f(
            f64::from(x),
            f64::from(y),
            f64::from(w),
            f64::from(h),
            color,
        );
    }
    pub fn fill_rect_f(&mut self, x: f64, y: f64, w: f64, h: f64, color: Color) {
        let x0 = ((x * self.sx).floor() as i64).max(0) as usize;
        let y0 = ((y * self.sy).floor() as i64).max(0) as usize;
        let x1 = (((x + w) * self.sx).floor() as i64).clamp(0, self.fw as i64) as usize;
        let y1 = (((y + h) * self.sy).floor() as i64).clamp(0, self.fh as i64) as usize;
        for y in y0..y1 {
            for x in x0..x1 {
                self.pixels[y * self.fw + x] = color;
            }
        }
    }
    fn to_pixel(&self, x: f64, y: f64) -> (i64, i64) {
        (
            (x * self.sx + 0.5 * self.sx).floor() as i64,
            (y * self.sy + 0.5 * self.sy).floor() as i64,
        )
    }
    pub fn line(&mut self, x0: i32, y0: i32, x1: i32, y1: i32, color: Color) {
        self.line_f(
            f64::from(x0),
            f64::from(y0),
            f64::from(x1),
            f64::from(y1),
            color,
        );
    }
    pub fn line_f(&mut self, x0: f64, y0: f64, x1: f64, y1: f64, color: Color) {
        let ((mut x0, mut y0), (mut x1, mut y1)) = (self.to_pixel(x0, y0), self.to_pixel(x1, y1));
        if x0 > x1 || x0 == x1 && y0 > y1 {
            std::mem::swap(&mut x0, &mut x1);
            std::mem::swap(&mut y0, &mut y1);
        }
        let dx = (x1 - x0).abs();
        let dy = -(y1 - y0).abs();
        let sx = if x0 > x1 { -1 } else { 1 };
        let sy = if y0 > y1 { -1 } else { 1 };
        let mut error = dx + dy;
        loop {
            self.set_pixel(x0, y0, color);
            if x0 == x1 && y0 == y1 {
                break;
            }
            let twice = 2 * error;
            if twice >= dy {
                error += dy;
                x0 += sx;
            }
            if twice <= dx {
                error += dx;
                y0 += sy;
            }
        }
    }
    pub fn fill_circle(&mut self, cx: f64, cy: f64, radius: f64, color: Color) {
        let (rx, ry) = (radius * self.sx, radius * self.sy);
        let (cx, cy) = (cx * self.sx, cy * self.sy);
        for y in ((cy - ry).floor() as i64)..=((cy + ry).ceil() as i64) {
            let dy = (y as f64 + 0.5 - cy) / ry;
            for x in ((cx - rx).floor() as i64)..=((cx + rx).ceil() as i64) {
                let dx = (x as f64 + 0.5 - cx) / rx;
                if dx * dx + dy * dy <= 1.0 {
                    self.set_pixel(x, y, color);
                }
            }
        }
    }
    pub fn polyline(&mut self, points: &[Vec2], closed: bool, color: Color) {
        if points.len() < 2 {
            return;
        }
        for pair in points.windows(2) {
            self.line_f(pair[0].x, pair[0].y, pair[1].x, pair[1].y, color);
        }
        if closed {
            let first = points[0];
            let last = points[points.len() - 1];
            self.line_f(last.x, last.y, first.x, first.y, color);
        }
    }
    pub fn cell(&self, x: usize, y: usize) -> Cell {
        let mut buffer = [Color(0); 9];
        let mut n = 0;
        for row in 0..self.shape.rows {
            for column in 0..self.shape.cols {
                buffer[n] = self.pixels
                    [(y * self.shape.rows + row) * self.fw + x * self.shape.cols + column];
                n += 1;
            }
        }
        let first = buffer[0];
        let mut second = None;
        let mut crowded = false;
        for &color in &buffer[1..n] {
            if color == first {
                continue;
            }
            match second {
                None => second = Some(color),
                Some(other) if color != other => {
                    crowded = true;
                    break;
                }
                _ => {}
            }
        }
        let (foreground, background, mask) = if !crowded {
            match second {
                Some(second) => self.orient(&buffer[..n], first, second),
                None if self.shape.ink && first != self.background => {
                    (first, self.background, self.shape.glyphs.len() - 1)
                }
                None => (first, first, 0),
            }
        } else {
            let (mut a, mut b, mut best) = (0, 1, None);
            for i in 0..n {
                for j in i + 1..n {
                    let distance = color_distance(buffer[i], buffer[j]);
                    if best.is_none_or(|best| distance > best) {
                        a = i;
                        b = j;
                        best = Some(distance);
                    }
                }
            }
            self.orient(&buffer[..n], buffer[a], buffer[b])
        };
        Cell {
            glyph: self.shape.glyphs[mask],
            foreground,
            background,
        }
    }
    fn orient(&self, pixels: &[Color], x: Color, y: Color) -> (Color, Color, usize) {
        let (foreground, background) = if x == self.background { (y, x) } else { (x, y) };
        let mut mask = 0;
        for (index, &color) in pixels.iter().enumerate() {
            if color_distance(color, foreground) <= color_distance(color, background) {
                mask |= 1 << index;
            }
        }
        (foreground, background, mask)
    }
    /// ANSI output is also retained for standalone SDK users. The arcade draws
    /// the same cells into Ratatui's buffer instead of embedding escape strings.
    pub fn render(&self) -> String {
        let mut output = String::new();
        for y in 0..self.h / 2 {
            if y > 0 {
                output.push('\n');
            }
            let mut previous = None;
            for x in 0..self.w {
                let cell = self.cell(x, y);
                if previous != Some((cell.foreground, cell.background)) {
                    let (r, g, b) = cell.foreground.rgb();
                    let (br, bg, bb) = cell.background.rgb();
                    write!(output, "\x1b[38;2;{r};{g};{b};48;2;{br};{bg};{bb}m")
                        .expect("string write");
                    previous = Some((cell.foreground, cell.background));
                }
                output.push(cell.glyph);
            }
            output.push_str("\x1b[0m");
        }
        output
    }
}
fn color_distance(a: Color, b: Color) -> u32 {
    let (ar, ag, ab) = a.rgb();
    let (br, bg, bb) = b.rgb();
    let dr = i32::from(ar) - i32::from(br);
    let dg = i32::from(ag) - i32::from(bg);
    let db = i32::from(ab) - i32::from(bb);
    (dr * dr + dg * dg + db * db) as u32
}
