package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hazyhaar/pkg/horosafe"
)

// GenerateToken creates a signed JWT string from the given claims.
// The expiry duration is added to the current time to set the ExpiresAt field.
// Returns an error if the secret is shorter than horosafe.MinSecretLen bytes.
func GenerateToken(secret []byte, claims *HorosClaims, expiry time.Duration) (string, error) {
	if err := horosafe.ValidateSecret(secret); err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}

	now := time.Now()
	claims.IssuedAt = jwt.NewNumericDate(now)
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(expiry))

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ValidateToken parses and validates a JWT string, returning the structured HorosClaims.
// Strictly pins the signing method to HS256 to prevent algorithm confusion attacks.
func ValidateToken(secret []byte, tokenStr string) (*HorosClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &HorosClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v (only HS256 allowed)", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*HorosClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, errors.New("invalid token")
}

// ValidateTokenMapClaims parses a JWT and returns raw MapClaims for backward
// compatibility with services that still expect unstructured claims.
// Strictly pins the signing method to HS256.
func ValidateTokenMapClaims(secret []byte, tokenStr string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v (only HS256 allowed)", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, errors.New("invalid token")
}
