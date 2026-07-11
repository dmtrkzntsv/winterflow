package models

import (
	"winterflow/internal/infra/db/types"

	"github.com/uptrace/bun"
)

type Organization struct {
	bun.BaseModel `bun:"table:organizations"`

	OrganizationID string         `bun:"organization_id,pk,type:char(36)" json:"organization_id"`
	Name           string         `bun:"name,notnull" json:"name"`
	Icon           string         `bun:"icon,notnull,default:''" json:"icon"`
	Color          string         `bun:"color,notnull,default:''" json:"color"`
	CreatedAt      types.DateTime `bun:"created_at,notnull" json:"created_at"`
}

type OrganizationUser struct {
	bun.BaseModel `bun:"table:organization_users"`

	OrganizationID string         `bun:"organization_id,pk,type:char(36)" json:"organization_id"`
	UserID         string         `bun:"user_id,pk,type:char(36)" json:"user_id"`
	Role           string         `bun:"role,notnull,default:'member'" json:"role"`
	CreatedAt      types.DateTime `bun:"created_at,notnull" json:"created_at"`

	Organization *Organization `bun:"rel:belongs-to,join:organization_id=organization_id"`
	User         *User         `bun:"rel:belongs-to,join:user_id=user_id"`
}

type User struct {
	bun.BaseModel `bun:"table:users"`

	UserID    string         `bun:"user_id,pk,type:char(36)" json:"user_id"`
	Name      string         `bun:"name,notnull" json:"name"`
	Avatar    *string        `bun:"avatar,nullzero" json:"avatar"`
	CreatedAt types.DateTime `bun:"created_at,notnull" json:"created_at"`
	LastSeen  types.DateTime `bun:"last_seen,notnull" json:"last_seen"`

	ConnectedAccounts []UserConnectedAccount `bun:"rel:has-many,join:user_id=user_id"`
	Tokens            []UserToken            `bun:"rel:has-many,join:user_id=user_id"`
}

type UserToken struct {
	bun.BaseModel `bun:"table:user_tokens"`

	TokenID     string          `bun:"token_id,pk,type:char(36)" json:"token_id"`
	UserID      string          `bun:"user_id,type:char(36)" json:"user_id"`
	Name        string          `bun:"name,notnull" json:"name"`
	TokenPrefix string          `bun:"token_prefix,notnull" json:"token_prefix"`
	TokenHash   string          `bun:"token_hash,unique,notnull" json:"-"`
	TokenType   string          `bun:"token_type,notnull" json:"token_type"`
	ExpiresAt   *types.DateTime `bun:"expires_at,nullzero" json:"expires_at"`
	LastUsedAt  *types.DateTime `bun:"last_used_at,nullzero" json:"last_used_at"`
	CreatedAt   types.DateTime  `bun:"created_at,notnull" json:"created_at"`

	User *User `bun:"rel:belongs-to,join:user_id=user_id"`
}

type UserCredentials struct {
	bun.BaseModel `bun:"table:user_credentials"`

	UserID             string         `bun:"user_id,pk,type:char(36)" json:"user_id"`
	Email              string         `bun:"email,unique,notnull" json:"email"`
	PasswordHash       string         `bun:"password_hash,notnull" json:"-"`
	MustChangePassword bool           `bun:"must_change_password,notnull" json:"must_change_password"`
	UpdatedAt          types.DateTime `bun:"updated_at,notnull" json:"updated_at"`

	User *User `bun:"rel:belongs-to,join:user_id=user_id"`
}

type UserConnectedAccount struct {
	bun.BaseModel `bun:"table:user_connected_accounts"`

	Provider   string `bun:"provider,pk" json:"provider"`
	ExternalID string `bun:"external_id,pk" json:"external_id"`
	UserID     string `bun:"user_id,notnull,type:char(36)" json:"user_id"`

	User *User `bun:"rel:belongs-to,join:user_id=user_id"`
}

type Server struct {
	bun.BaseModel `bun:"table:servers"`

	ServerID       string          `bun:"server_id,pk,type:char(36)" json:"server_id"`
	OrganizationID string          `bun:"organization_id,notnull,type:char(36)" json:"organization_id"`
	Name           string          `bun:"name,notnull" json:"name"`
	CreatedAt      types.DateTime  `bun:"created_at,notnull" json:"created_at"`
	LastSeen       *types.DateTime `bun:"last_seen,nullzero" json:"last_seen"`

	Organization *Organization       `bun:"rel:belongs-to,join:organization_id=organization_id"`
	Capabilities []ServerCapability  `bun:"rel:has-many,join:server_id=server_id"`
	Features     []ServerFeature     `bun:"rel:has-many,join:server_id=server_id"`
	Certificates []ServerCertificate `bun:"rel:has-many,join:server_id=server_id"`
	Apps         []App               `bun:"rel:has-many,join:server_id=server_id"`
}

