use serde_json::Value;
use termcade_sdk::*;

#[test]
fn pixels_glyphs_quantization_and_ansi_match_independent_go_fixtures() {
    let data: Value = serde_json::from_str(include_str!("fixtures/go-render-v1.json")).unwrap();
    assert_eq!(data["baseline"], "9de13d1af144a89ba2ec1123b73b707a7f1bff60");
    for case in data["cases"].as_array().unwrap() {
        let shape = CellShape::lookup(case["shape"].as_str().unwrap()).unwrap();
        let name = case["case"].as_str().unwrap();
        if name == "masks" {
            assert_eq!(
                shape.glyphs.iter().collect::<String>(),
                case["glyphs"].as_str().unwrap()
            );
            for (mask, expected) in case["render_masks"].as_array().unwrap().iter().enumerate() {
                let mut canvas = Canvas::new(1, 2, BLACK, shape.clone());
                for index in 0..shape.cols * shape.rows {
                    if mask & (1 << index) != 0 {
                        canvas.set_pixel(
                            (index % shape.cols) as i64,
                            (index / shape.cols) as i64,
                            WHITE,
                        );
                    }
                }
                assert_eq!(
                    canvas.render(),
                    expected.as_str().unwrap(),
                    "{} mask {mask}",
                    shape.name
                );
            }
            continue;
        }
        let mut canvas = Canvas::new(12, 10, BLACK, shape.clone());
        match name {
            "geometry" => {
                canvas.set(-1, 0, WHITE);
                canvas.set(2, 1, RED);
                canvas.set_f(6.25, 1.5, CYAN);
                canvas.fill_rect_f(-1.25, 7.5, 5.75, 3.5, GREEN);
                canvas.line_f(-2.5, 1.2, 12.25, 7.75, WHITE);
                canvas.fill_circle(7.75, 4.25, 2.4, BLUE);
                canvas.polyline(
                    &[
                        Vec2::new(1.5, 3.25),
                        Vec2::new(5.75, 2.5),
                        Vec2::new(9.2, 8.7),
                    ],
                    true,
                    YELLOW,
                );
            }
            "busy" => {
                let (w, h) = canvas.pixel_size();
                let colors = [BLACK, WHITE, RED, GREEN, BLUE, YELLOW];
                for y in 0..h {
                    for x in 0..w {
                        canvas.set_pixel(
                            x as i64,
                            y as i64,
                            colors[(x * 7 + y * 3) % colors.len()],
                        );
                    }
                }
            }
            "uniform" => canvas.fill_rect(0, 0, 12, 10, CYAN),
            _ => panic!("unknown reference case"),
        }
        let expected: Vec<Color> = serde_json::from_value(case["pixels"].clone()).unwrap();
        assert_eq!(canvas.pixels(), expected, "{} {name} pixels", shape.name);
        assert_eq!(
            canvas.render(),
            case["render"].as_str().unwrap(),
            "{} {name} ANSI",
            shape.name
        );
        assert_eq!(
            canvas.pixel_size(),
            (
                case["width"].as_u64().unwrap() as usize,
                case["height"].as_u64().unwrap() as usize
            )
        );
        assert_eq!(canvas.pixel_size().0 / shape.cols, 12);
        assert_eq!(canvas.pixel_size().1 / shape.rows, 5);
    }
}

#[test]
fn input_fallback_expiry_and_discovered_release_mode_survive_reset() {
    let mut tracker = KeyTracker::new(5);
    assert!(!tracker.held(Key::Left));
    tracker.press(Key::Left);
    for _ in 0..4 {
        tracker.tick();
    }
    assert!(tracker.held(Key::Left));
    tracker.tick();
    assert!(!tracker.held(Key::Left));
    tracker.press(Key::Left);
    tracker.release(Key::Right);
    for _ in 0..120 {
        tracker.tick();
    }
    assert!(tracker.held(Key::Left));
    assert!(!tracker.held(Key::Right));
    assert!(tracker.exact());
    tracker.reset();
    assert!(tracker.exact());
    assert!(!tracker.held(Key::Left));
    tracker.press(Key::A);
    tracker.release(Key::A);
    assert!(!tracker.held(Key::A));
}

#[test]
fn vector_math_reverses_rotation_and_preserves_length() {
    let v = Vec2::new(3.0, 4.0);
    assert_eq!(v.len(), 5.0);
    let result = v.rotate(0.37).rotate(-0.37);
    assert!((v.x - result.x).abs() < 1e-12 && (v.y - result.y).abs() < 1e-12);
    assert_eq!((v + Vec2::new(2.0, 1.0)) - Vec2::new(2.0, 1.0), v);
    assert_eq!(v.scale(2.0).len(), 10.0);
}

#[test]
fn legacy_go_null_hud_fields_preserve_a_hint_and_zero_value_entries() {
    let hud: Hud = serde_json::from_str(r#"{"Fields":null,"Hint":"move left"}"#).unwrap();
    assert!(hud.fields.is_empty());
    assert_eq!(hud.hint, "move left");
    let hud: Hud = serde_json::from_str(
        r#"{"Fields":[null,{"Label":null,"Value":"10","Accent":null}],"Hint":null}"#,
    )
    .unwrap();
    assert_eq!(hud.fields.len(), 2);
    assert_eq!(hud.fields[1].value, "10");
    assert!(hud.fields[1].label.is_empty());
    assert!(!hud.fields[1].accent);
    assert!(hud.hint.is_empty());
}
