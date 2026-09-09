package registry

import (
	"fmt"
	"net/http"
	"net/url"
)

type PlayDay struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

type CLICredential struct {
	ID         string `json:"id"`
	ClientID   string `json:"client_id"`
	DeviceName string `json:"device_name"`
	ExpiresAt  string `json:"expires_at"`
	Revoked    bool   `json:"revoked"`
	LastUsedAt string `json:"last_used_at,omitempty"`
	CreatedAt  string `json:"created_at"`
}

func (c *Client) Game(author, slug string) (Game, error) {
	var out Game
	err := c.do(http.MethodGet, "/v1/games/"+url.PathEscape(author)+"/"+url.PathEscape(slug), nil, &out)
	if err == nil && (out.ID != author+"/"+slug || out.Name == "") {
		return Game{}, fmt.Errorf("the marketplace returned a mismatched game identity")
	}
	return out, err
}

func (c *Client) Owner(name string) (HandleOwner, error) {
	var out HandleOwner
	err := c.do(http.MethodGet, "/v1/usernames/"+url.PathEscape(name), nil, &out)
	if err == nil && out.Name != name {
		return HandleOwner{}, fmt.Errorf("the marketplace returned a mismatched owner identity")
	}
	return out, err
}

func (c *Client) PlayDays(author, slug string, days int) ([]PlayDay, error) {
	if days < 1 || days > 366 {
		return nil, fmt.Errorf("play-day window must be 1–366")
	}
	path := "/v1/usernames/" + url.PathEscape(author) + "/plays"
	if slug != "" {
		path = "/v1/games/" + url.PathEscape(author) + "/" + url.PathEscape(slug) + "/plays"
	}
	var out struct {
		Days []PlayDay `json:"days"`
	}
	err := c.do(http.MethodGet, fmt.Sprintf("%s?days=%d", path, days), nil, &out)
	return out.Days, err
}

func (c *Client) CLICredentials() ([]CLICredential, error) {
	var out []CLICredential
	err := c.do(http.MethodGet, "/v1/cli-credentials", nil, &out)
	return out, err
}

func (c *Client) RevokeCLICredential(id string) error {
	if id == "" {
		return fmt.Errorf("missing credential id")
	}
	return c.do(http.MethodDelete, "/v1/cli-credentials/"+url.PathEscape(id), nil, nil)
}
