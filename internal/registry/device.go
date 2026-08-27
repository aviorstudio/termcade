package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	// PairingURI is deliberately compiled in. A compromised API must not be
	// able to send a player to a lookalike authorization page.
	PairingURI = "https://app.termca.de/pair"
	clientID   = "termcade-cli"

	minDeviceExpiry  = 30 * time.Second
	maxDeviceExpiry  = 15 * time.Minute
	minPollInterval  = time.Second
	maxPollInterval  = 30 * time.Second
	maxTokenLifetime = 31 * 24 * time.Hour
)

var (
	userCodeRE   = regexp.MustCompile(`^[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}-[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}$`)
	deviceCodeRE = regexp.MustCompile(`^tcd_[A-Za-z0-9_-]{43}$`)
	cliTokenRE   = regexp.MustCompile(`^tcc_[A-Za-z0-9_-]{43}$`)
	publishKeyRE = regexp.MustCompile(`^tck_[A-Za-z0-9_-]{43}$`)
)

func IsCLIToken(value string) bool   { return cliTokenRE.MatchString(value) }
func IsPublishKey(value string) bool { return publishKeyRE.MatchString(value) }

var ErrDeviceExpired = errors.New("device authorization expired")

type DeviceRound struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresIn       time.Duration
	Interval        time.Duration
}

type deviceStartResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int64  `json:"expires_in"`
	Interval        int64  `json:"interval"`
}

type DevicePoll struct {
	Status       string `json:"status"`
	Token        string `json:"token"`
	CredentialID string `json:"credential_id"`
	ExpiresAt    string `json:"expires_at"`
	Interval     int64  `json:"interval"`
}

func (c *Client) StartDevice(ctx context.Context, deviceName string) (DeviceRound, error) {
	deviceName = safeText(deviceName, 200)
	if deviceName == "" {
		deviceName = "Termcade CLI"
	}
	var out deviceStartResponse
	err := c.doContext(ctx, http.MethodPost, "/v1/device/start", map[string]string{
		"client_id": clientID, "device_name": deviceName,
	}, &out)
	if err != nil {
		return DeviceRound{}, err
	}
	round := DeviceRound{
		DeviceCode: out.DeviceCode, UserCode: strings.ToUpper(strings.TrimSpace(out.UserCode)),
		VerificationURI: out.VerificationURI,
		ExpiresIn:       time.Duration(out.ExpiresIn) * time.Second,
		Interval:        time.Duration(out.Interval) * time.Second,
	}
	if !deviceCodeRE.MatchString(round.DeviceCode) {
		return DeviceRound{}, errors.New("the marketplace returned a malformed device code")
	}
	if !userCodeRE.MatchString(round.UserCode) {
		return DeviceRound{}, errors.New("the marketplace returned a malformed pairing code")
	}
	if round.VerificationURI != PairingURI {
		return DeviceRound{}, errors.New("the marketplace returned an untrusted pairing address")
	}
	if round.ExpiresIn < minDeviceExpiry || round.ExpiresIn > maxDeviceExpiry {
		return DeviceRound{}, errors.New("the marketplace returned an unsafe pairing expiry")
	}
	if round.Interval < minPollInterval || round.Interval > maxPollInterval {
		return DeviceRound{}, errors.New("the marketplace returned an unsafe polling interval")
	}
	return round, nil
}

func (c *Client) PollDevice(ctx context.Context, deviceCode string) (DevicePoll, error) {
	if !deviceCodeRE.MatchString(deviceCode) {
		return DevicePoll{}, errors.New("refusing to poll with a malformed device code")
	}
	var out DevicePoll
	if err := c.doContext(ctx, http.MethodPost, "/v1/device/poll",
		map[string]string{"device_code": deviceCode}, &out); err != nil {
		return DevicePoll{}, err
	}
	switch out.Status {
	case "pending":
		interval := time.Duration(out.Interval) * time.Second
		if interval < minPollInterval || interval > maxPollInterval {
			return DevicePoll{}, errors.New("the marketplace returned an unsafe polling interval")
		}
	case "expired":
		if out.Token != "" {
			return DevicePoll{}, errors.New("the marketplace returned a credential in an expired response")
		}
	case "approved":
		if !cliTokenRE.MatchString(out.Token) {
			return DevicePoll{}, errors.New("the marketplace returned a malformed CLI credential")
		}
		if out.CredentialID == "" || safeText(out.CredentialID, 200) != out.CredentialID {
			return DevicePoll{}, errors.New("the marketplace returned a malformed credential identifier")
		}
		expires, err := time.Parse(time.RFC3339, out.ExpiresAt)
		if err != nil || !expires.After(time.Now()) || time.Until(expires) > maxTokenLifetime {
			return DevicePoll{}, errors.New("the marketplace returned an unsafe CLI credential expiry")
		}
	default:
		return DevicePoll{}, errors.New("the marketplace returned an unknown device status")
	}
	return out, nil
}

