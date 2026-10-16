package auth

import _ "embed"

// Embed canonical sqlc query sources until P1.0 regenerates the shared query packages.
// The statements run through the caller's existing authorized transaction.

//go:embed queries/support_complete_holder_credential.sql
var supportCompleteHolderCredentialSQL string

//go:embed queries/support_lock_consent_version.sql
var supportLockConsentVersionSQL string

//go:embed queries/support_lock_session_context.sql
var supportLockSessionContextSQL string

//go:embed queries/support_lock_tenant_consent.sql
var supportLockTenantConsentSQL string

//go:embed queries/support_lock_user_sessions.sql
var supportLockUserSessionsSQL string

//go:embed queries/support_read_consent_enabled.sql
var supportReadConsentEnabledSQL string

//go:embed queries/support_read_consent_version.sql
var supportReadConsentVersionSQL string

//go:embed queries/support_read_eligible_tenant.sql
var supportReadEligibleTenantSQL string

//go:embed queries/support_read_open_visit_id.sql
var supportReadOpenVisitIDSQL string

//go:embed queries/support_read_session_context.sql
var supportReadSessionContextSQL string

//go:embed queries/support_read_tenant_consent.sql
var supportReadTenantConsentSQL string

//go:embed queries/support_read_tenant_label.sql
var supportReadTenantLabelSQL string

//go:embed queries/support_read_user_state.sql
var supportReadUserStateSQL string

//go:embed queries/support_stamp_platform_credential_reset.sql
var supportStampPlatformCredentialResetSQL string

//go:embed queries/support_write_session_context.sql
var supportWriteSessionContextSQL string
