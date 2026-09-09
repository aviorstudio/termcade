# TUI workflows

The terminal application follows the web product's destinations and information
hierarchy, while keeping terminal-cell graphics and keyboard input. The web app,
backend behavior, game ABI, and sandbox limits are not changed by this shell.

## Navigation and focus

- Marketplace is the initial destination, including while signed out.
- Tab/Shift+Tab cycles navigation, content, and actions. Arrows select within the
  focused area; Enter activates. A normal game row opens details; Continue
  Playing and the explicit Play action launch the game.
- At 120 or more columns the sidebar takes 22 columns. At smaller widths it is
  an on-demand overlay. Non-game content is centered and capped at 100 columns.
- The supported baseline is 80×24. Lists and long text scroll. Page Up/Down
  moves list selection with the viewport, so actions do not target a hidden row.
- During active gameplay, keys belong to the game. Esc/P pauses; Tab can then
  focus navigation. Leaving Play closes the guest and records an abandoned run.
- Pause retains Resume, Restart, pixel selection for the next start, and Leave
  Play. Pixel changes do not stretch or mutate an existing guest framebuffer.
- Forms consume typed/pasted characters. Tab moves between fields/buttons;
  left/right, Home/End, Backspace/Delete and Ctrl+U edit a field. Enter activates
  a button rather than silently submitting from a text field.
- Ctrl+C quits from every state. Escape dismisses/backtracks outside gameplay.

## Library and package actions

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
