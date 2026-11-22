package auth

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/social-media-nlp/refactor/backend/db/sqlc"
)

type PublicUser struct {
	ID           uuid.UUID  `json:"id"`
	UserName     string     `json:"userName"`
	NickName     string     `json:"nickName"`
	Email        string     `json:"email"`
	Role         string     `json:"role"`
	AvatarURL    *string    `json:"avatarUrl"`
	CoverPageURL *string    `json:"coverPageUrl"`
	Bio          *string    `json:"bio"`
	DateOfBirth  *time.Time `json:"dateOfBirth"`
	IsActive     bool       `json:"isActive"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastLoginAt  *time.Time `json:"lastLoginAt"`
}

type AuthResponse struct {
	AccessToken string     `json:"accessToken"`
	User        PublicUser `json:"user"`
}

type RegisterRequest struct {
	Username     string  `json:"username"`
	UserName     string  `json:"userName"`
	NickName     string  `json:"nickName"`
	Email        string  `json:"email"`
	Password     string  `json:"password"`
	AvatarURL    *string `json:"avatarUrl,omitempty"`
	CoverPageURL *string `json:"coverPageUrl,omitempty"`
}

type LoginRequest struct {
	Identifier string `json:"identifier"`
	Username   string `json:"username"`
	Email      string `json:"email"`
	Password   string `json:"password"`
}

type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

type ResetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
}

type ChangePasswordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

type AuthClaims struct {
	UserID   uuid.UUID
	UserName string
	Role     string
	JTI      string
}

func pgTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

func publicUserFromCreateRow(u sqlc.CreateUserRow) PublicUser {
	return PublicUser{
		ID:           u.ID,
		UserName:     u.UserName,
		NickName:     u.NickName,
		Email:        u.Email,
		Role:         string(u.Role),
		AvatarURL:    u.AvatarUrl,
		CoverPageURL: u.CoverPageUrl,
		Bio:          u.Bio,
		DateOfBirth:  pgTimePtr(u.DateOfBirth),
		IsActive:     u.IsActive,
		CreatedAt:    u.CreatedAt.Time.UTC(),
		LastLoginAt:  pgTimePtr(u.LastLoginAt),
	}
}

func publicUserFromGetByIDRow(u sqlc.GetPublicUserByIDRow) PublicUser {
	return PublicUser{
		ID:           u.ID,
		UserName:     u.UserName,
		NickName:     u.NickName,
		Email:        u.Email,
		Role:         string(u.Role),
		AvatarURL:    u.AvatarUrl,
		CoverPageURL: u.CoverPageUrl,
		Bio:          u.Bio,
		DateOfBirth:  pgTimePtr(u.DateOfBirth),
		IsActive:     u.IsActive,
		CreatedAt:    u.CreatedAt.Time.UTC(),
		LastLoginAt:  pgTimePtr(u.LastLoginAt),
	}
}

func publicUserFromGetByEmailRow(u sqlc.GetPublicUserByEmailRow) PublicUser {
	return PublicUser{
		ID:           u.ID,
		UserName:     u.UserName,
		NickName:     u.NickName,
		Email:        u.Email,
		Role:         string(u.Role),
		AvatarURL:    u.AvatarUrl,
		CoverPageURL: u.CoverPageUrl,
		Bio:          u.Bio,
		DateOfBirth:  pgTimePtr(u.DateOfBirth),
		IsActive:     u.IsActive,
		CreatedAt:    u.CreatedAt.Time.UTC(),
		LastLoginAt:  pgTimePtr(u.LastLoginAt),
	}
}

func publicUserFromUpdateLoginRow(u sqlc.UpdateLastLoginAtRow) PublicUser {
	return PublicUser{
		ID:           u.ID,
		UserName:     u.UserName,
		NickName:     u.NickName,
		Email:        u.Email,
		Role:         string(u.Role),
		AvatarURL:    u.AvatarUrl,
		CoverPageURL: u.CoverPageUrl,
		Bio:          u.Bio,
		DateOfBirth:  pgTimePtr(u.DateOfBirth),
		IsActive:     u.IsActive,
		CreatedAt:    u.CreatedAt.Time.UTC(),
		LastLoginAt:  pgTimePtr(u.LastLoginAt),
	}
}

func publicUserFromUpdateLoginForEpochRow(u sqlc.UpdateLastLoginAtForEpochRow) PublicUser {
	return PublicUser{
		ID:           u.ID,
		UserName:     u.UserName,
		NickName:     u.NickName,
		Email:        u.Email,
		Role:         string(u.Role),
		AvatarURL:    u.AvatarUrl,
		CoverPageURL: u.CoverPageUrl,
		Bio:          u.Bio,
		DateOfBirth:  pgTimePtr(u.DateOfBirth),
		IsActive:     u.IsActive,
		CreatedAt:    u.CreatedAt.Time.UTC(),
		LastLoginAt:  pgTimePtr(u.LastLoginAt),
	}
}
