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
			return errors.New(msg.Message)
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

func (c *Client) Games() ([]Game, error) {
	var games []Game
	return games, c.do(http.MethodGet, "/v1/games", nil, &games)
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
