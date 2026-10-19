package iam

import (
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

var overviewScopeSchema = httpx.NewSchema[OverviewScope]("iam.overview-scope", schemaFields("all tenantId", "", ""))
var schedulerSchema = httpx.NewSchema[SchedulerRef]("iam.scheduler", schemaFields("leaderPresent", "", ""))
var outboxStatusSchema = httpx.NewSchema[OutboxStatus]("iam.outbox-status", schemaFields("pending oldestSeconds", "", ""))
var jobStatusSchema = httpx.NewSchema[JobStatus]("iam.job-status", schemaFields("queued running failed24h succeeded24h", "", ""))
var statusSchema = httpx.NewSchema[SystemStatus]("iam.system-status", map[string]httpx.Field{
	"database": {Kind: httpx.Safe}, "redis": {Kind: httpx.Safe}, "scheduler": {Kind: httpx.Nested, Schema: schedulerSchema}, "outbox": {Kind: httpx.Nested, Schema: outboxStatusSchema}, "jobs": {Kind: httpx.Nested, Schema: jobStatusSchema},
})
var userCountSchema = httpx.NewSchema[UserCounts]("iam.user-counts", map[string]httpx.Field{"total": {Kind: httpx.Safe}, "byStatus": {Kind: httpx.Safe, MapKeys: []string{"active", "disabled", "reset_required", "deleted"}}})
var sessionCountSchema = httpx.NewSchema[SessionCounts]("iam.session-counts", map[string]httpx.Field{"active": {Kind: httpx.Safe}, "revoked24h": {Kind: httpx.Safe}, "revokedByReason24h": {Kind: httpx.Safe, MapKeys: []string{"logout", "disabled", "password_reset", "password_changed", "claims_changed", "admin_revoked", "admin_revoke", "logout_all", "revoked", "refresh_reuse", "refresh_token_reuse", "device_relogin", "expired"}}})
var loginCountSchema = httpx.NewSchema[LoginCounts]("iam.login-counts", schemaFields("failed24h lockouts24h", "", ""))
var activitySchema = httpx.NewSchema[security.Activity]("iam.activity", map[string]httpx.Field{"last24h": {Kind: httpx.Safe}, "last7d": {Kind: httpx.Safe}, "bySeverity24h": {Kind: httpx.Safe, MapKeys: []string{"info", "warning", "critical"}}, "hourly24h": {Kind: httpx.Safe}, "daily7d": {Kind: httpx.Safe}})
var installCountsSchema = httpx.NewSchema[InstallCounts]("iam.install-counts", schemaFields("total seen7d belowFloor belowLatest unknownVersion minAllowedVersion latestVersion", "", ""))
var quarantineCountsSchema = httpx.NewSchema[QuarantineCounts]("iam.quarantine-counts", map[string]httpx.Field{"rows": {Kind: httpx.Safe}, "byTable": {Kind: httpx.Safe, MapKeys: []string{"api_keys", "auth_identities", "billing_counters", "billing_parties", "billing_statement_lines", "billing_statements", "broadcast_reads", "broadcast_recipients", "broadcasts", "chat_messages", "chat_read_state", "chats", "companies", "consumer_inbox", "customer_driver_id_types", "customer_fuel_rate_adjustments", "customer_rate_entries", "customer_service_fees", "customers", "device_tokens", "driver_advances", "driver_compensation_configs", "driver_customer_codes", "driver_penalties", "drivers", "file_objects", "fuel_daily_snapshots", "fuel_monthly_snapshots", "holidays", "hub_name_aliases", "hub_soc_distances", "hubs", "idempotency_keys", "incident_reports", "jobs", "leave_request_attachments", "leave_requests", "maintenance_files", "maintenance_records", "membership_provenance_history", "memberships", "mobile_app_releases", "mobile_installations", "notification_deliveries", "outbox_events", "partner_interest", "password_reset_tokens", "payroll_line_items", "payroll_penalty_applications", "payroll_runs", "penalty_types", "refresh_tokens", "role_capability_overrides", "security_events", "sessions", "settings", "standby_photos", "standby_rate_entries", "standby_records", "statement_documents", "status_history", "support_visits", "task_delivery_stops", "task_number_counters", "tasks", "tenant_files", "tenants", "transactions", "trip_billing_snapshots", "trip_billing_stop_breakdown", "trip_delivery_stops", "trip_no_history", "trip_photos", "trip_records", "truck_assignments", "truck_files", "trucks", "user_platform_roles", "user_scopes", "users", "vehicle_expenses", "vehicle_locations", "waitlist"}}})
var _ = httpx.NewSchema[Overview]("iam.platform-overview", map[string]httpx.Field{
	"generatedAt": {Kind: httpx.Safe}, "scope": {Kind: httpx.Nested, Schema: overviewScopeSchema}, "users": {Kind: httpx.Nested, Schema: userCountSchema}, "sessions": {Kind: httpx.Nested, Schema: sessionCountSchema}, "logins": {Kind: httpx.Nested, Schema: loginCountSchema}, "events": {Kind: httpx.Nested, Schema: activitySchema}, "installations": {Kind: httpx.Nested, Schema: installCountsSchema}, "quarantine": {Kind: httpx.Nested, Schema: quarantineCountsSchema}, "system": {Kind: httpx.Nested, Schema: statusSchema},
})
var _ = httpx.NewSchema[UsersByRole]("iam.platform-role-counts", map[string]httpx.Field{
	"scope": {Kind: httpx.Nested, Schema: overviewScopeSchema}, "total": {Kind: httpx.Safe}, "none": {Kind: httpx.Safe}, "tenantRoles": {Kind: httpx.Safe, MapKeys: []string{"tenant_admin", "manager", "operation_staff", "operator", "user", "driver"}}, "scopes": {Kind: httpx.Safe, MapKeys: []string{"customer", "dispatcher"}}, "platformRoles": {Kind: httpx.Safe, MapKeys: []string{"platform_admin", "support"}},
})
var _ = httpx.NewSchema[APIKey]("iam.platform-api-key", func() map[string]httpx.Field {
	f := schemaFields("id scope capabilities tenantId ratePerMin status createdBy createdAt expiresAt lastUsedAt revokedAt revokedBy", "tenantNameTh tenantNameEn createdByEmail", "")
	f["name"] = httpx.Field{Kind: httpx.FreeText}
	f["keyPrefix"] = httpx.Field{Kind: httpx.FreeText}
	return f
}(), func(k APIKey) string {
	if k.TenantID != nil {
		return k.TenantID.String()
	}
	return ""
})

func tenantFields() map[string]httpx.Field {
	return schemaFields("id kind code nameTh nameEn status legalType contractorTenantId createdAt billingPartyId", "", "")
}

var _ = httpx.NewSchema[Tenant]("iam.tenant", tenantFields(), func(t Tenant) string { return t.ID.String() })
var statusHistorySchema = httpx.NewSchema[StatusHistory]("iam.status-history", func() map[string]httpx.Field {
	f := schemaFields("status previousStatus changedAt changedBy", "changedByLabel", "")
	f["reason"] = httpx.Field{Kind: httpx.FreeText}
	return f
}(), func(StatusHistory) string { return "" })

func tenantDetailFields() map[string]httpx.Field {
	f := tenantFields()
	for k, v := range schemaFields("fleetSize billingDateBasis updatedAt", "idCardNumber taxId contactPerson phone email", "") {
		f[k] = v
	}
	// The stored region/type lists are arbitrary strings, not a reviewed enum vocabulary.
	for _, name := range []string{"website", "designation", "lineGroupId", "serviceRegions", "vehicleTypes"} {
		f[name] = httpx.Field{Kind: httpx.FreeText}
	}
	for _, name := range []string{"address", "dispatchCenter"} {
		f[name] = httpx.Field{Kind: httpx.Address}
	}
	f["statusHistory"] = httpx.Field{Kind: httpx.Nested, Schema: statusHistorySchema}
	return f
}

var _ = httpx.NewSchema[TenantDetail]("iam.tenant-detail", tenantDetailFields(), func(t TenantDetail) string { return t.ID.String() })
var tenantDocumentSchema = httpx.NewSchema[TenantDocument]("iam.tenant-document", map[string]httpx.Field{"fileId": {Kind: httpx.File}, "key": {Kind: httpx.Secret}, "url": {Kind: httpx.File, BrokerPath: true}, "kind": {Kind: httpx.Safe}, "position": {Kind: httpx.Safe}})
var _ = httpx.NewSchema[Subcontractor]("iam.subcontractor", func() map[string]httpx.Field {
	f := tenantDetailFields()
	f["documents"] = httpx.Field{Kind: httpx.Nested, Schema: tenantDocumentSchema}
	return f
}(), func(t Subcontractor) string { return t.ID.String() })
var _ = httpx.NewSchema[SubcontractorSummary]("iam.subcontractor-summary", func() map[string]httpx.Field {
	f := schemaFields("id code nameTh nameEn legalType status fleetSize contractorTenantId billingPartyId billingDateBasis createdAt", "contactPerson phone email", "")
	f["serviceRegions"] = httpx.Field{Kind: httpx.FreeText}
	f["lineGroupId"] = httpx.Field{Kind: httpx.FreeText}
	return f
}(), func(t SubcontractorSummary) string { return t.ID.String() })

// These write-only contracts return a newly generated credential once, after D2 authorization.
// Stored hashes/secrets never enter either DTO; reads use the classified user and API-key schemas exclusively.
var _ = httpx.NewSchema[CreateUserResult]("iam.created-user", map[string]httpx.Field{"user": {Kind: httpx.Nested, Schema: userSchema}, "temporaryPassword": {Kind: httpx.Safe}})
var _ = httpx.NewSchema[CreatedAPIKey]("iam.created-api-key", func() map[string]httpx.Field {
	f := schemaFields("id scope capabilities tenantId ratePerMin status createdBy createdAt expiresAt lastUsedAt revokedAt revokedBy secret", "tenantNameTh tenantNameEn createdByEmail", "")
	f["name"] = httpx.Field{Kind: httpx.FreeText}
	f["keyPrefix"] = httpx.Field{Kind: httpx.FreeText}
	return f
}(), func(k CreatedAPIKey) string {
	if k.TenantID != nil {
		return k.TenantID.String()
	}
	return ""
})

var _ = httpx.NewSchema[SubcontractorMinimal]("iam.subcontractor-minimal", schemaFields("id code nameTh nameEn status billingPartyId", "", ""), func(t SubcontractorMinimal) string { return t.ID.String() })
