package middleware

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"office-craft-api/internal/models"
)

type jwksTestServer struct {
	server *httptest.Server
	calls  atomic.Int32
	mu     sync.RWMutex
	keys   []jwk
}

func newJWKSTestServer(t *testing.T, keys []jwk) *jwksTestServer {
	t.Helper()
	fixture := &jwksTestServer{keys: keys}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.calls.Add(1)
		if r.URL.Path != "/auth/v1/.well-known/jwks.json" {
			t.Errorf("unexpected JWKS path %q", r.URL.Path)
		}
		fixture.mu.RLock()
		defer fixture.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwksResponse{Keys: fixture.keys})
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (s *jwksTestServer) setKeys(keys []jwk) {
	s.mu.Lock()
	s.keys = keys
	s.mu.Unlock()
}

func generateTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ES256 key: %v", err)
	}
	return key
}

func publicJWK(kid string, key *ecdsa.PrivateKey) jwk {
	coordinate := func(value []byte) string {
		padded := make([]byte, 32)
		copy(padded[32-len(value):], value)
		return base64.RawURLEncoding.EncodeToString(padded)
	}

	return jwk{
		KTY: "EC",
		Use: "sig",
		Alg: "ES256",
		KID: kid,
		CRV: "P-256",
		X:   coordinate(key.PublicKey.X.Bytes()),
		Y:   coordinate(key.PublicKey.Y.Bytes()),
	}
}

func validTestClaims(issuer string) supabaseClaims {
	return supabaseClaims{
		AppUserID: testAppUserID,
		AppRoles: map[string]string{
			models.ApplicationCodeSilapet: models.RoleUser,
		},
		SessionID: testSessionID,
		Role:      "authenticated",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   testAuthUserID,
			Audience:  jwt.ClaimStrings{"authenticated"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	}
}

func signTestToken(
	t *testing.T,
	method jwt.SigningMethod,
	key interface{},
	kid string,
	claims supabaseClaims,
) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func TestTokenVerifierAcceptsValidES256TokenAndCachesJWKS(t *testing.T) {
	key := generateTestKey(t)
	server := newJWKSTestServer(t, []jwk{publicJWK("active-key", key)})
	verifier := NewTokenVerifier(server.server.URL)
	claims := validTestClaims(server.server.URL + "/auth/v1")
	token := signTestToken(t, jwt.SigningMethodES256, key, "active-key", claims)

	for i := 0; i < 2; i++ {
		verified, err := verifier.Verify(context.Background(), token)
		if err != nil {
			t.Fatalf("verify valid token: %v", err)
		}
		if verified.AuthUserID != testAuthUserID ||
			verified.AppUserID != testAppUserID ||
			verified.SessionID != testSessionID ||
			verified.AppRole != models.RoleUser {

			t.Fatal("verified token claims changed")
		}
	}

	if server.calls.Load() != 1 {
		t.Fatalf("expected one cached JWKS fetch, got %d", server.calls.Load())
	}
}

func TestTokenVerifierRefreshesJWKSForUnknownKID(t *testing.T) {
	firstKey := generateTestKey(t)
	secondKey := generateTestKey(t)
	firstJWK := publicJWK("first-key", firstKey)
	secondJWK := publicJWK("second-key", secondKey)
	server := newJWKSTestServer(t, []jwk{firstJWK})
	verifier := NewTokenVerifier(server.server.URL)
	claims := validTestClaims(server.server.URL + "/auth/v1")

	firstToken := signTestToken(t, jwt.SigningMethodES256, firstKey, "first-key", claims)
	if _, err := verifier.Verify(context.Background(), firstToken); err != nil {
		t.Fatalf("verify first key: %v", err)
	}

	server.setKeys([]jwk{firstJWK, secondJWK})
	secondToken := signTestToken(t, jwt.SigningMethodES256, secondKey, "second-key", claims)
	if _, err := verifier.Verify(context.Background(), secondToken); err != nil {
		t.Fatalf("verify rotated key: %v", err)
	}

	if server.calls.Load() != 2 {
		t.Fatalf("expected refresh for unknown kid, got %d fetches", server.calls.Load())
	}
}

func TestTokenVerifierSerializesConcurrentJWKSRefresh(t *testing.T) {
	key := generateTestKey(t)
	server := newJWKSTestServer(t, []jwk{publicJWK("active-key", key)})
	verifier := NewTokenVerifier(server.server.URL)
	claims := validTestClaims(server.server.URL + "/auth/v1")
	token := signTestToken(t, jwt.SigningMethodES256, key, "active-key", claims)

	const workers = 16
	errorsCh := make(chan error, workers)
	var workersWG sync.WaitGroup
	workersWG.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer workersWG.Done()
			_, err := verifier.Verify(context.Background(), token)
			errorsCh <- err
		}()
	}
	workersWG.Wait()
	close(errorsCh)

	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent verify: %v", err)
		}
	}
	if server.calls.Load() != 1 {
		t.Fatalf("expected one synchronized JWKS fetch, got %d", server.calls.Load())
	}
}

