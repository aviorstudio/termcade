use super::*;
use ratatui::{
    Frame,
    buffer::Buffer,
    layout::{Alignment, Constraint, Layout, Margin, Rect},
    style::{Color, Modifier, Style},
    text::{Line, Span, Text},
    widgets::{Block, Borders, Clear, List, ListItem, ListState, Paragraph, Widget, Wrap},
};
use unicode_width::UnicodeWidthStr;
const ACCENT: Color = Color::Rgb(63, 196, 201);
const MUTED: Color = Color::Rgb(138, 138, 138);
const INK: Color = Color::Rgb(242, 242, 242);
fn clean(value: &str) -> String {
    crate::terminal_text(value, 16000)
}
pub(super) fn page_title(page: &Page) -> String {
    match page {
        Page::Market => "Marketplace".into(),
        Page::Library => "Library".into(),
        Page::Game(id) => id.clone(),
        Page::Owner(id) => format!("@{id}"),
        Page::Activity(id) => format!("{id} · Activity"),
        Page::Settings => "Settings".into(),
        Page::Members(id) => format!("@{id} · Members"),
        Page::Sessions => "CLI sessions".into(),
        Page::Docs => "Docs".into(),
    }
}
fn border(title: impl Into<Line<'static>>, focused: bool) -> Block<'static> {
    Block::default()
        .borders(Borders::ALL)
        .title(title)
        .border_style(Style::default().fg(if focused { ACCENT } else { Color::DarkGray }))
}
fn selected() -> Style {
    Style::default()
        .fg(Color::Black)
        .bg(ACCENT)
        .add_modifier(Modifier::BOLD)
}
fn centered(area: Rect, width: u16, height: u16) -> Rect {
    let width = width.min(area.width);
    let height = height.min(area.height);
    Rect::new(
        area.x + (area.width - width) / 2,
        area.y + (area.height - height) / 2,
        width,
        height,
    )
}
fn list(
    frame: &mut Frame,
    items: &[Item],
    index: usize,
    area: Rect,
    title: String,
    focused: bool,
    details: bool,
) {
    let rows: Vec<_> = items
        .iter()
        .map(|item| {
            let label = Line::styled(clean(&item.label), Style::default().fg(INK));
            let mut text = vec![label];
            if details && !item.detail.is_empty() {
                for line in clean(&item.detail).lines().take(4) {
                    text.push(Line::styled(
                        format!("  {line}"),
                        Style::default().fg(MUTED),
                    ));
                }
                text.push(Line::from(""));
            }
            ListItem::new(text)
        })
        .collect();
    let widget = List::new(rows)
        .block(border(title, focused))
        .highlight_style(selected())
        .highlight_symbol(if focused { "› " } else { "  " });
    let mut state = ListState::default().with_selected(if items.is_empty() {
        None
    } else {
        Some(index.min(items.len() - 1))
    });
    frame.render_stateful_widget(widget, area, &mut state);
}
pub(super) fn draw(app: &mut App, frame: &mut Frame) {
    let area = frame.area();
    frame.render_widget(
        Block::default().style(Style::default().bg(Color::Black).fg(INK)),
        area,
    );
    if area.width < 20 || area.height < 8 {
        frame.render_widget(
            Paragraph::new("termcade\nResize terminal\nCtrl+C quits").alignment(Alignment::Center),
            area,
        );
        return;
    }
    let playing = app.mode != Mode::Browse;
    let [header, body, footer] = Layout::vertical([
        Constraint::Length(if playing { 1 } else { 2 }),
        Constraint::Min(1),
        Constraint::Length(if playing { 2 } else { 5 }),
    ])
    .areas(area);
    let status = if app.loading || app.search_loading {
        " · loading…"
    } else {
        ""
    };
    frame.render_widget(
        Paragraph::new(Line::from(vec![
            Span::styled(
                " termcade ",
                Style::default().fg(ACCENT).add_modifier(Modifier::BOLD),
            ),
            Span::raw(format!(" {}{status}", page_title(&app.page))),
        ]))
        .block(
            Block::default()
                .borders(Borders::BOTTOM)
                .border_style(Style::default().fg(Color::DarkGray)),
        ),
        header,
    );
    let show_nav =
        app.nav_visible && (area.width >= 100 || app.focus == 0) && app.mode == Mode::Browse;
    let content = if show_nav {
        let [nav, content] = Layout::horizontal([
            Constraint::Length(26.min(area.width / 2)),
            Constraint::Min(1),
        ])
        .areas(body);
        list(
            frame,
            &app.navigation(),
            app.nav_selection,
            nav,
            "Arcade".into(),
            app.focus == 0,
            false,
        );
        content
    } else {
        body
    };
    if app.game.is_some() && app.mode != Mode::Browse {
        game_view(app, frame, content);
    } else if let Page::Game(id) = &app.page {
        game_detail(app, frame, content, id);
    } else {
        let items = app.items();
        list(
            frame,
            &items,
            app.selection,
            content,
            page_title(&app.page),
            app.focus == 1,
            true,
        );
        if items.is_empty() {
            let empty = match &app.page {
                Page::Library => "No games added yet. Bundled games remain playable offline.",
                Page::Activity(_) => "No public play activity yet.",
                Page::Sessions => "No CLI sessions.",
                Page::Members(_) => "No members to display.",
                _ => {
                    if app.loading {
                        "Loading…"
                    } else {
                        "No games to display."
                    }
                }
            };
            frame.render_widget(
                Paragraph::new(empty)
                    .alignment(Alignment::Center)
                    .wrap(Wrap { trim: false }),
                content.inner(Margin::new(2, 2)),
            );
        }
    }
    let mut notice = app.notice.clone();
    if notice.is_empty() {
        notice = match app.page {
            Page::Market => app.snapshot.catalog_error.clone(),
            Page::Library => app.snapshot.library_error.clone(),
            Page::Settings => app.snapshot.account_error.clone(),
            _ => String::new(),
        };
    }
    let actions = app.actions();
    if playing {
        let hint = match app.mode {
            Mode::Playing => format!(
                "Esc / P pause · Ctrl+C quit · {}",
                app.game
                    .as_ref()
                    .map(|g| clean(&g.hud.hint))
                    .unwrap_or_default()
            ),
            Mode::Paused => "Enter select · Esc resume · Ctrl+N navigation · Ctrl+C quit".into(),
            Mode::GameOver => {
                "Enter / R play again · Esc / M Marketplace · Ctrl+N navigation · Ctrl+C quit"
                    .into()
            }
            _ => "Any key Marketplace · Ctrl+N navigation · Ctrl+C quit".into(),
        };
        frame.render_widget(
            Paragraph::new(hint)
                .style(Style::default().fg(MUTED))
                .block(
                    Block::default()
                        .borders(Borders::TOP)
                        .border_style(Style::default().fg(Color::DarkGray)),
                ),
            footer,
        );
    } else {
        let controls = if app.focus == 2 && !actions.is_empty() {
            let at = app.action_selection.min(actions.len() - 1);
            Line::styled(
                format!(
                    "‹ {} ›   {}/{}",
                    clean(&actions[at].label),
                    at + 1,
                    actions.len()
                ),
                selected(),
            )
        } else {
            Line::from(
                actions
                    .iter()
                    .map(|item| {
                        Span::styled(
                            format!(" {} ", clean(&item.label)),
                            Style::default().fg(ACCENT),
                        )
                    })
                    .collect::<Vec<_>>(),
            )
        };
        let foot = Text::from(vec![
            Line::styled(clean(&notice), Style::default().fg(Color::Yellow)),
            controls,
            Line::styled(
                "Tab / Shift+Tab focus · ↑/↓ select · Enter activate · Ctrl+N navigation · Ctrl+C quit",
                Style::default().fg(MUTED),
            ),
        ]);
        frame.render_widget(
            Paragraph::new(foot).wrap(Wrap { trim: false }).block(
                Block::default()
                    .borders(Borders::TOP)
                    .border_style(Style::default().fg(Color::DarkGray)),
            ),
            footer,
        );
    }
    if app.game_nav && playing {
        let nav = Rect::new(body.x, body.y, 26.min(body.width), body.height);
        frame.render_widget(Clear, nav);
        list(
            frame,
            &app.navigation(),
            app.nav_selection,
            nav,
            "Arcade".into(),
            true,
            false,
        );
    }
    if app.search_focus && app.page == Page::Market {
        frame.render_widget(
            Paragraph::new(format!("Search: {}█", clean(&app.query)))
                .style(Style::default().fg(ACCENT)),
            Rect::new(
                content.x + 2,
                content.y + 1,
                content.width.saturating_sub(4),
                1,
            ),
        );
    }
    if let Some(form) = &app.form {
        form_view(app, form, frame, area);
    }
    if let Some(pair) = &app.pairing {
        pairing_view(app, pair, frame, area);
    }
    if !app.game_nav
        && app.pairing.is_none()
        && matches!(app.mode, Mode::Paused | Mode::GameOver | Mode::Crashed)
    {
        overlay(app, frame, area);
    }
}
fn game_detail(app: &App, frame: &mut Frame, area: Rect, id: &str) {
    let Some(game) = app.metadata(id) else {
        frame.render_widget(
            Paragraph::new(if app.loading {
                "Loading game…"
            } else {
                "Game metadata is unavailable. Refresh or go back."
            })
            .block(border(id.to_string(), false)),
            area,
        );
        return;
    };
    let local = app.local(id);
    let status = if let Some(local) = local {
        if local.error.is_empty() {
            format!("Installed {}", local.version())
        } else {
            format!("Broken package: {}", local.error)
        }
    } else if app.in_library(id) {
        "In your Library · Play installs on this machine".into()
    } else {
        "Add to your Library to play".into()
    };
    let activity = app.snapshot.activity.iter().find(|a| a.id == id);
    let mut text = vec![
        Line::styled(
            clean(&game.name),
            Style::default().fg(ACCENT).add_modifier(Modifier::BOLD),
        ),
        Line::from(clean(id)),
        Line::from(""),
        Line::from(clean(&game.description)),
        Line::from(""),
        Line::from(format!(
            "{} · {} likes · ABI {} · {} × {}",
            clean(&game.version),
            game.likes,
            game.abi,
            game.width,
            game.height
        )),
        Line::from(clean(&status)),
        Line::from(format!("Personal best here: {}", app.scores.high(id))),
    ];
    if let Some(activity) = activity {
        text.push(Line::from(format!(
            "Account best: {} · {} plays · last {}",
            activity.personal_best,
            activity.plays,
            clean(&activity.last_played)
        )));
    }
    if let Some(manifest) = local.and_then(|g| g.manifest.as_ref()) {
        text.push(Line::from(""));
        for (key, value) in &manifest.controls {
            text.push(Line::from(format!("{}  {}", clean(key), clean(value))));
        }
    }
    if !game.repo.is_empty() {
        text.push(Line::from(""));
        text.push(Line::from(format!("Repository: {}", clean(&game.repo))));
    }
    let total: i64 = app.days.iter().map(|d| d.count.max(0)).sum();
    text.push(Line::from(""));
    text.push(Line::from(format!(
        "Public activity: {total} plays this year · C for chart"
    )));
    let width = area.width.min(100);
    let panel = Rect::new(
        area.x + (area.width - width) / 2,
        area.y,
        width,
        area.height,
    );
    frame.render_widget(
        Paragraph::new(text)
            .alignment(Alignment::Center)
            .wrap(Wrap { trim: false })
            .scroll((app.scroll, 0))
            .block(border("Game", app.focus == 1)),
        panel,
    );
}
struct Pixels<'a>(&'a Canvas);
impl Widget for Pixels<'_> {
    fn render(self, area: Rect, buf: &mut Buffer) {
        for y in 0..area.height.min((self.0.h / 2) as u16) {
            for x in 0..area.width.min(self.0.w as u16) {
                let cell = self.0.cell(x as usize, y as usize);
                let (fr, fg, fb) = cell.foreground.rgb();
                let (br, bg, bb) = cell.background.rgb();
                buf[(area.x + x, area.y + y)]
                    .set_char(cell.glyph)
                    .set_fg(Color::Rgb(fr, fg, fb))
                    .set_bg(Color::Rgb(br, bg, bb));
            }
        }
    }
}
fn game_view(app: &App, frame: &mut Frame, area: Rect) {
    let Some(game) = &app.game else {
        return;
    };
    let width = game.canvas.w as u16 + 2;
    let height = (game.canvas.h / 2) as u16 + 1;
    if area.width < width || area.height < height {
        frame.render_widget(
            Paragraph::new(format!(
                "Resize to at least {} × {} to play\nEsc pauses",
                width,
                height + 3
            ))
            .alignment(Alignment::Center),
            area,
        );
        return;
    }
    let panel = centered(area, width, height);
    let block = Block::default()
        .borders(Borders::LEFT | Borders::RIGHT)
        .border_style(Style::default().fg(ACCENT));
    let inner = block.inner(panel);
    frame.render_widget(block, panel);
    let hud = game
        .hud
        .fields
        .iter()
        .map(|field| format!("{} {}", clean(&field.label), clean(&field.value)))
        .collect::<Vec<_>>()
        .join("   ");
    frame.render_widget(
        Paragraph::new(hud)
            .style(Style::default().fg(ACCENT))
            .alignment(Alignment::Center),
        Rect::new(inner.x, inner.y, inner.width, 1),
    );
    frame.render_widget(
        Pixels(&game.canvas),
        Rect::new(
            inner.x,
            inner.y + 1,
            inner.width,
            (game.canvas.h / 2) as u16,
        ),
    );
}
fn modal(frame: &mut Frame, area: Rect, title: &str, width: u16, height: u16) -> Rect {
    let panel = centered(area, width, height);
    frame.render_widget(Clear, panel);
    let block = border(clean(title), true).style(Style::default().bg(Color::Black).fg(INK));
    let inner = block.inner(panel);
    frame.render_widget(block, panel);
    inner
}
fn small_dialog(frame: &mut Frame, area: Rect, title: &str) -> bool {
    if area.width < 40 || area.height < 20 {
        let inner = modal(frame, area, title, area.width, area.height);
        frame.render_widget(
            Paragraph::new("Resize to at least 40 × 20\nEsc cancel · Ctrl+C quit")
                .alignment(Alignment::Center),
            inner,
        );
        true
    } else {
        false
    }
}
fn form_view(app: &App, form: &Form, frame: &mut Frame, area: Rect) {
    if small_dialog(frame, area, &form.title) {
        return;
    }
    let inner = modal(
        frame,
        area,
        &form.title,
        76,
        (form.fields.len() as u16 * 3 + 8).min(area.height),
    );
    let [fields, notice, buttons] = Layout::vertical([
        Constraint::Min(1),
        Constraint::Length(2),
        Constraint::Length(2),
    ])
    .areas(inner);
    let field_areas =
        Layout::vertical(form.fields.iter().map(|_| Constraint::Length(3))).split(fields);
    for (i, (field, rect)) in form.fields.iter().zip(field_areas.iter()).enumerate() {
        let block = border(clean(&field.label), form.index == i);
        let input = block.inner(*rect);
        frame.render_widget(block, *rect);
        let before: String = field.value.chars().take(field.caret).collect();
        let caret = before.width() as u16;
        let offset = caret.saturating_sub(input.width.saturating_sub(1));
        frame.render_widget(
            Paragraph::new(clean(&field.value)).scroll((0, offset)),
            input,
        );
        if form.index == i && input.width > 0 && input.height > 0 {
            frame.set_cursor_position((
                input.x + caret.saturating_sub(offset).min(input.width - 1),
                input.y,
            ));
        }
    }
    frame.render_widget(
        Paragraph::new(clean(&app.notice))
            .wrap(Wrap { trim: false })
            .style(Style::default().fg(Color::Yellow)),
        notice,
    );
    let labels = if matches!(form.kind, FormKind::Username) {
        vec!["Submit", "Check availability", "Cancel"]
    } else {
        vec!["Confirm", "Cancel"]
    };
    frame.render_widget(
        Paragraph::new(Line::from(
            labels
                .iter()
                .enumerate()
                .map(|(i, label)| {
                    Span::styled(
                        format!(" [{label}] "),
                        if form.index == form.fields.len() + i {
                            selected()
                        } else {
                            Style::default().fg(ACCENT)
                        },
                    )
                })
                .collect::<Vec<_>>(),
        ))
        .alignment(Alignment::Center),
        buttons,
    );
}
fn pairing_view(app: &App, pair: &Pairing, frame: &mut Frame, area: Rect) {
    if small_dialog(frame, area, "Sign in") {
        return;
    }
    let inner = modal(frame, area, "Sign in", 76, 18);
    let content = vec![
        Line::from("Approve this terminal in your browser."),
        Line::from(""),
        Line::styled(clean(&pair.url), Style::default().fg(ACCENT)),
        Line::from(""),
        Line::styled(
            if pair.code.is_empty() {
                "Requesting a pairing code…".into()
            } else {
                pair.code.clone()
            },
            Style::default().fg(ACCENT).add_modifier(Modifier::BOLD),
        ),
        Line::from(""),
        Line::from("Waiting for approval. R requests a new code."),
        Line::from(""),
        Line::from(vec![
            Span::styled(
                " [Open browser] ",
                if pair.button == 0 {
                    selected()
                } else {
                    Style::default()
                },
            ),
            Span::styled(
                " [Cancel] ",
                if pair.button == 1 {
                    selected()
                } else {
                    Style::default()
                },
            ),
        ]),
        Line::from(""),
        Line::styled(clean(&app.notice), Style::default().fg(Color::Yellow)),
    ];
    frame.render_widget(
        Paragraph::new(content)
            .alignment(Alignment::Center)
            .wrap(Wrap { trim: false }),
        inner,
    );
}
fn overlay(app: &App, frame: &mut Frame, area: Rect) {
    let (title, lines) = match app.mode {
        Mode::Paused => (
            "Paused",
            vec![
                Line::from(""),
                Line::from(vec![
                    Span::styled(
                        " [Resume] ",
                        if app.pause_selection == 0 {
                            selected()
                        } else {
                            Style::default()
                        },
                    ),
                    Span::styled(
                        " [Exit] ",
                        if app.pause_selection == 1 {
                            selected()
                        } else {
                            Style::default()
                        },
                    ),
                ]),
                Line::from(""),
                Line::from("Tab selects · Enter activates · Esc resumes"),
            ],
        ),
        Mode::GameOver => (
            "Game over",
            vec![
                Line::from(format!(
                    "Score: {}",
                    app.game.as_ref().map(|g| g.score).unwrap_or(0)
                )),
                Line::from(if app.new_high {
                    "New personal best!"
                } else {
                    ""
                }),
                Line::from(""),
                Line::from("Enter / R restart · Esc / M return"),
            ],
        ),
        _ => (
            "Game stopped",
            vec![
                Line::from(clean(&app.crash)),
                Line::from(""),
                Line::from("Press any key to return to the arcade"),
            ],
        ),
    };
    let inner = modal(frame, area, title, 72, 10);
    frame.render_widget(
        Paragraph::new(lines)
            .wrap(Wrap { trim: false })
            .alignment(Alignment::Center),
        inner,
    );
}
