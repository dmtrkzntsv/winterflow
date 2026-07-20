// Package org serves organization member management: create accounts with
// temp passwords, list members, change roles, reset passwords, remove.
// Every route here sits behind the rbac.RequireAdmin middleware; handlers
// additionally scope all operations to the caller's own organization.
package org

import (
	"context"
	"crypto/rand"
	"net/http"
	"strings"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

// OrgStore is the slice of port.UserService this handler needs.
type OrgStore interface {
	PrimaryOrganizationID(ctx context.Context, userID string) (string, error)
	CreateMemberUser(ctx context.Context, orgID, name, email, role, tempPassword string) (model.User, error)
	ListMembers(ctx context.Context, orgID string) ([]model.Member, error)
	UpdateMemberRole(ctx context.Context, orgID, userID, role string) error
	RemoveMember(ctx context.Context, orgID, userID string) error
	SetPassword(ctx context.Context, userID, password string, mustChange bool) error
	GetCredentials(ctx context.Context, userID string) (model.Credentials, error)
	GetOrganization(ctx context.Context, orgID string) (model.Organization, error)
	UpdateOrganization(ctx context.Context, orgID, name, icon, color string) error
}

type Deps struct {
	Logger *logger.Logger
	Users  OrgStore
}

type Handler struct {
	log   *logger.Logger
	users OrgStore
}

func NewHandler(d *Deps) *Handler {
	return &Handler{log: d.Logger, users: d.Users}
}

const tempPasswordLen = 16
const passwordAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// randomPassword returns a crypto/rand temp password (shown to the admin
// once; stored only as a bcrypt hash with must_change_password set).
func randomPassword() (string, error) {
	buf := make([]byte, tempPasswordLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, tempPasswordLen)
	for i, b := range buf {
		out[i] = passwordAlphabet[int(b)%len(passwordAlphabet)]
	}
	return string(out), nil
}

// callerOrg resolves the authenticated user and their organization; writes
// the error response itself on failure.
func (h *Handler) callerOrg(w http.ResponseWriter, r *http.Request) (userID, orgID string, ok bool) {
	userID, ok = webutil.RequireUser(w, r)
	if !ok {
		return "", "", false
	}
	orgID, err := h.users.PrimaryOrganizationID(r.Context(), userID)
	if err != nil {
		h.log.Error("resolve caller org", "error", err, "user_id", userID)
		webutil.Error(w, "failed to resolve organization", nil)
		return "", "", false
	}
	return userID, orgID, true
}

func validEmail(email string) bool {
	at := strings.IndexByte(email, '@')
	return at > 0 && at < len(email)-1 && !strings.ContainsAny(email, " \t")
}

type createUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// CreateUser provisions an account in the caller's org. The response carries
// the generated temp password — the only time it is ever shown.
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[createUserRequest](w, r)
	if !ok {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 64 {
		webutil.Error(w, "name is required (max 64 chars)", nil)
		return
	}
	if !validEmail(email) || len(email) > 255 {
		webutil.Error(w, "a valid email is required", nil)
		return
	}
	if req.Role != model.RoleAdmin.Value() && req.Role != model.RoleMember.Value() {
		webutil.Error(w, "role must be admin or member", nil)
		return
	}
	tempPassword, err := randomPassword()
	if err != nil {
		webutil.Error(w, "failed to generate password", nil)
		return
	}
	user, err := h.users.CreateMemberUser(r.Context(), orgID, name, email, req.Role, tempPassword)
	if err != nil {
		if err == model.ErrEmailTaken {
			webutil.Error(w, "email already in use", nil)
			return
		}
		h.log.Error("CreateUser", "error", err, "email", email)
		webutil.Error(w, "failed to create user", nil)
		return
	}
	webutil.Success(w, "user created", struct {
		UserID       string `json:"user_id"`
		Name         string `json:"name"`
		Email        string `json:"email"`
		Role         string `json:"role"`
		TempPassword string `json:"temp_password"`
	}{user.ID, user.Name, email, req.Role, tempPassword})
}

// GetMembers lists the caller's organization members.
func (h *Handler) GetMembers(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	members, err := h.users.ListMembers(r.Context(), orgID)
	if err != nil {
		h.log.Error("GetMembers", "error", err, "org_id", orgID)
		webutil.Error(w, "failed to list members", nil)
		return
	}
	webutil.Success(w, "members", members)
}

type updateMemberRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// UpdateMember changes a member's role within the caller's org.
func (h *Handler) UpdateMember(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[updateMemberRequest](w, r)
	if !ok {
		return
	}
	if req.UserID == "" {
		webutil.Error(w, "user_id is required", nil)
		return
	}
	if req.Role != model.RoleAdmin.Value() && req.Role != model.RoleMember.Value() && req.Role != model.RoleOwner.Value() {
		webutil.Error(w, "invalid role", nil)
		return
	}
	if err := h.users.UpdateMemberRole(r.Context(), orgID, req.UserID, req.Role); err != nil {
		if err == model.ErrLastOwner {
			webutil.Error(w, "cannot demote the last owner", nil)
			return
		}
		webutil.Error(w, "failed to update member", nil)
		return
	}
	webutil.Success(w, "member updated", nil)
}

type memberRequest struct {
	UserID string `json:"user_id"`
}

// RemoveMember deletes a member (and their user account) from the caller's
// org. Self-removal is refused — hand ownership over first.
func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	callerID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[memberRequest](w, r)
	if !ok {
		return
	}
	if req.UserID == "" {
		webutil.Error(w, "user_id is required", nil)
		return
	}
	if req.UserID == callerID {
		webutil.Error(w, "you cannot remove yourself", nil)
		return
	}
	if err := h.users.RemoveMember(r.Context(), orgID, req.UserID); err != nil {
		if err == model.ErrLastOwner {
			webutil.Error(w, "cannot remove the last owner", nil)
			return
		}
		webutil.Error(w, "failed to remove member", nil)
		return
	}
	webutil.Success(w, "member removed", nil)
}

