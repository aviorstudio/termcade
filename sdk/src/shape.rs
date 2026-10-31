#[derive(Clone, Debug)]
pub struct CellShape {
    pub name: String,
    pub cols: usize,
    pub rows: usize,
    pub glyphs: Vec<char>,
    pub ink: bool,
}
impl CellShape {
    pub fn half() -> Self {
        Self {
            name: "half".into(),
            cols: 1,
            rows: 2,
            glyphs: " ▀▄█".chars().collect(),
            ink: false,
        }
    }
    pub fn quadrant() -> Self {
        Self {
            name: "quad".into(),
            cols: 2,
            rows: 2,
            glyphs: " ▘▝▀▖▌▞▛▗▚▐▜▄▙▟█".chars().collect(),
            ink: false,
        }
    }
    pub fn sextant() -> Self {
        let mut glyphs = vec![' '; 64];
        glyphs[0b010101] = '▌';
        glyphs[0b101010] = '▐';
        glyphs[63] = '█';
        let mut rune = 0x1fb00;
        for (mask, glyph) in glyphs.iter_mut().enumerate().take(63).skip(1) {
            if mask == 0b010101 || mask == 0b101010 {
                continue;
            }
            *glyph = char::from_u32(rune).expect("sextant codepoint");
            rune += 1;
        }
        Self {
            name: "sextant".into(),
            cols: 2,
            rows: 3,
            glyphs,
            ink: false,
        }
    }
    pub fn ascii() -> Self {
        let ramp: Vec<_> = " .:-=+*#%@".chars().collect();
        let mut glyphs: Vec<_> = (0_u16..512)
            .map(|mask| ramp[mask.count_ones() as usize])
            .collect();
        for (mask, glyph) in glyphs.iter_mut().enumerate().skip(1) {
            if let Some([a, b, c]) = one_per_column(mask) {
                if a == b && b == c {
                    *glyph = ['"', '-', '_'][a];
                } else if a <= b && b <= c {
                    *glyph = '\\';
                } else if a >= b && b >= c {
                    *glyph = '/';
                }
            } else if let Some([a, b, c]) = one_per_row(mask) {
                if a == b && b == c {
                    *glyph = '|';
                } else if a <= b && b <= c {
                    *glyph = '\\';
                } else if a >= b && b >= c {
                    *glyph = '/';
                }
            }
        }
        glyphs[1] = '`';
        glyphs[1 << 2] = '\'';
        glyphs[1 << 6] = ',';
        glyphs[1 << 8] = '.';
        Self {
            name: "ascii".into(),
            cols: 3,
            rows: 3,
            glyphs,
            ink: true,
        }
    }
    pub fn lookup(name: &str) -> Option<Self> {
        match name.trim().to_ascii_lowercase().as_str() {
            "half" | "halfblock" => Some(Self::half()),
            "quad" | "quadrant" => Some(Self::quadrant()),
            "sextant" | "six" => Some(Self::sextant()),
            "ascii" => Some(Self::ascii()),
            _ => None,
        }
    }
    pub fn host(cols: usize, rows: usize) -> Self {
        Self {
            name: "host".into(),
            cols,
            rows,
            glyphs: Vec::new(),
            ink: false,
        }
    }
}
fn one_per_column(mask: usize) -> Option<[usize; 3]> {
    let mut rows = [0; 3];
    for (column, row) in rows.iter_mut().enumerate() {
        *row = match (mask >> column) & 0b001001001 {
            1 => 0,
            8 => 1,
            64 => 2,
            _ => return None,
        };
    }
    Some(rows)
}
fn one_per_row(mask: usize) -> Option<[usize; 3]> {
    let mut columns = [0; 3];
    for (row, column) in columns.iter_mut().enumerate() {
        *column = match (mask >> (row * 3)) & 7 {
            1 => 0,
            2 => 1,
            4 => 2,
            _ => return None,
        };
    }
    Some(columns)
}
