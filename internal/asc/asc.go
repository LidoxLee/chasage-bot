// Package asc reads build numbers from the App Store Connect API.
package asc

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

const defaultBaseURL = "https://api.appstoreconnect.apple.com"

// Client authenticates with an App Store Connect API key (AuthKey_<KeyID>.p8).
type Client struct {
	KeyID    string
	IssuerID string
	KeyPath  string
	BundleID string

	// BaseURL, HTTP and Now are overridden in tests.
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time
}

// LatestBuildNumber returns the highest CFBundleVersion uploaded for BundleID,
// or 0 when the app has no builds yet.
func (c Client) LatestBuildNumber(ctx context.Context) (int, error) {
	token, err := c.token()
	if err != nil {
		return 0, err
	}
	var apps struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	q := url.Values{"filter[bundleId]": {c.BundleID}, "fields[apps]": {"bundleId"}}
	if err := c.get(ctx, token, "/v1/apps?"+q.Encode(), &apps); err != nil {
		return 0, err
	}
	if len(apps.Data) == 0 {
		return 0, fmt.Errorf("App Store Connect 找不到 bundle id %s", c.BundleID)
	}
	var builds struct {
		Data []struct {
			Attributes struct {
				Version string `json:"version"`
			} `json:"attributes"`
		} `json:"data"`
	}
	// Build numbers only grow, so the most recent uploads hold the maximum.
	q = url.Values{
		"filter[app]":    {apps.Data[0].ID},
		"fields[builds]": {"version"},
		"sort":           {"-uploadedDate"},
		"limit":          {"200"},
	}
	if err := c.get(ctx, token, "/v1/builds?"+q.Encode(), &builds); err != nil {
		return 0, err
	}
	latest := 0
	for _, b := range builds.Data {
		if n, err := strconv.Atoi(b.Attributes.Version); err == nil && n > latest {
			latest = n
		}
	}
	return latest, nil
}

func (c Client) get(ctx context.Context, token, path string, out any) error {
	base := c.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("App Store Connect %s: HTTP %d: %.300s", path, resp.StatusCode, body)
	}
	return json.Unmarshal(body, out)
}

// token builds the ES256 JWT App Store Connect expects (max lifetime 20 minutes).
func (c Client) token() (string, error) {
	key, err := loadKey(c.KeyPath)
	if err != nil {
		return "", err
	}
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": c.KeyID, "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss": c.IssuerID,
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"aud": "appstoreconnect-v1",
	})
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	// JWS wants the raw 32-byte r || s, not ASN.1.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + enc.EncodeToString(sig), nil
}

func loadKey(path string) (*ecdsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("App Store Connect key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse App Store Connect key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("App Store Connect key is not an EC key")
	}
	return key, nil
}