type ServerCapability struct {
	bun.BaseModel `bun:"table:server_capabilities"`

	ServerID  string         `bun:"server_id,pk,type:char(36)" json:"server_id"`
	Name      string         `bun:"name,pk" json:"name"`
	Value     string         `bun:"value,notnull,default:''" json:"value"`
	UpdatedAt types.DateTime `bun:"updated_at,notnull" json:"updated_at"`

	Server *Server `bun:"rel:belongs-to,join:server_id=server_id"`
}

type ServerFeature struct {
	bun.BaseModel `bun:"table:server_features"`

	ServerID  string `bun:"server_id,pk,type:char(36)" json:"server_id"`
	Name      string `bun:"name,pk" json:"name"`
	IsEnabled bool   `bun:"is_enabled,notnull" json:"is_enabled"`

	Server *Server `bun:"rel:belongs-to,join:server_id=server_id"`
}

type ServerRegistration struct {
	bun.BaseModel `bun:"table:server_registrations"`

	ServerID             string         `bun:"server_id,pk,type:char(36)" json:"server_id"`
	CertificateID        string         `bun:"certificate_id,notnull,type:char(36)" json:"certificate_id"`
	Hostname             string         `bun:"hostname,notnull" json:"hostname"`
	Code                 string         `bun:"code,unique,type:char(6)" json:"code"`
	ExpiresAt            types.DateTime `bun:"expires_at,notnull" json:"expires_at"`
	Certificate          string         `bun:"certificate,notnull" json:"certificate"`
	CertificateExpiresAt types.DateTime `bun:"certificate_expires_at,notnull" json:"certificate_expires_at"`
	CreatedAt            types.DateTime `bun:"created_at,notnull" json:"created_at"`
}

type ServerCertificate struct {
	bun.BaseModel `bun:"table:server_certificates"`

	CertificateID string         `bun:"certificate_id,pk,type:char(36)" json:"certificate_id"`
	ServerID      string         `bun:"server_id,notnull,type:char(36)" json:"server_id"`
	Certificate   string         `bun:"certificate,notnull" json:"certificate"`
	IsActive      bool           `bun:"is_active,notnull,default:true" json:"is_active"`
	ExpiresAt     types.DateTime `bun:"expires_at,notnull" json:"expires_at"`
	CreatedAt     types.DateTime `bun:"created_at,notnull" json:"created_at"`

	Server *Server `bun:"rel:belongs-to,join:server_id=server_id"`
}

type App struct {
	bun.BaseModel `bun:"table:apps"`

	AppID      string         `bun:"app_id,pk,type:char(36)" json:"app_id"`
	ServerID   string         `bun:"server_id,notnull,type:char(36)" json:"server_id"`
	TemplateID *string        `bun:"template_id,nullzero,type:char(36)" json:"template_id"`
	Name       string         `bun:"name,notnull" json:"name"`
	Version    string         `bun:"version,notnull,default:''" json:"version"`
	Icon       string         `bun:"icon,notnull" json:"icon"`
	Color      string         `bun:"color,notnull,type:char(7)" json:"color"`
	CreatedAt  types.DateTime `bun:"created_at,notnull" json:"created_at"`

	Server *Server `bun:"rel:belongs-to,join:server_id=server_id"`
}

type AppDomain struct {
	bun.BaseModel `bun:"table:app_domains"`

	// Domain is the PK: lowercased FQDN. The PK is the global uniqueness
	// constraint that makes cross-app/cross-server conflicts impossible to
	// persist, not just impolite.
	Domain       string         `bun:"domain,pk" json:"domain"`
	AppID        string         `bun:"app_id,notnull,type:char(36)" json:"app_id"`
	ServerID     string         `bun:"server_id,notnull,type:char(36)" json:"server_id"`
	Kind         string         `bun:"kind,notnull" json:"kind"` // "route" | "redirect"
	SSL          bool           `bun:"ssl,notnull" json:"ssl"`
	UpstreamPort int            `bun:"upstream_port,notnull,default:0" json:"upstream_port"` // routes only
	Target       string         `bun:"target,notnull,default:''" json:"target"`              // redirects only
	Code         int            `bun:"code,notnull,default:0" json:"code"`                   // redirects only
	UpdatedAt    types.DateTime `bun:"updated_at,notnull" json:"updated_at"`

	App    *App    `bun:"rel:belongs-to,join:app_id=app_id"`
	Server *Server `bun:"rel:belongs-to,join:server_id=server_id"`
}
