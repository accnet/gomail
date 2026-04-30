package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"gomail/internal/config"
	"gomail/internal/db"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Claims struct {
	UserID       uuid.UUID `json:"user_id"`
	Email        string    `json:"email"`
	IsAdmin      bool      `json:"is_admin"`
	IsSuperAdmin bool      `json:"is_super_admin"`
	jwt.RegisteredClaims
}

type Service struct {
	db  *gorm.DB
	cfg config.Config
}

func NewService(database *gorm.DB, cfg config.Config) *Service {
	return &Service{db: database, cfg: cfg}
}

func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

func CheckPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func (s *Service) AccessToken(user db.User) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:       user.ID,
		Email:        user.Email,
		IsAdmin:      user.IsAdmin,
		IsSuperAdmin: user.IsSuperAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.AccessTokenTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.cfg.JWTSecret))
}

func (s *Service) ParseAccessToken(token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(s.cfg.JWTSecret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

func (s *Service) NewRefreshToken(userID uuid.UUID) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	row := db.RefreshToken{
		UserID:    userID,
		TokenHash: tokenHash(token),
		ExpiresAt: time.Now().Add(s.cfg.RefreshTokenTTL),
	}
	if err := s.db.Create(&row).Error; err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) RotateRefreshToken(token string) (db.User, string, error) {
	var old db.RefreshToken
	err := s.db.Where("token_hash = ?", tokenHash(token)).First(&old).Error
	if err != nil {
		return db.User{}, "", err
	}
	if old.RevokedAt != nil || time.Now().After(old.ExpiresAt) {
		now := time.Now()
		_ = s.db.Model(&db.RefreshToken{}).Where("family_id = ?", old.FamilyID).Update("revoked_at", &now).Error
		return db.User{}, "", errors.New("refresh token revoked or expired")
	}
	var user db.User
	if err := s.db.First(&user, "id = ? AND is_active = ?", old.UserID, true).Error; err != nil {
		return db.User{}, "", err
	}
	newToken, err := randomToken()
	if err != nil {
		return db.User{}, "", err
	}
	now := time.Now()
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&old).Update("revoked_at", &now).Error; err != nil {
			return err
		}
		return tx.Create(&db.RefreshToken{
			UserID:    old.UserID,
			TokenHash: tokenHash(newToken),
			FamilyID:  old.FamilyID,
			ExpiresAt: time.Now().Add(s.cfg.RefreshTokenTTL),
		}).Error
	})
	return user, newToken, err
}

func (s *Service) RevokeRefreshToken(token string) error {
	now := time.Now()
	return s.db.Model(&db.RefreshToken{}).Where("token_hash = ?", tokenHash(token)).Update("revoked_at", &now).Error
}

func (s *Service) RevokeUserRefreshTokens(userID uuid.UUID) error {
	now := time.Now()
	return s.db.Model(&db.RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", &now).Error
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
