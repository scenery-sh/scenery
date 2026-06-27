package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	authdb "scenery.sh/auth/db/gen"
	"strings"
)

type ListOrganizationsResponse struct {
	CurrentTenantID string                `json:"current_tenant_id"`
	Sessions        []OrganizationSession `json:"sessions"`
}

type CreateOrganizationParams struct {
	Name string `json:"name"`
}

type SwitchOrganizationParams struct {
	TenantID string `json:"tenant_id"`
}

type UpdateOrganizationParams struct {
	Name string `json:"name"`
}

type OrganizationMember struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
}

type ListOrganizationMembersResponse struct {
	Members []OrganizationMember `json:"members"`
}

type InviteMemberParams struct {
	Email string `json:"email"`
	Role  string `json:"role,omitempty"`
}

type InviteMemberResponse struct {
	OK             bool   `json:"ok"`
	Email          string `json:"email"`
	DevInviteToken string `json:"dev_invite_token,omitempty"`
}

type AcceptInviteParams struct {
	Token string `json:"token"`
}

type UpdateMemberRoleParams struct {
	Role string `json:"role"`
}

type DisableMemberParams struct {
	UserID string `json:"user_id"`
}

// ListOrganizations returns all active workspaces for the current user.
//
//scenery:api auth method=GET path=/auth/organizations
func (s *Service) ListOrganizations(ctx context.Context) (*ListOrganizationsResponse, error) {
	authData, err := currentAuthData()
	if err != nil {
		return nil, err
	}
	userID, err := parseUUID(string(authData.UserID))
	if err != nil {
		return nil, unauthenticated("invalid user id")
	}
	memberships, err := s.query.ListUserMemberships(ctx, userID)
	if err != nil {
		return nil, err
	}
	sessions := make([]OrganizationSession, 0, len(memberships))
	for _, membership := range memberships {
		sessions = append(sessions, mapOrganization(membership))
	}
	return &ListOrganizationsResponse{
		CurrentTenantID: strings.TrimSpace(string(authData.TenantID)),
		Sessions:        sessions,
	}, nil
}

// CreateOrganization creates a new first-party workspace owned by the current user.
//
//scenery:api auth method=POST path=/auth/organizations
func (s *Service) CreateOrganization(ctx context.Context, params *CreateOrganizationParams) (*AuthBootstrapResponse, error) {
	if params == nil || strings.TrimSpace(params.Name) == "" {
		return nil, invalidArgument("name is required")
	}
	authData, err := currentAuthData()
	if err != nil {
		return nil, err
	}
	userID, err := parseUUID(string(authData.UserID))
	if err != nil {
		return nil, unauthenticated("invalid user id")
	}

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !user.EmailVerifiedAt.Valid {
		return nil, failedPrecondition("email verification is required")
	}
	tenantID, err := newUUID()
	if err != nil {
		return nil, err
	}
	tenant, err := q.CreateTenant(ctx, authdb.CreateTenantParams{
		ID:   tenantID,
		Name: strings.TrimSpace(params.Name),
	})
	if err != nil {
		return nil, err
	}
	membershipID, err := newUUID()
	if err != nil {
		return nil, err
	}
	if _, err := q.CreateOrganizationMembership(ctx, authdb.CreateOrganizationMembershipParams{
		ID:       membershipID,
		TenantID: tenant.ID,
		UserID:   user.ID,
		Role:     roleOwner,
	}); err != nil {
		return nil, err
	}
	session, err := s.sessionForAuthData(ctx, q, authData, tenant.ID)
	if err != nil {
		return nil, err
	}
	bootstrap, err := s.buildBootstrap(ctx, q, user, tenant.ID, session)
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "organization_created", user.ID, authdb.UUID{}, tenant.ID, session.ID, nil)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return bootstrap, nil
}

