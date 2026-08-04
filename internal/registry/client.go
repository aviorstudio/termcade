// Package registry is the termca.de client: marketplace catalog, package
// resolution, account auth, and the user's library.
//
// The registry stores no packages. It is an index that answers "which release
// should I install, and what must it hash to" — the bytes originate from the
// GitHub release the author published, arrive through the registry, and are
// verified here against the digest it recorded when it validated them.
// Browsing is anonymous; installing and account operations send the session
// token.
package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aviorstudio/termcade/sdk"
)

// DefaultURL is the marketplace every released binary talks to.
//
// This was localhost until api.termca.de was deployed and answering, which was
// the stated condition for moving it: the value is compiled in, so pointing it
// at a host that is not up costs a release to undo. The host now serves.
//
// TERMCADE_REGISTRY overrides it, which is how you reach a local stack —
// `make dev` in termcade-be still listens on 127.0.0.1:8080.
const DefaultURL = "https://api.termca.de"

// maxPackageSize matches the registry's publish-time ceiling.
const maxPackageSize = 64 << 20

// ErrLoginRequired distinguishes "you need an account" from real failures.
var ErrLoginRequired = errors.New("login required")

// ErrNotFound lets callers distinguish an absent library row from a network
// failure without replacing the API's useful human-readable message.
var ErrNotFound = errors.New("not found")

type responseError struct {
	status  int
	message string
}

func (e responseError) Error() string { return e.message }

func (e responseError) Is(target error) bool {
	return target == ErrNotFound && e.status == http.StatusNotFound
}

// ErrUnreachable is the marketplace not answering at all: no network, or
// nothing listening where the registry is supposed to be.
//
// It carries no transport detail. Go's is accurate and useless to a player —
// `Get "http://127.0.0.1:8080/v1/games/aviorstudio/tetris/resolve?abi=1":
// dial tcp 127.0.0.1:8080: connect: connection refused` names a host, a port,
// a query string and a syscall, none of which anyone can act on, and the
// arcade renders it into a TUI notice. TERMCADE_DEBUG puts it back for
// whoever is actually debugging.
var ErrUnreachable = errors.New("the marketplace is not answering — check your connection and try again")

