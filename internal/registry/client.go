// Package registry is the termcade.com client: marketplace catalog, account
// auth, and the user's library. Browsing is anonymous; everything else sends
// the session token.
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
	"os"
	"strings"
	"time"
)

// DefaultURL is the development default. Set TERMCADE_REGISTRY (or log in
// against another registry) once termcade.com is live.
const DefaultURL = "http://127.0.0.1:8080"

// ErrLoginRequired distinguishes "you need an account" from real failures.
var ErrLoginRequired = errors.New("login required")

// Game is one marketplace catalog entry.
type Game struct {
	ID          string `json:"id"` // "author/slug"
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	ABI         int    `json:"abi"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	HasPackage  bool   `json:"has_package"`
	SHA256      string `json:"sha256"`
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

// Download fetches a game's .tcade to a temp file, verifying the registry's
// checksum when one is published. The caller removes the returned path.
func (c *Client) Download(author, slug string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/v1/games/"+author+"/"+slug+"/download", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("registry unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrLoginRequired
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry: download failed with HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", slug+"-*.tcade")
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, digest), resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}

	if want := resp.Header.Get("X-Checksum-Sha256"); want != "" {
		if got := hex.EncodeToString(digest.Sum(nil)); got != want {
			os.Remove(tmp.Name())
			return "", fmt.Errorf("checksum mismatch: registry says %s, download is %s", want, got)
		}
	}
	return tmp.Name(), nil
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