func TestTokenVerifierRejectsInvalidTokens(t *testing.T) {
	key := generateTestKey(t)
	otherKey := generateTestKey(t)
	server := newJWKSTestServer(t, []jwk{publicJWK("active-key", key)})
	issuer := server.server.URL + "/auth/v1"

	tests := []struct {
		name  string
		token func() string
	}{
		{
			name: "invalid signature",
			token: func() string {
				return signTestToken(t, jwt.SigningMethodES256, otherKey, "active-key", validTestClaims(issuer))
			},
		},
		{
			name: "HS256",
			token: func() string {
				return signTestToken(t, jwt.SigningMethodHS256, []byte("not-used"), "active-key", validTestClaims(issuer))
			},
		},
		{
			name: "wrong issuer",
			token: func() string {
				claims := validTestClaims("https://wrong.example/auth/v1")
				return signTestToken(t, jwt.SigningMethodES256, key, "active-key", claims)
			},
		},
		{
			name: "wrong audience",
			token: func() string {
				claims := validTestClaims(issuer)
				claims.Audience = jwt.ClaimStrings{"other"}
				return signTestToken(t, jwt.SigningMethodES256, key, "active-key", claims)
			},
		},
		{
			name: "expired",
			token: func() string {
				claims := validTestClaims(issuer)
				claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
				return signTestToken(t, jwt.SigningMethodES256, key, "active-key", claims)
			},
		},
		{
			name: "missing expiry",
			token: func() string {
				claims := validTestClaims(issuer)
				claims.ExpiresAt = nil
				return signTestToken(t, jwt.SigningMethodES256, key, "active-key", claims)
			},
		},
		{
			name:  "malformed",
			token: func() string { return "not-a-jwt" },
		},
		{
			name: "missing kid",
			token: func() string {
				return signTestToken(t, jwt.SigningMethodES256, key, "", validTestClaims(issuer))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier := NewTokenVerifier(server.server.URL)
			if _, err := verifier.Verify(context.Background(), test.token()); err == nil {
				t.Fatal("expected token verification failure")
			}
		})
	}
}

func TestTokenVerifierRejectsInvalidCustomClaims(t *testing.T) {
	key := generateTestKey(t)
	server := newJWKSTestServer(t, []jwk{publicJWK("active-key", key)})
	issuer := server.server.URL + "/auth/v1"

	tests := []struct {
		name   string
		mutate func(*supabaseClaims)
	}{
		{"missing sub", func(c *supabaseClaims) { c.Subject = "" }},
		{"malformed sub", func(c *supabaseClaims) { c.Subject = "not-a-uuid" }},
		{"missing app user", func(c *supabaseClaims) { c.AppUserID = "" }},
		{"malformed app user", func(c *supabaseClaims) { c.AppUserID = "not-a-uuid" }},
		{"missing app roles", func(c *supabaseClaims) { c.AppRoles = nil }},
		{"other app only", func(c *supabaseClaims) { c.AppRoles = map[string]string{"other": models.RoleAdmin} }},
		{"invalid SILAPET role", func(c *supabaseClaims) { c.AppRoles[models.ApplicationCodeSilapet] = "owner" }},
		{"missing session", func(c *supabaseClaims) { c.SessionID = "" }},
		{"malformed session", func(c *supabaseClaims) { c.SessionID = "not-a-uuid" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := validTestClaims(issuer)
			test.mutate(&claims)
			token := signTestToken(t, jwt.SigningMethodES256, key, "active-key", claims)
			verifier := NewTokenVerifier(server.server.URL)
			if _, err := verifier.Verify(context.Background(), token); err == nil {
				t.Fatal("expected custom claim validation failure")
			}
		})
	}
}

func TestTokenVerifierDoesNotUseStandardRoleAsApplicationRole(t *testing.T) {
	key := generateTestKey(t)
	server := newJWKSTestServer(t, []jwk{publicJWK("active-key", key)})
	claims := validTestClaims(server.server.URL + "/auth/v1")
	claims.Role = "authenticated"
	claims.AppRoles[models.ApplicationCodeSilapet] = models.RoleAdmin
	token := signTestToken(t, jwt.SigningMethodES256, key, "active-key", claims)

	verified, err := NewTokenVerifier(server.server.URL).Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if verified.AppRole != models.RoleAdmin || verified.AppRole == claims.Role {
		t.Fatal("standard Supabase role was used as application role")
	}
}
