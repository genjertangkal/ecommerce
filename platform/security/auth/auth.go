// Package auth provides the platform's authentication capability.
package auth

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AuthProvider is a supported authentication scheme.
type AuthProvider string

const (
	// ProviderOAuth2 authenticates via an OAuth2 access token.
	ProviderOAuth2 AuthProvider = "oauth2"
	// ProviderOpenIDConnect authenticates via an OIDC identity token.
	ProviderOpenIDConnect AuthProvider = "openid-connect"
	// ProviderAPIKey authenticates via a shared secret.
	ProviderAPIKey AuthProvider = "api-key"
)

// Errors returned by this package. Callers should compare with errors.Is.
var (
	// ErrNoProviders is returned by NewValidator when no provider is enabled.
	ErrNoProviders = errors.New("auth: no authentication providers configured")
	// ErrInvalidToken is returned when a token is structurally unusable.
	ErrInvalidToken = errors.New("auth: invalid token")
	// ErrExpired is returned when a token's expiry has passed.
	ErrExpired = errors.New("auth: token expired")
	// ErrNotYetValid is returned when a token's nbf claim is in the future.
	ErrNotYetValid = errors.New("auth: token not yet valid")
	// ErrIssuer is returned when the iss claim does not match.
	ErrIssuer = errors.New("auth: unexpected token issuer")
	// ErrAudience is returned when the aud claim does not match.
	ErrAudience = errors.New("auth: unexpected token audience")
	// ErrUnverified is returned when signature verification is requested but
	// no key is configured.
	ErrUnverified = errors.New("auth: no verification key configured")
)

// AuthConfig holds authentication configuration.
type AuthConfig struct {
	// Providers that may be used. At least one is required.
	Providers []AuthProvider
	// JWTAlg is the expected signing algorithm.
	JWTAlg string
	// PublicKey is used to verify token signatures. A nil key means claims are
	// parsed but not verified, which is only safe for tests.
	PublicKey *rsa.PublicKey
	// Issuer that tokens must claim.
	Issuer string
	// Audience that tokens must claim.
	Audience []string
	// Leeway tolerated on time-based claims.
	Leeway time.Duration
	// Now supplies the current time. Defaults to time.Now.
	Now func() time.Time
}