// SwitchOrganization scopes the current session to another active workspace.
//
//scenery:api auth method=POST path=/auth/organizations/switch
func (s *Service) SwitchOrganization(ctx context.Context, params *SwitchOrganizationParams) (*AuthBootstrapResponse, error) {
	if params == nil || strings.TrimSpace(params.TenantID) == "" {
		return nil, invalidArgument("tenant_id is required")
	}
	authData, err := currentAuthData()
	if err != nil {
		return nil, err
	}
	userID, err := parseUUID(string(authData.UserID))
	if err != nil {
		return nil, unauthenticated("invalid user id")
	}
	tenantID, err := parseUUID(params.TenantID)
	if err != nil {
		return nil, invalidArgument("tenant_id is invalid")
	}

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if _, err := q.GetActiveMembership(ctx, authdb.GetActiveMembershipParams{UserID: userID, TenantID: tenantID}); err != nil {
		if isNoRows(err) {
			return nil, permissionDenied("workspace access is disabled")
		}
		return nil, err
	}
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	session, err := s.sessionForAuthData(ctx, q, authData, tenantID)
	if err != nil {
		return nil, err
	}
	bootstrap, err := s.buildBootstrap(ctx, q, user, tenantID, session)
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "organization_switched", user.ID, session.ActorUserID, tenantID, session.ID, nil)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return bootstrap, nil
}

// UpdateOrganization renames a workspace. Owners only.
//
//scenery:api auth method=PATCH path=/auth/organizations/:tenantID
func (s *Service) UpdateOrganization(ctx context.Context, tenantID string, params *UpdateOrganizationParams) (*AuthBootstrapResponse, error) {
	if params == nil || strings.TrimSpace(params.Name) == "" {
		return nil, invalidArgument("name is required")
	}
	authData, err := currentAuthData()
	if err != nil {
		return nil, err
	}
	tenantUUID, err := parseUUID(tenantID)
	if err != nil {
		return nil, invalidArgument("tenant_id is invalid")
	}
	userID, err := parseUUID(string(authData.UserID))
	if err != nil {
		return nil, unauthenticated("invalid user id")
	}

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := s.requireOwner(ctx, q, userID, tenantUUID); err != nil {
		return nil, err
	}
	if _, err := q.UpdateTenantName(ctx, authdb.UpdateTenantNameParams{ID: tenantUUID, Name: strings.TrimSpace(params.Name)}); err != nil {
		return nil, err
	}
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	session, err := s.sessionForAuthData(ctx, q, authData, tenantUUID)
	if err != nil {
		return nil, err
	}
	bootstrap, err := s.buildBootstrap(ctx, q, user, tenantUUID, session)
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "organization_updated", user.ID, session.ActorUserID, tenantUUID, session.ID, nil)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return bootstrap, nil
}

// DeleteOrganization soft-deletes a workspace. Owners only.
//
//scenery:api auth method=DELETE path=/auth/organizations/:tenantID
func (s *Service) DeleteOrganization(ctx context.Context, tenantID string) (*AuthBootstrapResponse, error) {
	authData, err := currentAuthData()
	if err != nil {
		return nil, err
	}
	tenantUUID, err := parseUUID(tenantID)
	if err != nil {
		return nil, invalidArgument("tenant_id is invalid")
	}
	userID, err := parseUUID(string(authData.UserID))
	if err != nil {
		return nil, unauthenticated("invalid user id")
	}

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := s.requireOwner(ctx, q, userID, tenantUUID); err != nil {
		return nil, err
	}
	if _, err := q.SoftDeleteTenant(ctx, tenantUUID); err != nil {
		return nil, err
	}
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	nextTenant, err := s.ensureActiveTenant(ctx, q, user, authdb.UUID{})
	if err != nil {
		return nil, err
	}
	session, err := s.sessionForAuthData(ctx, q, authData, nextTenant)
	if err != nil {
		return nil, err
	}
	bootstrap, err := s.buildBootstrap(ctx, q, user, nextTenant, session)
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "organization_deleted", user.ID, session.ActorUserID, tenantUUID, session.ID, nil)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return bootstrap, nil
}

