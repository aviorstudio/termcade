# TUI workflows

The terminal application follows the web product's destinations and information
hierarchy, while keeping terminal-cell graphics and keyboard input. The web app,
backend behavior, game ABI, and sandbox limits are not changed by this shell.

## Navigation and focus

- Marketplace is the initial destination, including while signed out.
- The navigation's Sign in / @username item is pinned at the bottom of the
  rail. Navigation has no local divider or key legend; the shared footer swaps
  to ↓ Tab · ↑ Shift+Tab · ✓ Enter · ✕ Esc while nav is focused. Library and installed
  offline games remain accessible
  while signed out; a long library scrolls without displacing the account row.
- Ctrl+N directly toggles navigation and page content, even if an action
  previously had focus. It never steps through the footer. Tab is next and
  Shift+Tab is previous within the focused list, action row, form, dialog, or
  pause menu. They do not open or close navigation. Arrows still select
  within the focused area; Enter activates. A
  normal game row opens details; Continue Playing and the explicit Play action
  launch the game.
- When open at 120 or more columns, navigation reserves 22 columns and pushes
  the content. On smaller terminals it overlays without reflowing the content.
  In both cases it stops above the main divider so page actions and keyboard
  hints stay fully visible. Closed navigation reserves no width. Non-game
  content stays centered and capped at 100 columns. If pushing would squeeze a
  fixed-size game, navigation uses the overlay instead, even on a wide terminal.
- The supported baseline is 80×24. Lists and long text scroll. Page Up/Down
  moves list selection with the viewport, so actions do not target a hidden row.
- During active gameplay, keys belong to the game. Esc/P pauses; Ctrl+N can then
  focus navigation. Leaving Play closes the guest and records an abandoned run.
- Pause retains Resume, Restart, pixel selection for the next start, and Leave
  Play. Pixel changes do not stretch or mutate an existing guest framebuffer.
- Forms consume typed/pasted characters. Tab moves between fields/buttons;
  left/right, Home/End, Backspace/Delete and Ctrl+U edit a field. Enter activates
  a button rather than silently submitting from a text field.
- Ctrl+C quits from every state. Escape dismisses/backtracks outside gameplay.
- Product pages/states share one bottom-anchored footer that spans the full
  terminal width. Its contents follow the focused context (navigation, page
  actions, forms, gameplay). Keyboard hints use ↓ Tab, ↑ Shift+Tab, ✓ Enter,
  and ✕ Esc. Marketplace/library game actions are keyed directly: ✕ Esc Back,
  ↻ R Refresh, ▶ P Play, + A Add, and ⌫ U Uninstall.
  Pause choices and game hints are in this footer rather than over the artwork.
  Footer rows are reserved before content is laid out; games keep their fixed
  cell dimensions and show a too-small notice rather than being cropped.

## Library and package actions

Marketplace and Library have a Search row at the top of the list. Tab/Shift+Tab
or arrows move onto it to type; `/` jumps there. Enter, Tab, or down moves to
results without clearing. Esc is still Back. Ctrl+U clears the query. Queries
survive visiting details or another destination during this TUI session.

Marketplace uses server name/slug/description search (not owner), debounced by
300ms, and follows cursors through the full result set. An API/network failure
shows an error rather than a complete-looking partial list. The API accepts at
most 64 UTF-8 bytes after trimming. Library immediately filters its view using
the same fields when metadata is available, including installed local-only games.
Neither sidebar games nor shared membership are filtered. Continue Playing is
hidden while the query is nonblank; an empty filtered list says No matching games.

Library is an ID-keyed union of account membership and installed packages.
Installed versions are shown independently of the newest registry metadata.

| Action | Account membership | Local package |
| --- | --- | --- |
| Add | Added | Unchanged |
| Play, healthy installed copy | Unchanged | Runs as-is, including offline |
| Play, missing account game | Unchanged | Downloaded, digest/identity/ABI validated, installed, launched |
| Remove from Library | Removed | Preserved |
| Uninstall here | Preserved | Removed after typed confirmation |

Account-only entries show Not installed. Local-only is asserted only when
membership is known; unavailable account reads show unknown state, and
signed-out copies are described as on this machine. Broken/incompatible packages
remain visible with an explanation instead of being treated as playable. The
TUI does not silently update or overwrite existing copies.

