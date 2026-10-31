use termcade_sdk::{BLACK, Canvas, CellShape, RED};
#[test]
fn browser_framebuffers_keep_logical_coordinates_above_terminal_density() {
    for scale in [4, 8, 16, 32] {
        let mut canvas = Canvas::new(16, 8, BLACK, CellShape::host(scale, scale));
        assert_eq!(canvas.pixel_size(), (16 * scale, 8 * scale / 2));
        canvas.fill_rect_f(1., 1., 2., 3., RED);
        assert_eq!(canvas.at_pixel(scale as i64, (scale / 2) as i64), RED);
        assert_eq!(
            canvas.at_pixel((3 * scale - 1) as i64, (4 * scale / 2 - 1) as i64),
            RED
        );
        assert_eq!(
            canvas.at_pixel((scale - 1) as i64, (scale / 2) as i64),
            BLACK
        );
        assert_eq!(
            canvas.at_pixel((3 * scale) as i64, (scale / 2) as i64),
            BLACK
        );
    }
}