// ListOrganizationMembers lists members in a workspace.
//
//scenery:api auth method=GET path=/auth/organizations/:tenantID/members
func (s *Service) ListOrganizationMembers(ctx context.Context, tenantID string) (*ListOrganizationMembersResponse, error) {
	authData, err := currentAuthData()
	if err != nil {
		return nil, err
	}
	userID, err := parseUUID(string(authData.UserID))
	if err != nil {
		return nil, unauthenticated("invalid user id")
	}
	tenantUUID, err := parseUUID(tenantID)
	if err != nil {
		return nil, invalidArgument("tenant_id is invalid")
	}
	if _, err := s.query.GetActiveMembership(ctx, authdb.GetActiveMembershipParams{UserID: userID, TenantID: tenantUUID}); err != nil {
		if isNoRows(err) {
			return nil, permissionDenied("workspace access is disabled")
		}
		return nil, err
	}
	rows, err := s.query.ListTenantMembers(ctx, tenantUUID)
	if err != nil {
		return nil, err
	}
	members := make([]OrganizationMember, 0, len(rows))
	for _, row := range rows {
		members = append(members, OrganizationMember{
			UserID:      uuidString(row.UserID),
			Email:       strings.TrimSpace(row.PrimaryEmail),
			DisplayName: strings.TrimSpace(row.DisplayName),
			AvatarURL:   strings.TrimSpace(row.AvatarUrl),
			Role:        strings.TrimSpace(row.Role),
			Disabled:    row.DisabledAt.Valid || row.UserDisabledAt.Valid,
		})
	}
	return &ListOrganizationMembersResponse{Members: members}, nil
}

// InviteOrganizationMember creates an email invite for a workspace. Owners only.
//
//scenery:api auth method=POST path=/auth/organizations/:tenantID/invites
func (s *Service) InviteOrganizationMember(ctx context.Context, tenantID string, params *InviteMemberParams) (*InviteMemberResponse, error) {
	if params == nil {
		return nil, invalidArgument("request body is required")
	}
	normalizedEmail, err := normalizeEmail(params.Email)
	if err != nil {
		return nil, invalidArgument(err.Error())
	}
	role := strings.TrimSpace(params.Role)
	if role == "" {
		role = roleMember
	}
	if role != roleOwner && role != roleMember {
		return nil, invalidArgument("role must be owner or member")
	}
	authData, err := currentAuthData()
	if err != nil {
		return nil, err
	}
	userID, err := parseUUID(string(authData.UserID))
	if err != nil {
		return nil, unauthenticated("invalid user id")
	}
	tenantUUID, err := parseUUID(tenantID)
	if err != nil {
		return nil, invalidArgument("tenant_id is invalid")
	}

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := s.requireOwner(ctx, q, userID, tenantUUID); err != nil {
		return nil, err
	}
	rawToken, err := s.createOneTimeToken(ctx, q, tokenPurposeInviteAcceptance, userID, tenantUUID, displayEmail(params.Email), normalizedEmail, map[string]string{"role": role}, defaultInviteTTL)
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "invite_created", userID, authdb.UUID{}, tenantUUID, authdb.UUID{}, map[string]string{"email": normalizedEmail, "role": role})
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	_ = sendInviteEmail(ctx, displayEmail(params.Email), rawToken)
	response := &InviteMemberResponse{OK: true, Email: displayEmail(params.Email)}
	if isLocalRuntime() {
		response.DevInviteToken = rawToken
	}
	return response, nil
}

