package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// decodeSegment decodes the base64url payload of a compact JWT serialisation
// into TokenClaims.
//
// It deliberately rejects the standard base64 alphabet: JWT uses base64url, and
// accepting "+" and "/" here would let two different strings decode identically.
func decodeSegment(segment string) (*TokenClaims, error) {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return nil, fmt.Errorf("claims segment is not valid base64url: %w", err)
	}

	if len(raw) == 0 {
		return nil, errEmptyClaims
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	var claims TokenClaims
	if err := decoder.Decode(&claims); err != nil {
		return nil, fmt.Errorf("claims segment is not a valid claim set: %w", err)
	}
	return &claims, nil
}

// errEmptyClaims guards against a zero-length payload decoding to a valid but
// meaningless claim set.
var errEmptyClaims = errors.New("claims segment is empty")
