package asc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeKey(t *testing.T) (string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "AuthKey_K1.p8")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, key
}

func verifyToken(t *testing.T, token string, pub *ecdsa.PublicKey) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("signature: %v len %d", err, len(sig))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Fatal("bad signature")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	claims := map[string]any{}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestLatestBuildNumberTakesNumericMax(t *testing.T) {
	path, key := writeKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := verifyToken(t, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), &key.PublicKey)
		if claims["iss"] != "issuer-1" || claims["aud"] != "appstoreconnect-v1" {
			t.Errorf("claims %v", claims)
		}
		switch r.URL.Path {
		case "/v1/apps":
			if r.URL.Query().Get("filter[bundleId]") != "com.chasage.app" {
				t.Errorf("apps query %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"6700000000"}]}`))
		case "/v1/builds":
			if r.URL.Query().Get("filter[app]") != "6700000000" {
				t.Errorf("builds query %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":[
				{"attributes":{"version":"9"}},
				{"attributes":{"version":"11"}},
				{"attributes":{"version":"1.0.3"}},
				{"attributes":{"version":"10"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := Client{
		KeyID: "K1", IssuerID: "issuer-1", KeyPath: path, BundleID: "com.chasage.app",
		BaseURL: srv.URL, HTTP: srv.Client(),
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
	}
	got, err := c.LatestBuildNumber(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != 11 {
		t.Fatalf("latest = %d, want 11", got)
	}
}

func TestLatestBuildNumberReportsHTTPErrors(t *testing.T) {
	path, _ := writeKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"errors":[{"status":"401"}]}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := Client{KeyID: "K1", IssuerID: "i", KeyPath: path, BundleID: "b", BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.LatestBuildNumber(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("err = %v", err)
	}
}