type devicePolicy struct {
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

var productionDevicePolicy = devicePolicy{now: time.Now, sleep: sleepContext}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func transient(err error) bool {
	if errors.Is(err, ErrUnreachable) {
		return true
	}
	var response responseError
	return errors.As(err, &response) &&
		(response.status == http.StatusRequestTimeout || response.status == http.StatusTooManyRequests || response.status >= 500)
}

func nextBackoff(current time.Duration) time.Duration {
	if current < time.Second {
		return time.Second
	}
	current *= 2
	if current > maxPollInterval {
		return maxPollInterval
	}
	return current
}

func waitWithin(ctx context.Context, policy devicePolicy, duration time.Duration, deadline time.Time) error {
	remaining := deadline.Sub(policy.now())
	if remaining <= 0 {
		return ErrDeviceExpired
	}
	if duration > remaining {
		duration = remaining
	}
	if err := policy.sleep(ctx, duration); err != nil {
		return err
	}
	if !policy.now().Before(deadline) {
		return ErrDeviceExpired
	}
	return nil
}

func (c *Client) pollRound(ctx context.Context, round DeviceRound, policy devicePolicy) (DevicePoll, error) {
	deadline := policy.now().Add(round.ExpiresIn)
	interval := round.Interval
	backoff := interval
	for {
		if err := waitWithin(ctx, policy, backoff, deadline); err != nil {
			return DevicePoll{}, err
		}
		pollCtx, cancel := context.WithDeadline(ctx, deadline)
		result, err := c.PollDevice(pollCtx, round.DeviceCode)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return DevicePoll{}, ErrDeviceExpired
			}
			if transient(err) {
				backoff = nextBackoff(backoff)
				continue
			}
			return DevicePoll{}, err
		}
		backoff = interval
		switch result.Status {
		case "pending":
			interval = time.Duration(result.Interval) * time.Second
			backoff = interval
		case "expired":
			return DevicePoll{}, ErrDeviceExpired
		case "approved":
			return result, nil
		}
	}
}

// DeviceLogin runs complete, restartable device-authorization rounds. Only the
// human code and the fixed pairing URI cross the display callback; the device
// code and issued credential remain request-body/in-memory values.
func (c *Client) DeviceLogin(ctx context.Context, deviceName string, display func(uri, code string)) (Session, error) {
	return c.deviceLogin(ctx, deviceName, display, productionDevicePolicy)
}

func (c *Client) deviceLogin(ctx context.Context, deviceName string, display func(string, string), policy devicePolicy) (Session, error) {
	startBackoff := time.Second
	expiryBackoff := time.Second
	for {
		round, err := c.StartDevice(ctx, deviceName)
		if err != nil {
			if !transient(err) {
				return Session{}, err
			}
			if err := policy.sleep(ctx, startBackoff); err != nil {
				return Session{}, err
			}
			startBackoff = nextBackoff(startBackoff)
			continue
		}
		display(PairingURI, round.UserCode)
		approved, err := c.pollRound(ctx, round, policy)
		if errors.Is(err, ErrDeviceExpired) {
			if err := policy.sleep(ctx, expiryBackoff); err != nil {
				return Session{}, err
			}
			expiryBackoff = nextBackoff(expiryBackoff)
			continue
		}
		if err != nil {
			return Session{}, err
		}

		credentialClient := New(c.baseURL, approved.Token)
		credentialExpiry, _ := time.Parse(time.RFC3339, approved.ExpiresAt)
		verifyBackoff := time.Second
		var me Me
		for {
			me, err = credentialClient.MeContext(ctx)
			if err == nil {
				break
			}
			if !transient(err) {
				return Session{}, fmt.Errorf("verifying issued CLI credential: %w", err)
			}
			if err := waitWithin(ctx, policy, verifyBackoff, credentialExpiry); err != nil {
				return Session{}, fmt.Errorf("verifying issued CLI credential: %w", err)
			}
			verifyBackoff = nextBackoff(verifyBackoff)
		}
		return Session{Registry: c.baseURL, Email: me.Email, Username: me.Username, Token: approved.Token}, nil
	}
}