// AcceptInvite accepts an invite for the signed-in verified user.
//
//scenery:api auth method=POST path=/auth/invites/accept
func (s *Service) AcceptInvite(ctx context.Context, params *AcceptInviteParams) (*AuthBootstrapResponse, error) {
	if params == nil || strings.TrimSpace(params.Token) == "" {
		return nil, invalidArgument("token is required")
	}
	authData, err := currentAuthData()
	if err != nil {
		return nil, err
	}
	userID, err := parseUUID(string(authData.UserID))
	if err != nil {
		return nil, unauthenticated("invalid user id")
	}
	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !user.EmailVerifiedAt.Valid {
		return nil, failedPrecondition("email verification is required")
	}
	oneTime, err := q.ConsumeOneTimeToken(ctx, authdb.ConsumeOneTimeTokenParams{
		TokenHash: tokenHash(params.Token),
		Purpose:   tokenPurposeInviteAcceptance,
	})
	if err != nil {
		if isNoRows(err) {
			return nil, invalidArgument("invite token is invalid or expired")
		}
		return nil, err
	}
	if !oneTime.TenantID.Valid {
		return nil, invalidArgument("invite token is invalid")
	}
	if !strings.EqualFold(strings.TrimSpace(user.NormalizedPrimaryEmail), strings.TrimSpace(oneTime.NormalizedEmail)) {
		return nil, permissionDenied("invite email does not match signed-in user")
	}
	role := roleMember
	var metadata map[string]string
	if len(oneTime.Metadata) > 0 {
		_ = json.Unmarshal(oneTime.Metadata, &metadata)
	}
	if metadata["role"] == roleOwner {
		role = roleOwner
	}
	membershipID, err := newUUID()
	if err != nil {
		return nil, err
	}
	if _, err := q.CreateOrganizationMembership(ctx, authdb.CreateOrganizationMembershipParams{
		ID:              membershipID,
		TenantID:        oneTime.TenantID,
		UserID:          user.ID,
		Role:            role,
		InvitedByUserID: oneTime.UserID,
		InvitedAt:       sql.NullTime{Time: s.clock(), Valid: true},
	}); err != nil {
		return nil, err
	}
	session, err := s.sessionForAuthData(ctx, q, authData, oneTime.TenantID)
	if err != nil {
		return nil, err
	}
	bootstrap, err := s.buildBootstrap(ctx, q, user, oneTime.TenantID, session)
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "invite_accepted", user.ID, session.ActorUserID, oneTime.TenantID, session.ID, nil)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return bootstrap, nil
}