// ResetMemberPassword issues a new temp password (shown once) for a member
// with local credentials and forces a change on next login.
func (h *Handler) ResetMemberPassword(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[memberRequest](w, r)
	if !ok {
		return
	}
	if req.UserID == "" {
		webutil.Error(w, "user_id is required", nil)
		return
	}
	// Only members of the caller's org with local credentials qualify.
	if _, err := h.users.GetCredentials(r.Context(), req.UserID); err != nil {
		webutil.Error(w, "user has no local credentials", nil)
		return
	}
	if memberOf, err := h.users.PrimaryOrganizationID(r.Context(), req.UserID); err != nil || memberOf != orgID {
		webutil.Error(w, "user is not a member of your organization", nil)
		return
	}
	tempPassword, err := randomPassword()
	if err != nil {
		webutil.Error(w, "failed to generate password", nil)
		return
	}
	if err := h.users.SetPassword(r.Context(), req.UserID, tempPassword, true); err != nil {
		webutil.Error(w, "failed to reset password", nil)
		return
	}
	webutil.Success(w, "password reset", struct {
		UserID       string `json:"user_id"`
		TempPassword string `json:"temp_password"`
	}{req.UserID, tempPassword})
}

// GetOrganization returns the caller's org identity (name, icon, color).
// Visible to every member — only updates are admin-gated.
func (h *Handler) GetOrganization(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	org, err := h.users.GetOrganization(r.Context(), orgID)
	if err != nil {
		h.log.Error("GetOrganization", "error", err, "org_id", orgID)
		webutil.Error(w, "failed to load organization", nil)
		return
	}
	webutil.Success(w, "organization", org)
}

type updateOrganizationRequest struct {
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	Color string `json:"color"`
}

// UpdateOrganization sets the org's name/icon/color (admin-only via route).
func (h *Handler) UpdateOrganization(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[updateOrganizationRequest](w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 64 {
		webutil.Error(w, "name is required (max 64 chars)", nil)
		return
	}
	if len(req.Icon) > 64 || len(req.Color) > 7 {
		webutil.Error(w, "invalid icon or color", nil)
		return
	}
	if err := h.users.UpdateOrganization(r.Context(), orgID, name, req.Icon, req.Color); err != nil {
		h.log.Error("UpdateOrganization", "error", err, "org_id", orgID)
		webutil.Error(w, "failed to update organization", nil)
		return
	}
	webutil.Success(w, "organization updated", nil)
}
