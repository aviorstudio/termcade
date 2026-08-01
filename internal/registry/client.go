// Package registry is the termcade.com client: marketplace catalog, package
// resolution, account auth, and the user's library.
//
// The registry stores no packages. It is an index that answers "where does
// this version live, and what must it hash to" — the bytes come from the
// GitHub release the author published, and are verified here against the
// digest the registry recorded when it validated them. Browsing and
// installing are anonymous; account operations send the session token.
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

// DefaultURL is the development default. Set TERMCADE_REGISTRY (or log in
// against another registry) once termcade.com is live.
const DefaultURL = "http://127.0.0.1:8080"

// maxPackageSize matches the registry's publish-time ceiling.
const maxPackageSize = 64 << 20

// ErrLoginRequired distinguishes "you need an account" from real failures.
var ErrLoginRequired = errors.New("login required")

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

// Resolved is where one release actually lives and what it must hash to. The
// registry hosts no packages: it hands out a GitHub asset URL plus the digest
// it recorded when it fetched and validated that package itself.
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
		return fmt.Errorf("registry unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrLoginRequired
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var msg apiMessage
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		if json.Unmarshal(raw, &msg) == nil && msg.Message != "" {
			return fmt.Errorf("registry: %s", msg.Message)
		}
		return fmt.Errorf("registry: HTTP %d", resp.StatusCode)
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

func (c *Client) Game(author, slug string) (Game, error) {
	var g Game
	return g, c.do(http.MethodGet, "/v1/games/"+author+"/"+slug, nil, &g)
}

// Resolve asks the registry where a version lives. An empty version means
// the newest one.
//
// The ABI this arcade speaks goes with the request, so the registry can pick
// the newest release this binary can actually run rather than the newest one
// that exists. Without it a game that has moved on to a later ABI would
// resolve, download, and only then be refused by the host.
func (c *Client) Resolve(author, slug, version string) (Resolved, error) {
	q := url.Values{}
	q.Set("abi", strconv.Itoa(sdk.ABIVersion))
	if version != "" {
		q.Set("version", version)
	}
	var out Resolved
	return out, c.do(http.MethodGet, "/v1/games/"+author+"/"+slug+"/resolve?"+q.Encode(), nil, &out)
}

// Download resolves a game and fetches its package to a temp file. The caller
// removes the returned path.
func (c *Client) Download(author, slug string) (string, error) {
	resolved, err := c.Resolve(author, slug, "")
	if err != nil {
		return "", err
	}
	return c.Fetch(resolved, slug)
}

// Fetch downloads a resolved package and verifies it against the digest the
// registry attested.
//
// The bytes come from GitHub, not from the registry, so the digest is the
// only thing tying what arrives to what the registry reviewed — a release
// asset can be deleted and re-uploaded under the same tag. Unlike the old
// download path, where the checksum was a header the sender could simply
// omit, a mismatch or a missing digest here is fatal.
//
// Nothing authenticates this request: the registry token is for the registry,
// and must not be sent to a third-party host.
func (c *Client) Fetch(resolved Resolved, slug string) (string, error) {
	if !strings.HasPrefix(resolved.URL, "https://") {
		return "", fmt.Errorf("registry returned a non-https package url for %s", resolved.ID)
	}
	if len(resolved.SHA256) != 64 {
		return "", fmt.Errorf("registry published no checksum for %s %s", resolved.ID, resolved.Version)
	}

	resp, err := c.http.Get(resolved.URL)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", resolved.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: %s", resolved.URL, resp.Status)
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
}

func (c *Client) Login(email, password string) (Session, error) {
	var out Session
	err := c.do(http.MethodPost, "/v1/auth/login", credentials{email, password}, &out)
	if errors.Is(err, ErrLoginRequired) {
		return Session{}, errors.New("wrong email or password")
	}
	if err != nil {
		return Session{}, err
	}
	out.Registry = c.baseURL
	return out, nil
}

func (c *Client) Signup(email, password string) (Session, error) {
	var out Session
	if err := c.do(http.MethodPost, "/v1/auth/signup", credentials{email, password}, &out); err != nil {
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

func (c *Client) Library() ([]Game, error) {
	var games []Game
	return games, c.do(http.MethodGet, "/v1/library", nil, &games)
}
