package security

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Token types mirror superset.security.api: "access" and "refresh".
const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

// Claims is the Superset session JWT payload. It mirrors the payload produced
// by flask-jwt-extended in the Python implementation so that tokens issued by
// either side can be exchanged.
type Claims struct {
	UserID int    `json:"user_id"`
	Fresh  bool   `json:"fresh"`
	Type   string `json:"type"`
	Sub    string `json:"sub,omitempty"`
	jwt.RegisteredClaims
}

// TokenPair is returned by the login and refresh endpoints.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// IssueTokenPair mints an access/refresh pair for a user.
func IssueTokenPair(secret string, userID, ttlSeconds int) (*TokenPair, error) {
	access, err := issue(secret, userID, TokenTypeAccess, ttlSeconds)
	if err != nil {
		return nil, err
	}
	refresh, err := issue(secret, userID, TokenTypeRefresh, ttlSeconds*24*7)
	if err != nil {
		return nil, err
	}
	return &TokenPair{AccessToken: access, RefreshToken: refresh}, nil
}

func issue(secret string, userID int, typ string, ttlSeconds int) (string, error) {
	if ttlSeconds <= 0 {
		ttlSeconds = 3600
	}
	now := time.Now()
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", err
	}
	claims := Claims{
		UserID: userID,
		Fresh:  typ == TokenTypeRefresh,
		Type:   typ,
		Sub:    fmt.Sprintf("%d", userID),
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(ttlSeconds) * time.Second)),
			ID:        hex.EncodeToString(jti),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// ParseToken validates a JWT and returns its claims.
func ParseToken(secret, raw, wantType string) (*Claims, error) {
	tok, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return nil, errors.New("invalid token")
	}
	if wantType != "" && claims.Type != wantType {
		return nil, fmt.Errorf("expected %s token, got %s", wantType, claims.Type)
	}
	return claims, nil
}

// GuestClaims is the guest-token JWT payload
// (superset.security.guest_token.GuestToken).
type GuestClaims struct {
	User      GuestUser       `json:"user"`
	Resources []GuestResource `json:"resources"`
	RLS       []GuestRLSRule  `json:"rls_rules"`
	IAT       int64           `json:"iat,omitempty"`
	EXP       int64           `json:"exp,omitempty"`
	Aud       string          `json:"aud,omitempty"`
	// Scope narrows what a guest token may do. "" (legacy, embedded) keeps the
	// historical "everything" grant; ScopePublic drops the token down to the
	// read-only set a public share link needs.
	Scope string         `json:"scope,omitempty"`
	Extra map[string]any `json:"-"`
	jwt.RegisteredClaims
}

// Guest token scopes.
const (
	// ScopeEmbedded is the default embedded-dashboard grant (full access).
	ScopeEmbedded = ""
	// ScopePublic is a read-only grant minted by public share links.
	ScopePublic = "public"
)

// GuestUser is the embedded user descriptor in a guest token.
type GuestUser struct {
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// GuestResource is a resource grant inside a guest token.
type GuestResource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// GuestRLSRule is an inline row-level-security rule inside a guest token.
type GuestRLSRule struct {
	DatasetID int    `json:"dataset_id"`
	Clause    string `json:"clause"`
}

// IssueGuestToken signs a guest token.
func IssueGuestToken(secret string, claims GuestClaims, ttlSeconds int) (string, error) {
	now := time.Now()
	claims.IssuedAt = jwt.NewNumericDate(now)
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(time.Duration(ttlSeconds) * time.Second))
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// ParseGuestToken validates a guest token.
func ParseGuestToken(secret, raw, audience string) (*GuestClaims, error) {
	tok, err := jwt.ParseWithClaims(raw, &GuestClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := tok.Claims.(*GuestClaims)
	if !ok || !tok.Valid {
		return nil, errors.New("invalid guest token")
	}
	if audience != "" && claims.Aud != audience && claims.Audience != nil {
		for _, a := range claims.Audience {
			if a != audience {
				return nil, errors.New("guest token audience mismatch")
			}
		}
	}
	return claims, nil
}

// WebsocketToken mints the short-lived token used by the async query channel.
func WebsocketToken(secret string, channel, userID string, ttlSeconds int) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"channel": channel,
		"user_id": userID,
		"iat":     now.Unix(),
		"exp":     now.Add(time.Duration(ttlSeconds) * time.Second).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}