func (c AuthConfig) withDefaults() AuthConfig {
	if c.JWTAlg == "" {
		c.JWTAlg = "RS256"
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// TokenClaims is the set of claims this platform understands.
type TokenClaims struct {
	Subject   string                 `json:"sub"`
	Issuer    string                 `json:"iss"`
	Audience  []string               `json:"aud"`
	ExpiresAt int64                  `json:"exp"`
	IssuedAt  int64                  `json:"iat"`
	NotBefore int64                  `json:"nbf"`
	Roles     []string               `json:"roles"`
	Scopes    []string               `json:"scopes"`
	Custom    map[string]interface{} `json:"custom,omitempty"`
}

// HasRole reports whether the claims carry role.
func (c *TokenClaims) HasRole(role string) bool {
	for _, r := range c.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// HasScope reports whether the claims carry scope.
func (c *TokenClaims) HasScope(scope string) bool {
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// Validator validates bearer tokens.
//
// It does not implement the JWS/JWT signature scheme itself. That is
// deliberately a separate, well-audited dependency: a hand-rolled verifier is
// exactly the kind of code that must not live in a platform layer. What this
// type provides is the *policy* around a verifier:
//
//   - which claims must hold,
//   - when a token is expired,
//   - which algorithm is acceptable.
type Validator struct {
	config AuthConfig
}

// NewValidator creates a token validator.
//
// It fails when no provider is configured: a validator that accepts everything
// is worse than no validator at all.
func NewValidator(config AuthConfig) (*Validator, error) {
	if len(config.Providers) == 0 {
		return nil, ErrNoProviders
	}
	return &Validator{config: config.withDefaults()}, nil
}

// Config returns the validator's configuration.
func (v *Validator) Config() AuthConfig { return v.config }

// ValidateToken validates a token against the configured policy.
//
// token is the compact JWS/JWT serialisation, or - for the API-key provider - an
// opaque credential that VerifyAPIKey understands.
//
// Signature verification is not performed here. Until a verifier is wired in,
// ValidateToken parses and enforces claims only, and reports ErrUnverified when
// PublicKey is configured but verification is unavailable, so that a
// misconfigured deployment fails closed instead of accepting unsigned tokens.
func (v *Validator) ValidateToken(token string) (*TokenClaims, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("%w: token is empty", ErrInvalidToken)
	}

	claims, err := v.decodeClaims(token)
	if err != nil {
		return nil, err
	}

	if v.config.PublicKey != nil {
		// Fail closed: a key is configured, so the caller believes tokens are
		// verified. Silently skipping verification here would be a bypass.
		return nil, fmt.Errorf("%w: configure a JWS verifier to enable signature checking", ErrUnverified)
	}

	if err := v.checkTimes(claims); err != nil {
		return nil, err
	}
	if err := v.checkIssuer(claims); err != nil {
		return nil, err
	}
	if err := v.checkAudience(claims); err != nil {
		return nil, err
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("%w: missing sub claim", ErrInvalidToken)
	}
	return claims, nil
}

func (v *Validator) checkTimes(claims *TokenClaims) error {
	now := v.config.Now().Unix()

	if claims.ExpiresAt != 0 && now > claims.ExpiresAt+int64(v.config.Leeway.Seconds()) {
		return fmt.Errorf("%w: exp %d, now %d", ErrExpired, claims.ExpiresAt, now)
	}
	if claims.NotBefore != 0 && now+int64(v.config.Leeway.Seconds()) < claims.NotBefore {
		return fmt.Errorf("%w: nbf %d, now %d", ErrNotYetValid, claims.NotBefore, now)
	}
	return nil
}

func (v *Validator) checkIssuer(claims *TokenClaims) error {
	if v.config.Issuer == "" {
		return nil
	}
	if claims.Issuer != v.config.Issuer {
		return fmt.Errorf("%w: got %q, want %q", ErrIssuer, claims.Issuer, v.config.Issuer)
	}
	return nil
}

func (v *Validator) checkAudience(claims *TokenClaims) error {
	if len(v.config.Audience) == 0 {
		return nil
	}
	for _, want := range v.config.Audience {
		for _, got := range claims.Audience {
			if got == want {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: got %v, want one of %v", ErrAudience, claims.Audience, v.config.Audience)
}

// decodeClaims extracts the claims from a compact serialisation.
//
// This package only understands the JSON claims segment of a JWT, which is
// enough to enforce claims policy. It does not verify the signature; see
// ValidateToken.
func (v *Validator) decodeClaims(token string) (*TokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: expected 3 dot-separated segments, got %d", ErrInvalidToken, len(parts))
	}
	claims, err := decodeSegment(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	return claims, nil
}

// APIKeyValidator validates shared-secret credentials.
//
// Keys are held in memory and compared in constant time.
type APIKeyValidator struct {
	// keys maps key -> owning service name.
	keys map[string]string
}

// NewAPIKeyValidator creates an API key validator over a copy of keys.
func NewAPIKeyValidator(keys map[string]string) *APIKeyValidator {
	copied := make(map[string]string, len(keys))
	for k, v := range keys {
		copied[k] = v
	}
	return &APIKeyValidator{keys: copied}
}

// Validate checks an API key and returns the owning service name.
//
// The second return value is false for an unknown key. Comparison is
// constant-time so that a caller cannot learn a prefix by timing.
func (v *APIKeyValidator) Validate(key string) (string, bool) {
	var found string
	var ok bool
	for candidate, service := range v.keys {
		if constantTimeEqual(candidate, key) {
			found, ok = service, true
		}
	}
	return found, ok
}

// constantTimeEqual compares two strings without leaking their contents through
// timing.
func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