// UpdateOrganizationMemberRole changes a member role while preserving at least one owner.
//
//scenery:api auth method=PATCH path=/auth/organizations/:tenantID/members/:userID
func (s *Service) UpdateOrganizationMemberRole(ctx context.Context, tenantID string, userID string, params *UpdateMemberRoleParams) (*ListOrganizationMembersResponse, error) {
	if params == nil || (params.Role != roleOwner && params.Role != roleMember) {
		return nil, invalidArgument("role must be owner or member")
	}
	tenantUUID, targetUserID, actorUserID, err := s.memberMutationIDs(tenantID, userID)
	if err != nil {
		return nil, err
	}
	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := s.requireOwner(ctx, q, actorUserID, tenantUUID); err != nil {
		return nil, err
	}
	current, err := q.GetActiveMembership(ctx, authdb.GetActiveMembershipParams{UserID: targetUserID, TenantID: tenantUUID})
	if err != nil {
		return nil, err
	}
	if current.Role == roleOwner && params.Role == roleMember {
		if err := s.ensureCanRemoveOwner(ctx, q, tenantUUID); err != nil {
			return nil, err
		}
	}
	if _, err := q.UpdateMembershipRole(ctx, authdb.UpdateMembershipRoleParams{TenantID: tenantUUID, UserID: targetUserID, Role: params.Role}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListOrganizationMembers(ctx, tenantID)
}

// DisableOrganizationMember disables a member in one workspace while preserving at least one owner.
//
//scenery:api auth method=POST path=/auth/organizations/:tenantID/members/disable
func (s *Service) DisableOrganizationMember(ctx context.Context, tenantID string, params *DisableMemberParams) (*ListOrganizationMembersResponse, error) {
	if params == nil || strings.TrimSpace(params.UserID) == "" {
		return nil, invalidArgument("user_id is required")
	}
	tenantUUID, targetUserID, actorUserID, err := s.memberMutationIDs(tenantID, params.UserID)
	if err != nil {
		return nil, err
	}
	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := s.requireOwner(ctx, q, actorUserID, tenantUUID); err != nil {
		return nil, err
	}
	current, err := q.GetActiveMembership(ctx, authdb.GetActiveMembershipParams{UserID: targetUserID, TenantID: tenantUUID})
	if err != nil {
		return nil, err
	}
	if current.Role == roleOwner {
		if err := s.ensureCanRemoveOwner(ctx, q, tenantUUID); err != nil {
			return nil, err
		}
	}
	if _, err := q.DisableMembership(ctx, authdb.DisableMembershipParams{TenantID: tenantUUID, UserID: targetUserID}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListOrganizationMembers(ctx, tenantID)
}

func (s *Service) requireOwner(ctx context.Context, q authdb.Querier, userID authdb.UUID, tenantID authdb.UUID) error {
	membership, err := q.GetActiveMembership(ctx, authdb.GetActiveMembershipParams{UserID: userID, TenantID: tenantID})
	if err != nil {
		if isNoRows(err) {
			return permissionDenied("workspace access is disabled")
		}
		return err
	}
	if membership.Role != roleOwner {
		return permissionDenied("workspace owner role is required")
	}
	return nil
}

func (s *Service) ensureCanRemoveOwner(ctx context.Context, q authdb.Querier, tenantID authdb.UUID) error {
	ownerCount, err := q.CountActiveOwners(ctx, tenantID)
	if err != nil {
		return err
	}
	if ownerCount <= 1 {
		return failedPrecondition("workspace must retain at least one owner")
	}
	return nil
}

func (s *Service) sessionForAuthData(ctx context.Context, q authdb.Querier, authData *AuthData, tenantID authdb.UUID) (authdb.SceneryAuthRefreshSession, error) {
	session := authdb.SceneryAuthRefreshSession{}
	if authData == nil || strings.TrimSpace(authData.SessionID) == "" {
		session.ID, _ = newUUID()
		session.ActiveTenantID = tenantID
		if authData != nil {
			session.ActorUserID, _ = nullableUUID(string(authData.ActorUserID))
			session.ImpersonationID, _ = nullableUUID(authData.ImpersonationID)
		}
		return session, nil
	}
	sessionID, err := parseUUID(authData.SessionID)
	if err != nil {
		return authdb.SceneryAuthRefreshSession{}, unauthenticated("invalid session id")
	}
	session, err = q.SetRefreshSessionTenant(ctx, authdb.SetRefreshSessionTenantParams{
		ID:             sessionID,
		ActiveTenantID: tenantID,
	})
	if err != nil {
		return authdb.SceneryAuthRefreshSession{}, err
	}
	return session, nil
}

func (s *Service) memberMutationIDs(tenantID string, targetUserID string) (authdb.UUID, authdb.UUID, authdb.UUID, error) {
	authData, err := currentAuthData()
	if err != nil {
		return authdb.UUID{}, authdb.UUID{}, authdb.UUID{}, err
	}
	tenantUUID, err := parseUUID(tenantID)
	if err != nil {
		return authdb.UUID{}, authdb.UUID{}, authdb.UUID{}, invalidArgument("tenant_id is invalid")
	}
	targetUUID, err := parseUUID(targetUserID)
	if err != nil {
		return authdb.UUID{}, authdb.UUID{}, authdb.UUID{}, invalidArgument("user_id is invalid")
	}
	actorUUID, err := parseUUID(string(authData.UserID))
	if err != nil {
		return authdb.UUID{}, authdb.UUID{}, authdb.UUID{}, unauthenticated("invalid user id")
	}
	return tenantUUID, targetUUID, actorUUID, nil
}
