package registry

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// TUIStore binds credentials to one registry. Explicit local development never
// reads the normal production session file, and cannot select a remote app.
type TUIStore struct {
	Registry   string
	PairingURL string
	Path       string
}

func NewTUIStore() (TUIStore, error) {
	base := URL(nil)
	if err := validateTUIRegistry(base); err != nil {
		return TUIStore{}, err
	}
	path, err := sessionPath()
	if err != nil {
		return TUIStore{}, err
	}
	if localApp := strings.TrimRight(os.Getenv("TERMCADE_DEV_APP_URL"), "/"); localApp != "" {
		a, err := loopbackOrigin(base)
		if err != nil {
			return TUIStore{}, err
		}
		b, err := loopbackOrigin(localApp)
		if err != nil || a.Hostname() != b.Hostname() {
			return TUIStore{}, fmt.Errorf("local registry and app must use the same literal loopback host")
		}
		sum := sha256.Sum256([]byte(base))
		path = filepath.Join(filepath.Dir(path), "local", fmt.Sprintf("%x", sum[:12]), "session.json")
		return TUIStore{Registry: base, PairingURL: localApp + "/pair", Path: path}, nil
	}
	if os.Getenv("TERMCADE_REGISTRY") == "" {
		if s, err := loadSessionAt(path); err == nil && s != nil {
			base = URL(s)
		}
	}
	if err := validateTUIRegistry(base); err != nil {
		return TUIStore{}, err
	}
	return TUIStore{Registry: base, PairingURL: PairingURI, Path: path}, nil
}

func validateTUIRegistry(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("registry must be an HTTP(S) URL without credentials, query or fragment")
	}
	return nil
}

func loopbackOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("local pairing requires an HTTP loopback origin, without credentials or a path")
	}
	ip := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("local pairing requires a literal loopback host and port")
	}
	return u, nil
}

func (s TUIStore) Load() (*Session, error) {
	session, err := loadSessionAt(s.Path)
	if err != nil || session == nil {
		return session, err
	}
	if strings.TrimRight(session.Registry, "/") != strings.TrimRight(s.Registry, "/") {
		return nil, fmt.Errorf("saved credentials belong to another registry; sign in here separately")
	}
	return session, nil
}

func (s TUIStore) Save(session Session) error {
	if session.Registry != s.Registry {
		return fmt.Errorf("refusing credentials for another registry")
	}
	return saveSessionAt(s.Path, session)
}

func (s TUIStore) Clear() error {
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s TUIStore) CanPair() error {
	if _, err := loopbackOrigin(s.Registry); err == nil && s.PairingURL == PairingURI {
		return fmt.Errorf("set TERMCADE_DEV_APP_URL to the local app origin before pairing with a local registry")
	}
	return nil
}