Sign-in loads account metadata and the library, not all packages. Existing
`termcade add/remove/login/sync` command-line semantics are unchanged.

## Details and settings

Owner/game pages expose public metadata, repository/website links, game actions,
and UTC year-to-date play counts. A seven-row cell heatmap uses the web's relative
intensity thresholds; Enter opens the scrollable exact daily counts. Public
aggregate activity is distinct from personal/machine-local high scores.

Settings provides handle availability/claim/rename, org creation, member
addition/role changes/removal, org dissolution, CLI login-session listing and
revocation, account deletion, and sign-out. The API remains authoritative for
permissions and refusal rules. Publish keys are not CLI login sessions.

Uninstall, membership removal from an org, org/account deletion and revocation
show explicit confirmations. Escape during a submitted mutation stops waiting
and refreshes actual state: a confirmed remote/local operation may already have
committed, so cancellation does not promise rollback.

New logins retain their credential ID and expiry in the atomic mode-0600 session
file. TUI sign-out attempts revocation and then clears the local credential even
when the remote call fails. Failures are reported honestly. Legacy sessions with
no ID clear locally with a warning; the TUI never guesses another session to
revoke. Account changes/expiry clear cached private member/session views.

## Local development pairing

Choose the bottom Sign in item (or Sign in within Settings) to open a centered
dialog over the current screen. The dialog title is Sign in. Below it, the
pairing URL, a blank line, the pairing code, and Open browser / Cancel options
are centered. The URL is `/pair/ABCD-EFGH` so the pair page can look the device
up automatically; it remains an OSC-8 hyperlink in terminals that support
clicks. Approval in the browser is still explicit. Open browser is explicit,
and passwords/email codes are never entered in the TUI. Tab or arrows select
those options; Enter activates. Ctrl+N does nothing in the dialog. Esc
closes it without navigating away. Successful sign-in also closes
it. A paused game remains
paused and open underneath. Very small terminals show a resize notice and
accept only Esc/Ctrl+C until controls fit. Status sits above the divider; below
it the hints are ↓ Tab, ↑ Shift+Tab, ✓ Enter, and ✕ Esc.
Cancel stops waiting; if credential saving has begun, the shell reconciles
actual state because a completed save cannot be undone by canceling its result.

Use the actual API/app ports printed by `make dev` in the backend checkout:

```sh
TERMCADE_REGISTRY=http://127.0.0.1:8080 \
TERMCADE_DEV_APP_URL=http://127.0.0.1:8081 \
mise exec -- go run .
```

`TERMCADE_DEV_APP_URL` is an explicit **TUI-only** local pairing opt-in. Both
origins must be HTTP with a port, have the same literal loopback host, and carry
no credentials, path, query or fragment. DNS names, LAN hosts and remote apps are
refused. The API's normal trusted pairing response is still validated; the
display/open target is the explicitly configured local `/pair` page.

Local credentials live under
`<config>/termcade/local/<registry-hash>/session.json`; this mode never loads the
normal production session file. Changing a registry override without matching
credentials cannot forward the old registry's credential. Bound TUI HTTP clients
also refuse cross-origin redirects.

The ordinary `termcade login` command and its production pairing policy remain
unchanged. If testing shell commands as well, use separate XDG_CONFIG_HOME and
XDG_DATA_HOME directories; do not mix production sessions into a local registry.
Changing the local API port selects a different local credential store.

## Verification

Use the pinned Go toolchain and the existing CI gates:

```sh
mise install
mise exec -- gofmt -l .
mise exec -- go vet ./...
mise exec -- go vet ./sdk/...
mise exec -- go test -race -count=1 -timeout 10m ./... ./sdk/...
GOWORK=off mise exec -- go build ./...
GOWORK=off mise exec -- go vet ./...
```

Formatting output must be empty. Cross-compile the targets and flags in
`.github/actions/build/action.yml` before a release. The product-model tests use
non-nil ProductServices and verify the real application path; older nil-service
shell tests remain controls for standalone game-loop behavior.

HTTP tests cover read contracts, cancellation, credential-origin isolation,
revocation, and the independent effects of package/library actions. Local PTY
walkthroughs must use isolated configuration/data and disposable local accounts,
never production credentials or account data.