// unreachable wraps a transport failure as ErrUnreachable, keeping the
// original only when someone asked for it.
func unreachable(err error) error {
	if os.Getenv("TERMCADE_DEBUG") != "" {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	return ErrUnreachable
}

// Game is one marketplace catalog entry. The version and requirements are
// those of its newest release; a game with none reports has_package false.
type Game struct {
	ID          string `json:"id"` // "author/slug"
	Name        string `json:"name"`
	Description string `json:"description"`
	Repo        string `json:"repo"`
	Version     string `json:"version"`
	ABI         int    `json:"abi"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	HasPackage  bool   `json:"has_package"`
	SHA256      string `json:"sha256"`
	// RFC 3339. When the game entered the catalog, and when its newest release
	// was published; the second is absent on a game that has none.
	CreatedAt  string `json:"created_at,omitempty"`
	ReleasedAt string `json:"released_at,omitempty"`
}

// Resolved is which release to install and what it must hash to. The registry
// stores no packages — a game's releases live on its GitHub releases — but
// the bytes come back through the registry rather than from there, so URL is
// provenance rather than somewhere this client fetches from.
type Resolved struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Repo    string `json:"repo"`
	Tag     string `json:"tag"`
	Asset   string `json:"asset"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	ABI     int    `json:"abi"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}

// Session is a logged-in identity against a specific registry.
type Session struct {
	Registry string `json:"registry"`
	Email    string `json:"email"`
	Token    string `json:"token"`
	// Username is the handle this account publishes under. Empty is a real
	// state — an account whose signup lost a handle race still logs in — and
	// means publishing is refused until one is claimed.
	Username string `json:"username,omitempty"`
	// Notice is a server-side remark about an otherwise usable session. Not
	// persisted: it describes the moment the session was created.
	Notice string `json:"notice,omitempty"`
}

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// URL resolves the registry base URL: explicit env wins, then the session's
// registry (so a login sticks to the registry it happened against), then the
// default.
func URL(session *Session) string {
	if env := strings.TrimSpace(os.Getenv("TERMCADE_REGISTRY")); env != "" {
		return strings.TrimRight(env, "/")
	}
	if session != nil && session.Registry != "" {
		return session.Registry
	}
	return DefaultURL
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

type apiMessage struct {
	Message string `json:"message"`
}

func (c *Client) do(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return unreachable(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrLoginRequired
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// The registry writes its own messages for a person to read, so they
		// are passed through unprefixed — the CLI already says "termcade:",
		// and "termcade: registry: ..." is a stutter, not attribution.
		var msg apiMessage
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		if json.Unmarshal(raw, &msg) == nil && msg.Message != "" {
			return responseError{status: resp.StatusCode, message: msg.Message}
		}
		if resp.StatusCode >= 500 {
			return fmt.Errorf("the marketplace is having trouble (HTTP %d) — try again shortly", resp.StatusCode)
		}
		return fmt.Errorf("the marketplace refused that request (HTTP %d)", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// CatalogPage is one page of the marketplace and the cursor that continues it.
//
// Next empty is the end. A SHORT PAGE IS NOT: the registry applies its
// compatibility filter after reading a page, so a page can hold fewer games
// than asked for while the catalog continues. Stopping on a short page shows a
// truncated marketplace with nothing to say anything is missing.
type CatalogPage struct {
	Games []Game `json:"games"`
	Next  string `json:"next,omitempty"`
}

// CatalogQuery narrows a catalog request. The zero value asks for the first
// page of everything.
type CatalogQuery struct {
	Cursor string
	// Limit is 1-200; zero lets the registry choose.
	Limit int
	// Search matches a game's name, id or description.
	Search string
	// ABI restricts to games this arcade can run. Set by Games(); the registry
	// treats zero as "do not filter".
	ABI  int
	Sort string // "slug" (default) or "newest"
}

func (q CatalogQuery) values() url.Values {
	v := url.Values{}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Search != "" {
		v.Set("q", q.Search)
	}
	if q.ABI > 0 {
		v.Set("abi", strconv.Itoa(q.ABI))
	}
	if q.Sort != "" {
		v.Set("sort", q.Sort)
	}
	return v
}

// CatalogPage fetches one page of the marketplace.
func (c *Client) CatalogPage(q CatalogQuery) (CatalogPage, error) {
	path := "/v1/games"
	if encoded := q.values().Encode(); encoded != "" {
		path += "?" + encoded
	}
	var page CatalogPage
	return page, c.do(http.MethodGet, path, nil, &page)
}

// maxCatalogPages bounds how far Games will walk. The marketplace is a screen
// somebody scrolls, not an index anybody mirrors; at the registry's ceiling of
// 200 a page this is four thousand games, and a limit that is reached returns
// what it has rather than failing — a marketplace showing four thousand beats
// one showing an error.
const maxCatalogPages = 20

// Games lists the marketplace, following cursors to the end.
//
// The catalog is paged, so this is several requests rather than one. It asks
// for games this arcade can run: a marketplace full of entries that refuse to
// install is worse than a shorter one.
func (c *Client) Games() ([]Game, error) {
	var all []Game
	query := CatalogQuery{ABI: sdk.ABIVersion}
	for range maxCatalogPages {
		page, err := c.CatalogPage(query)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Games...)
		if page.Next == "" {
			return all, nil
		}
		query.Cursor = page.Next
	}
	return all, nil
}

// Resolve asks the registry which release to install: the newest one this
// arcade can run.
//
// There is no way to ask for an older version. A game is not a dependency —
// nothing builds against one — so the only version worth installing is what
// the author currently ships.
//
// The ABI this arcade speaks goes with the request, so the registry can pick
// the newest release this binary can actually run rather than the newest one
// that exists. Without it a game that has moved on to a later ABI would
// resolve, download, and only then be refused by the host.
func (c *Client) Resolve(author, slug string) (Resolved, error) {
	q := url.Values{}
	q.Set("abi", strconv.Itoa(sdk.ABIVersion))
	var out Resolved
	return out, c.do(http.MethodGet, "/v1/games/"+author+"/"+slug+"/resolve?"+q.Encode(), nil, &out)
}

// Download resolves a game and fetches its package to a temp file, verifying
// it against the digest the registry attested. The caller removes the
// returned path.
//
// The bytes come from the registry, not from GitHub. That is what lets an
// install require an account and lets the arcade and the app take the same
// path; the games are open source and their assets are public, so it is a
// product boundary rather than one that keeps anybody out.
//
// The digest still does real work: it is the tie between what arrives and
// what the registry reviewed at publish time, across a hop the registry does
// not control. A mismatch or a missing digest is fatal.
func (c *Client) Download(author, slug string) (string, error) {
	resolved, err := c.Resolve(author, slug)
	if err != nil {
		return "", err
	}
	if len(resolved.SHA256) != 64 {
		return "", fmt.Errorf("registry published no checksum for %s %s", resolved.ID, resolved.Version)
	}

	q := url.Values{}
	q.Set("abi", strconv.Itoa(sdk.ABIVersion))
	req, err := http.NewRequest(http.MethodGet,
		c.baseURL+"/v1/games/"+author+"/"+slug+"/download?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	if c.token != "" {
		req.Header.Set("Authorization", c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", unreachable(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrLoginRequired
	}
	if resp.StatusCode != http.StatusOK {
		var msg apiMessage
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		if json.Unmarshal(raw, &msg) == nil && msg.Message != "" {
			return "", fmt.Errorf("could not download %s: %s", resolved.ID, msg.Message)
		}
		if resp.StatusCode >= 500 {
			return "", fmt.Errorf("could not download %s: the marketplace is having trouble — try again shortly", resolved.ID)
		}
		return "", fmt.Errorf("could not download %s (HTTP %d)", resolved.ID, resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", slug+"-*.tcade")
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	// Bounded so a hostile or broken host cannot fill the disk; the registry
	// refuses to publish anything larger than this either.
	if _, err := io.Copy(io.MultiWriter(tmp, digest), io.LimitReader(resp.Body, maxPackageSize)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}

	if got := hex.EncodeToString(digest.Sum(nil)); got != resolved.SHA256 {
		os.Remove(tmp.Name())
		return "", fmt.Errorf(
			"checksum mismatch for %s %s: the registry recorded %s, the download is %s",
			resolved.ID, resolved.Version, resolved.SHA256, got)
	}
	return tmp.Name(), nil
}

// Published is the registry's answer to a publish: what it found at the
// coordinates given, as it recorded them.
type Published struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Repo    string `json:"repo"`
	Tag     string `json:"tag"`
	Asset   string `json:"asset"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// Publish claims that a .tcade is on a GitHub release. The registry fetches
// it and reads the game's identity and version out of the manifest inside —
// nothing here asserts what the package is, only where it is.
func (c *Client) Publish(repo, tag, asset string) (Published, error) {
	var out Published
	body := map[string]string{"repo": repo, "tag": tag, "asset": asset}
	return out, c.do(http.MethodPost, "/v1/publish", body, &out)
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// Username is sent on signup and omitted on login, where the account
	// already has one.
	Username string `json:"username,omitempty"`
}

func (c *Client) Login(email, password string) (Session, error) {
	var out Session
	err := c.do(http.MethodPost, "/v1/auth/login", credentials{Email: email, Password: password}, &out)
	if errors.Is(err, ErrLoginRequired) {
		return Session{}, errors.New("wrong email or password")
	}
	if err != nil {
		return Session{}, err
	}
	out.Registry = c.baseURL
	return out, nil
}

// Signup creates an account and claims its handle in one call. The handle is
// required: it is the author segment of every game this account publishes.
func (c *Client) Signup(email, password, username string) (Session, error) {
	var out Session
	body := credentials{Email: email, Password: password, Username: username}
	if err := c.do(http.MethodPost, "/v1/auth/signup", body, &out); err != nil {
		return Session{}, err
	}
	out.Registry = c.baseURL
	return out, nil
}

func (c *Client) LibraryAdd(author, slug string) error {
	return c.do(http.MethodPut, "/v1/library/"+author+"/"+slug, nil, nil)
}

func (c *Client) LibraryRemove(author, slug string) error {
	return c.do(http.MethodDelete, "/v1/library/"+author+"/"+slug, nil, nil)
}

// Library lists the games on this account, newest addition first. It is the
// server's copy: what you have added anywhere, not what is installed here.
func (c *Client) Library() ([]Game, error) {
	var games []Game
	return games, c.do(http.MethodGet, "/v1/library", nil, &games)
}

// Activity is what the account has done with one game, wherever it did it.
//
// It is a personal record and not a score anybody is ranked by: the registry
// stores what a client reported about a game it ran in its own sandbox, and
// cannot check any of it.
type Activity struct {
	ID           string `json:"id"` // "author/slug"
	PersonalBest int    `json:"personal_best"`
	Plays        int    `json:"plays"`
	LastPlayed   string `json:"last_played,omitempty"`  // RFC 3339
	LastVersion  string `json:"last_version,omitempty"` // package version of the newest play
	LastComplete string `json:"last_completed,omitempty"`
}

// PlayedAt parses LastPlayed, zero if there is none or it is unreadable. A
// timestamp the arcade cannot read is not worth failing a sync over.
func (a Activity) PlayedAt() time.Time {
	parsed, err := time.Parse(time.RFC3339, a.LastPlayed)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// Activity lists everything this account has played. It is not filtered to the
// library: removing a game does not erase its history, and a machine syncing
// for the first time wants all of it.
func (c *Client) Activity() ([]Activity, error) {
	var out []Activity
	return out, c.do(http.MethodGet, "/v1/activity", nil, &out)
}

// Run is one finished run, as this arcade reports it.
type Run struct {
	// Run identifies the run rather than the request. Reuse it for every
	// attempt to deliver one run, or the registry counts each attempt as
	// another play.
	Run       string `json:"run"`
	Score     int    `json:"score"`
	PlayedAt  string `json:"played_at,omitempty"` // RFC 3339
	Completed bool   `json:"completed"`
	Version   string `json:"version,omitempty"`
}

// SubmitRun records a run against the account and returns the merged record.
//
// Safe to retry: every field the registry stores is merged with max(), so a
// submission that arrives twice, out of order, or a week after the fact cannot
// lower what is already there.
func (c *Client) SubmitRun(author, slug string, run Run) (Activity, error) {
	var out Activity
	return out, c.do(http.MethodPost, "/v1/activity/"+author+"/"+slug, run, &out)
}

// Key is a publish credential. Token is set only by CreateKey, in the one
// response that carries it.
type Key struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	LastUsed string `json:"last_used_at,omitempty"`
	Created  string `json:"created_at"`
	Token    string `json:"token,omitempty"`
}

// CreateKey mints a publish key scoped to one handle.
func (c *Client) CreateKey(name, username string) (Key, error) {
	var out Key
	body := map[string]string{"name": name, "username": username}
	return out, c.do(http.MethodPost, "/v1/keys", body, &out)
}

func (c *Client) Keys() ([]Key, error) {
	var keys []Key
	return keys, c.do(http.MethodGet, "/v1/keys", nil, &keys)
}

func (c *Client) DeleteKey(id string) error {
	return c.do(http.MethodDelete, "/v1/keys/"+url.PathEscape(id), nil, nil)
}

// Org is a studio: a handle more than one person can publish under.
type Org struct {
	Username string   `json:"username"`
	Link     string   `json:"link,omitempty"`
	Bio      string   `json:"bio,omitempty"`
	Admin    bool     `json:"admin"`
	Games    []string `json:"games,omitempty"`
}

// Me is who the caller is, and every handle they may publish under. Clients
// read Handles to offer a choice at publish time rather than making an author
// guess which names are theirs.
type Me struct {
	Email    string   `json:"email"`
	Username string   `json:"username,omitempty"`
	Orgs     []Org    `json:"orgs"`
	Handles  []string `json:"handles"`
}

func (c *Client) Me() (Me, error) {
	var out Me
	return out, c.do(http.MethodGet, "/v1/me", nil, &out)
}

// CreateOrg creates a studio and claims its handle, with the caller as its
// first admin.
func (c *Client) CreateOrg(username, bio, link string) (Org, error) {
	var out Org
	body := map[string]string{"username": username, "bio": bio, "link": link}
	return out, c.do(http.MethodPost, "/v1/orgs", body, &out)
}

func (c *Client) Org(name string) (Org, error) {
	var out Org
	return out, c.do(http.MethodGet, "/v1/orgs/"+url.PathEscape(name), nil, &out)
}

// Member is one person in a studio. Email is present only to members: the
// public org page reports roles without addresses.
type Member struct {
	Email string `json:"email,omitempty"`
	Admin bool   `json:"admin"`
}

// Members lists who is in a studio. Membership is enough to read it —
// administering is what changing it needs.
func (c *Client) Members(org string) ([]Member, error) {
	var out []Member
	return out, c.do(http.MethodGet, "/v1/orgs/"+url.PathEscape(org)+"/members", nil, &out)
}

// AddMember adds somebody to a studio, or changes whether they administer it.
func (c *Client) AddMember(org, email string, admin bool) error {
	body := map[string]any{"email": email, "admin": admin}
	return c.do(http.MethodPost, "/v1/orgs/"+url.PathEscape(org)+"/members", body, nil)
}

func (c *Client) RemoveMember(org, email string) error {
	return c.do(http.MethodDelete,
		"/v1/orgs/"+url.PathEscape(org)+"/members/"+url.PathEscape(email), nil, nil)
}

// HandleOwner is what a handle resolves to. Public, because a namespace
// nobody can inspect is a namespace people collide with.
type HandleOwner struct {
	Name  string `json:"name"`
	IsOrg bool   `json:"is_org"`
}

// HandleTaken reports whether a handle is claimed, and by what kind of owner.
// A free handle is a 404 from the registry, which is an answer rather than a
// failure — so it comes back as ok=false, not an error.
func (c *Client) HandleTaken(name string) (owner HandleOwner, taken bool, err error) {
	err = c.do(http.MethodGet, "/v1/usernames/"+url.PathEscape(name), nil, &owner)
	if err != nil {
		if strings.Contains(err.Error(), "is available") {
			return HandleOwner{}, false, nil
		}
		return HandleOwner{}, false, err
	}
	return owner, true, nil
}

// SetUsername claims a handle, or renames the one this account holds.
//
// The registry refuses a rename once the handle has published games: packages
// assert their own id and players install to <author>/<slug>, so a rename
// would leave shipped packages claiming a name their author no longer holds.
func (c *Client) SetUsername(name string) (HandleOwner, error) {
	var out HandleOwner
	body := map[string]string{"username": name}
	return out, c.do(http.MethodPatch, "/v1/me/username", body, &out)
}

// DeleteAccount removes the caller's account. Refused while its handle holds
// published games, or while it is the only admin of a studio.
func (c *Client) DeleteAccount() error {
	return c.do(http.MethodDelete, "/v1/me", nil, nil)
}

// UpdateOrg edits a studio. Nil leaves a field alone, which is not the same as
// the empty string — clearing a bio is a real edit.
func (c *Client) UpdateOrg(name string, bio, link *string) (Org, error) {
	var out Org
	body := map[string]any{}
	if bio != nil {
		body["bio"] = *bio
	}
	if link != nil {
		body["link"] = *link
	}
	return out, c.do(http.MethodPatch, "/v1/orgs/"+url.PathEscape(name), body, &out)
}

// DeleteOrg dissolves a studio. Refused while its handle holds published games.
func (c *Client) DeleteOrg(name string) error {
	return c.do(http.MethodDelete, "/v1/orgs/"+url.PathEscape(name), nil, nil)
}
