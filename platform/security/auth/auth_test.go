package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var fixedTime = time.Unix(1_700_000_000, 0).UTC()

func testConfig(t *testing.T, mutate ...func(*AuthConfig)) AuthConfig {
	t.Helper()
	cfg := AuthConfig{
		Providers: []AuthProvider{ProviderOAuth2},
		JWTAlg:    "RS256",
		Issuer:    "https://issuer.example.com",
		Audience:  []string{"ecommerce"},
		Now:       func() time.Time { return fixedTime },
	}
	for _, m := range mutate {
		m(&cfg)
	}
	return cfg
}

// encodeJWT builds an unsigned compact serialisation whose claims segment is
// base64url(JSON(claims)). Signature verification is not part of this package.
func encodeJWT(t *testing.T, claims TokenClaims) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling claims: %v", err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func newValidator(t *testing.T, mutate ...func(*AuthConfig)) *Validator {
	t.Helper()
	v, err := NewValidator(testConfig(t, mutate...))
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	return v
}

func TestNewValidatorRequiresProviders(t *testing.T) {
	t.Parallel()

	_, err := NewValidator(AuthConfig{})
	if !errors.Is(err, ErrNoProviders) {
		t.Errorf("NewValidator() error = %v, want ErrNoProviders", err)
	}
}

func TestNewValidatorAppliesDefaults(t *testing.T) {
	t.Parallel()

	v, err := NewValidator(AuthConfig{Providers: []AuthProvider{ProviderAPIKey}})
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	if got := v.Config().JWTAlg; got != "RS256" {
		t.Errorf("JWTAlg = %q, want RS256", got)
	}
	if v.Config().Now == nil {
		t.Error("Now is nil; a nil clock would panic on the first validation")
	}
}

// The previous implementation returned (&TokenClaims{}, nil) for every token,
// so any caller treating a nil error as "authenticated" accepted forged tokens.
func TestValidateTokenNeverReturnsEmptyClaimsForAGarbageToken(t *testing.T) {
	t.Parallel()

	v := newValidator(t)

	claims, err := v.ValidateToken("not-a-jwt")
	if err == nil {
		t.Fatalf("ValidateToken() = %+v, nil; want an error for a malformed token", claims)
	}
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("error = %v, want ErrInvalidToken", err)
	}
	if claims != nil {
		t.Errorf("claims = %+v, want nil alongside the error", claims)
	}
}

func TestValidateTokenEmpty(t *testing.T) {
	t.Parallel()

	v := newValidator(t)
	if _, err := v.ValidateToken("   "); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("ValidateToken(whitespace) error = %v, want ErrInvalidToken", err)
	}
}

func TestValidateTokenSuccess(t *testing.T) {
	t.Parallel()

	v := newValidator(t)
	token := encodeJWT(t, TokenClaims{
		Subject:   "u-1",
		Issuer:    "https://issuer.example.com",
		Audience:  []string{"ecommerce"},
		ExpiresAt: fixedTime.Add(time.Hour).Unix(),
		IssuedAt:  fixedTime.Add(-time.Minute).Unix(),
		Roles:     []string{"catalog_admin"},
		Scopes:    []string{"products:write"},
	})

	claims, err := v.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}
	if claims.Subject != "u-1" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "u-1")
	}
	if !claims.HasRole("catalog_admin") {
		t.Error("HasRole(catalog_admin) = false, want true")
	}
	if claims.HasRole("platform_admin") {
		t.Error("HasRole(platform_admin) = true, want false")
	}
	if !claims.HasScope("products:write") {
		t.Error("HasScope(products:write) = false, want true")
	}
}

func TestValidateTokenMissingSubject(t *testing.T) {
	t.Parallel()

	v := newValidator(t)
	token := encodeJWT(t, TokenClaims{
		Issuer:   "https://issuer.example.com",
		Audience: []string{"ecommerce"},
	})

	if _, err := v.ValidateToken(token); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("error = %v, want ErrInvalidToken for a token with no sub", err)
	}
}

