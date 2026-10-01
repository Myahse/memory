// Package auth verifies Supabase access tokens.
//
// Supports both the legacy shared HS256 secret and asymmetric signing keys
// published at {SUPABASE_URL}/auth/v1/.well-known/jwks.json (ES256 / RS256).
package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type User struct {
	ID    string
	Email string
	Name  string
}

type Verifier struct {
	secret  []byte
	jwksURL string
	client  *http.Client

	mu      sync.RWMutex
	keys    map[string]any
	fetched time.Time
}

func NewVerifier(supabaseURL, secret string) *Verifier {
	return &Verifier{
		secret:  []byte(secret),
		jwksURL: supabaseURL + "/auth/v1/.well-known/jwks.json",
		client:  &http.Client{Timeout: 10 * time.Second},
		keys:    map[string]any{},
	}
}

type claims struct {
	Email        string         `json:"email"`
	Role         string         `json:"role"`
	UserMetadata map[string]any `json:"user_metadata"`
	jwt.RegisteredClaims
}

var ErrInvalidToken = errors.New("invalid or expired access token")

func (v *Verifier) Verify(ctx context.Context, token string) (*User, error) {
	var c claims
	parsed, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		switch t.Method.Alg() {
		case "HS256":
			if len(v.secret) == 0 {
				return nil, errors.New("HS256 token but SUPABASE_JWT_SECRET is not configured")
			}
			return v.secret, nil
		case "ES256", "RS256":
			kid, _ := t.Header["kid"].(string)
			return v.key(ctx, kid)
		default:
			return nil, fmt.Errorf("unexpected signing method %s", t.Method.Alg())
		}
	},
		jwt.WithValidMethods([]string{"HS256", "ES256", "RS256"}),
		jwt.WithAudience("authenticated"),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	if c.Subject == "" || c.Role != "authenticated" {
		return nil, ErrInvalidToken
	}
	u := &User{ID: c.Subject, Email: c.Email}
	if n, ok := c.UserMetadata["name"].(string); ok {
		u.Name = n
	} else if n, ok := c.UserMetadata["full_name"].(string); ok {
		u.Name = n
	}
	return u, nil
}

func (v *Verifier) key(ctx context.Context, kid string) (any, error) {
	v.mu.RLock()
	k, ok := v.keys[kid]
	stale := time.Since(v.fetched) > time.Hour
	v.mu.RUnlock()
	if ok && !stale {
		return k, nil
	}
	if err := v.refresh(ctx); err != nil {
		if ok {
			return k, nil // keep serving the cached key if the refresh fails
		}
		return nil, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown signing key %q", kid)
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (v *Verifier) refresh(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if time.Since(v.fetched) < 30*time.Second {
		return nil // rate-limit refreshes triggered by unknown kids
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch jwks: status %d", resp.StatusCode)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}
	keys := map[string]any{}
	for _, k := range set.Keys {
		switch k.Kty {
		case "EC":
			if k.Crv != "P-256" {
				continue
			}
			x, err1 := b64(k.X)
			y, err2 := b64(k.Y)
			if err1 != nil || err2 != nil {
				continue
			}
			keys[k.Kid] = &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		case "RSA":
			n, err1 := b64(k.N)
			e, err2 := b64(k.E)
			if err1 != nil || err2 != nil {
				continue
			}
			keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		}
	}
	v.keys = keys
	v.fetched = time.Now()
	return nil
}

func b64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

type ctxKey struct{}

func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

func FromContext(ctx context.Context) *User {
	u, _ := ctx.Value(ctxKey{}).(*User)
	return u
}
