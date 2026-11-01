package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// userIDKey is the context key carrying the authenticated user id.
type userIDKey struct{}

func contextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey{}, userID)
}

func userIDFromContext(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(userIDKey{}).(string)
	return userID, ok
}

// validateJWT verifies an HS256 JWT signed with secret and returns the
// "sub" claim. It implements just enough of RFC 7519 for the gateway: the
// control plane issues the same tokens with the shared JWT_SECRET.
func validateJWT(tokenStr string, secret []byte) (string, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return "", errors.New("token must have three segments")
	}
	headerBytes, err := decodeSegment(parts[0])
	if err != nil {
		return "", err
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return "", err
	}
	if header.Alg != "HS256" {
		return "", errors.New("unsupported JWT algorithm")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(parts[2])) {
		return "", errors.New("invalid JWT signature")
	}
	payloadBytes, err := decodeSegment(parts[1])
	if err != nil {
		return "", err
	}
	var payload struct {
		Sub string `json:"sub"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return "", err
	}
	if time.Now().Unix() >= payload.Exp {
		return "", errors.New("token expired")
	}
	return payload.Sub, nil
}

func decodeSegment(s string) ([]byte, error) {
	if len(s)%4 != 0 {
		s += strings.Repeat("=", 4-len(s)%4)
	}
	return base64.URLEncoding.DecodeString(s)
}

// requireAuth wraps a handler with bearer-token JWT authentication.
func requireAuth(secret []byte, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"missing bearer token"}`))
			return
		}
		userID, err := validateJWT(strings.TrimPrefix(header, "Bearer "), secret)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"invalid or expired token"}`))
			return
		}
		r = r.WithContext(contextWithUserID(r.Context(), userID))
		next(w, r)
	}
}