func TestValidateTokenExpired(t *testing.T) {
	t.Parallel()

	v := newValidator(t)
	token := encodeJWT(t, TokenClaims{
		Subject:   "u-1",
		Issuer:    "https://issuer.example.com",
		Audience:  []string{"ecommerce"},
		ExpiresAt: fixedTime.Add(-time.Second).Unix(),
	})

	if _, err := v.ValidateToken(token); !errors.Is(err, ErrExpired) {
		t.Errorf("error = %v, want ErrExpired", err)
	}
}

func TestExpiredTokenWithinLeewayIsAccepted(t *testing.T) {
	t.Parallel()

	v := newValidator(t, func(c *AuthConfig) { c.Leeway = time.Minute })
	token := encodeJWT(t, TokenClaims{
		Subject:   "u-1",
		Issuer:    "https://issuer.example.com",
		Audience:  []string{"ecommerce"},
		ExpiresAt: fixedTime.Add(-30 * time.Second).Unix(),
	})

	if _, err := v.ValidateToken(token); err != nil {
		t.Errorf("ValidateToken() error = %v, want nil within the leeway", err)
	}
}

func TestValidateTokenNotYetValid(t *testing.T) {
	t.Parallel()

	v := newValidator(t)
	token := encodeJWT(t, TokenClaims{
		Subject:   "u-1",
		Issuer:    "https://issuer.example.com",
		Audience:  []string{"ecommerce"},
		NotBefore: fixedTime.Add(time.Hour).Unix(),
	})

	if _, err := v.ValidateToken(token); !errors.Is(err, ErrNotYetValid) {
		t.Errorf("error = %v, want ErrNotYetValid", err)
	}
}

func TestValidateTokenWrongIssuer(t *testing.T) {
	t.Parallel()

	v := newValidator(t)
	token := encodeJWT(t, TokenClaims{
		Subject:  "u-1",
		Issuer:   "https://evil.example.com",
		Audience: []string{"ecommerce"},
	})

	if _, err := v.ValidateToken(token); !errors.Is(err, ErrIssuer) {
		t.Errorf("error = %v, want ErrIssuer", err)
	}
}

func TestValidateTokenWrongAudience(t *testing.T) {
	t.Parallel()

	v := newValidator(t)
	token := encodeJWT(t, TokenClaims{
		Subject:  "u-1",
		Issuer:   "https://issuer.example.com",
		Audience: []string{"some-other-api"},
	})

	if _, err := v.ValidateToken(token); !errors.Is(err, ErrAudience) {
		t.Errorf("error = %v, want ErrAudience", err)
	}
}

// A configured public key means the deployment believes tokens are verified.
// Skipping verification would be a bypass, so this must fail closed.
func TestConfiguredKeyFailsClosed(t *testing.T) {
	t.Parallel()

	v := newValidator(t, func(c *AuthConfig) { c.PublicKey = testPublicKey() })
	token := encodeJWT(t, TokenClaims{
		Subject:  "u-1",
		Issuer:   "https://issuer.example.com",
		Audience: []string{"ecommerce"},
	})

	claims, err := v.ValidateToken(token)
	if !errors.Is(err, ErrUnverified) {
		t.Errorf("error = %v, want ErrUnverified when a key is configured", err)
	}
	if claims != nil {
		t.Errorf("claims = %+v, want nil: an unverified token must not produce claims", claims)
	}
}

func TestIssuerAndAudienceChecksAreOptional(t *testing.T) {
	t.Parallel()

	v := newValidator(t, func(c *AuthConfig) {
		c.Issuer = ""
		c.Audience = nil
	})
	token := encodeJWT(t, TokenClaims{Subject: "u-1"})

	if _, err := v.ValidateToken(token); err != nil {
		t.Errorf("ValidateToken() error = %v, want nil when no issuer/audience is required", err)
	}
}

func TestDecodeSegmentRejectsStandardBase64(t *testing.T) {
	t.Parallel()

	// "eyJhIjoxfQ" is base64url; the '+' and '/' alphabet must be rejected so
	// that one token cannot have two decodings.
	_, err := decodeSegment("++//")
	if err == nil {
		t.Error("decodeSegment(++//) = nil error; standard base64 must be rejected")
	}
}

func TestDecodeSegmentRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u-1","is_admin":true}`))
	_, err := decodeSegment(payload)
	if err == nil {
		t.Error("decodeSegment() accepted an undeclared claim (is_admin): a typo'd or spoofed claim must not pass silently")
	}
}

func TestDecodeSegmentEmpty(t *testing.T) {
	t.Parallel()

	if _, err := decodeSegment(""); !errors.Is(err, errEmptyClaims) {
		t.Errorf("decodeSegment(\"\") error = %v, want errEmptyClaims", err)
	}
}

func TestAPIKeyValidator(t *testing.T) {
	t.Parallel()

	v := NewAPIKeyValidator(map[string]string{
		"key-a": "service-a",
		"key-b": "service-b",
	})

	if service, ok := v.Validate("key-a"); !ok || service != "service-a" {
		t.Errorf("Validate(key-a) = %q, %v; want %q, true", service, ok, "service-a")
	}
	if _, ok := v.Validate("nope"); ok {
		t.Error("Validate(nope) = ok, want false for an unknown key")
	}
	if _, ok := v.Validate(""); ok {
		t.Error("Validate(\"\") = ok, want false")
	}
}

func TestAPIKeyValidatorCopiesItsInput(t *testing.T) {
	t.Parallel()

	keys := map[string]string{"key-a": "service-a"}
	v := NewAPIKeyValidator(keys)

	// Mutating the caller's map must not change the validator's view.
	keys["key-a"] = "hijacked"
	delete(keys, "key-a")

	if service, ok := v.Validate("key-a"); !ok || service != "service-a" {
		t.Errorf("Validate(key-a) = %q, %v; want the original %q, true", service, ok, "service-a")
	}
}

func TestConstantTimeEqual(t *testing.T) {
	t.Parallel()

	if !constantTimeEqual("abc", "abc") {
		t.Error("constantTimeEqual(abc, abc) = false, want true")
	}
	if constantTimeEqual("abc", "abd") {
		t.Error("constantTimeEqual(abc, abd) = true, want false")
	}
	if constantTimeEqual("abc", "ab") {
		t.Error("constantTimeEqual(abc, ab) = true, want false for different lengths")
	}
}

func TestHasRoleAndScopeOnEmptyClaims(t *testing.T) {
	t.Parallel()

	var c TokenClaims
	if c.HasRole("x") || c.HasScope("x") {
		t.Error("empty claims reported a role or scope")
	}
}

func TestProvidersAreDocumented(t *testing.T) {
	t.Parallel()

	// A change to these constants is an API change for every caller.
	want := map[AuthProvider]string{
		ProviderOAuth2:        "oauth2",
		ProviderOpenIDConnect: "openid-connect",
		ProviderAPIKey:        "api-key",
	}
	for provider, s := range want {
		if string(provider) != s {
			t.Errorf("provider constant = %q, want %q", string(provider), s)
		}
	}
}

func TestTokenWhitespaceIsTrimmed(t *testing.T) {
	t.Parallel()

	v := newValidator(t)
	token := encodeJWT(t, TokenClaims{
		Subject:  "u-1",
		Issuer:   "https://issuer.example.com",
		Audience: []string{"ecommerce"},
	})

	if _, err := v.ValidateToken("  " + token + "\n"); err != nil {
		t.Errorf("ValidateToken() error = %v, want nil: surrounding whitespace should be tolerated", err)
	}
}

func TestMalformedSegments(t *testing.T) {
	t.Parallel()

	v := newValidator(t)
	for _, token := range []string{
		"only.two",
		"a.b.c.d",
		"header..signature",
		"header." + strings.Repeat("!", 10) + ".signature",
	} {
		if _, err := v.ValidateToken(token); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("ValidateToken(%q) error = %v, want ErrInvalidToken", token, err)
		}
	}
}

// testPublicKey returns a throwaway RSA public key. Only its non-nil-ness
// matters: the package refuses to validate when a key is configured, so the
// test never needs a real modulus.
func testPublicKey() *rsa.PublicKey {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		panic(err)
	}
	return &key.PublicKey
}
