# UI closure evidence — 2026-10-19

75 source routes; each has before/after screenshots at 1280×900 and 360×900. Before is the frozen mv-go `50d3ba25` web build; after includes this UI change. Both use the same local synthetic BFF fixtures. Redirect aliases and explicit missing/denied states are retained as routes. No production records or credentials are included.

The manifest records headings, language, viewport/root width and render-error checks. All after pages have a visible H1, no page-error boundary and no root horizontal overflow. This captures page states, not every possible business operation. The independent source reports and component/regression tests cover the control flows.

| Route | Desktop before | Desktop after | Mobile before | Mobile after |
|---|---|---|---|---|
| `/forgot-password` | [before](forgot-password-1280-before.png) | [after](forgot-password-1280-after.png) | [before](forgot-password-360-before.png) | [after](forgot-password-360-after.png) |
| `/login` | [before](login-1280-before.png) | [after](login-1280-after.png) | [before](login-360-before.png) | [after](login-360-after.png) |
| `/reset-password` | [before](reset-password-1280-before.png) | [after](reset-password-1280-after.png) | [before](reset-password-360-before.png) | [after](reset-password-360-after.png) |
| `/waitlist` | [before](waitlist-1280-before.png) | [after](waitlist-1280-after.png) | [before](waitlist-360-before.png) | [after](waitlist-360-after.png) |
| `/about` | [before](about-1280-before.png) | [after](about-1280-after.png) | [before](about-360-before.png) | [after](about-360-after.png) |
| `/app/[...missing]` | [before](app-missing-1280-before.png) | [after](app-missing-1280-after.png) | [before](app-missing-360-before.png) | [after](app-missing-360-after.png) |
| `/app/accounting/audit` | [before](app-accounting-audit-1280-before.png) | [after](app-accounting-audit-1280-after.png) | [before](app-accounting-audit-360-before.png) | [after](app-accounting-audit-360-after.png) |
| `/app/accounting/billing-document` | [before](app-accounting-billing-document-1280-before.png) | [after](app-accounting-billing-document-1280-after.png) | [before](app-accounting-billing-document-360-before.png) | [after](app-accounting-billing-document-360-after.png) |
| `/app/accounting/billing-result` | [before](app-accounting-billing-result-1280-before.png) | [after](app-accounting-billing-result-1280-after.png) | [before](app-accounting-billing-result-360-before.png) | [after](app-accounting-billing-result-360-after.png) |
| `/app/accounting/fuel` | [before](app-accounting-fuel-1280-before.png) | [after](app-accounting-fuel-1280-after.png) | [before](app-accounting-fuel-360-before.png) | [after](app-accounting-fuel-360-after.png) |
| `/app/accounting/fuel-price-history` | [before](app-accounting-fuel-price-history-1280-before.png) | [after](app-accounting-fuel-price-history-1280-after.png) | [before](app-accounting-fuel-price-history-360-before.png) | [after](app-accounting-fuel-price-history-360-after.png) |
| `/app/accounting/income` | [before](app-accounting-income-1280-before.png) | [after](app-accounting-income-1280-after.png) | [before](app-accounting-income-360-before.png) | [after](app-accounting-income-360-after.png) |
| `/app/accounting/other` | [before](app-accounting-other-1280-before.png) | [after](app-accounting-other-1280-after.png) | [before](app-accounting-other-360-before.png) | [after](app-accounting-other-360-after.png) |
| `/app/accounting/rate-card` | [before](app-accounting-rate-card-1280-before.png) | [after](app-accounting-rate-card-1280-after.png) | [before](app-accounting-rate-card-360-before.png) | [after](app-accounting-rate-card-360-after.png) |
| `/app/accounting/shopee-express-report` | [before](app-accounting-shopee-express-report-1280-before.png) | [after](app-accounting-shopee-express-report-1280-after.png) | [before](app-accounting-shopee-express-report-360-before.png) | [after](app-accounting-shopee-express-report-360-after.png) |
| `/app/chat` | [before](app-chat-1280-before.png) | [after](app-chat-1280-after.png) | [before](app-chat-360-before.png) | [after](app-chat-360-after.png) |
| `/app/chat/room` | [before](app-chat-room-1280-before.png) | [after](app-chat-room-1280-after.png) | [before](app-chat-room-360-before.png) | [after](app-chat-room-360-after.png) |
| `/app/chat/with-driver` | [before](app-chat-with-driver-1280-before.png) | [after](app-chat-with-driver-1280-after.png) | [before](app-chat-with-driver-360-before.png) | [after](app-chat-with-driver-360-after.png) |
| `/app/companies` | [before](app-companies-1280-before.png) | [after](app-companies-1280-after.png) | [before](app-companies-360-before.png) | [after](app-companies-360-after.png) |
| `/app/customers/[id]/edit` | [before](app-customers-id-edit-1280-before.png) | [after](app-customers-id-edit-1280-after.png) | [before](app-customers-id-edit-360-before.png) | [after](app-customers-id-edit-360-after.png) |
| `/app/customers/[id]` | [before](app-customers-id-1280-before.png) | [after](app-customers-id-1280-after.png) | [before](app-customers-id-360-before.png) | [after](app-customers-id-360-after.png) |
| `/app/customers/new` | [before](app-customers-new-1280-before.png) | [after](app-customers-new-1280-after.png) | [before](app-customers-new-360-before.png) | [after](app-customers-new-360-after.png) |
| `/app/customers` | [before](app-customers-1280-before.png) | [after](app-customers-1280-after.png) | [before](app-customers-360-before.png) | [after](app-customers-360-after.png) |
| `/app/dashboard` | [before](app-dashboard-1280-before.png) | [after](app-dashboard-1280-after.png) | [before](app-dashboard-360-before.png) | [after](app-dashboard-360-after.png) |
| `/app/driver-monitor` | [before](app-driver-monitor-1280-before.png) | [after](app-driver-monitor-1280-after.png) | [before](app-driver-monitor-360-before.png) | [after](app-driver-monitor-360-after.png) |
| `/app/drivers/edit` | [before](app-drivers-edit-1280-before.png) | [after](app-drivers-edit-1280-after.png) | [before](app-drivers-edit-360-before.png) | [after](app-drivers-edit-360-after.png) |
| `/app/drivers/new` | [before](app-drivers-new-1280-before.png) | [after](app-drivers-new-1280-after.png) | [before](app-drivers-new-360-before.png) | [after](app-drivers-new-360-after.png) |
| `/app/drivers` | [before](app-drivers-1280-before.png) | [after](app-drivers-1280-after.png) | [before](app-drivers-360-before.png) | [after](app-drivers-360-after.png) |
| `/app/drivers/view` | [before](app-drivers-view-1280-before.png) | [after](app-drivers-view-1280-after.png) | [before](app-drivers-view-360-before.png) | [after](app-drivers-view-360-after.png) |
| `/app/first-mile` | [before](app-first-mile-1280-before.png) | [after](app-first-mile-1280-after.png) | [before](app-first-mile-360-before.png) | [after](app-first-mile-360-after.png) |
| `/app/holidays` | [before](app-holidays-1280-before.png) | [after](app-holidays-1280-after.png) | [before](app-holidays-360-before.png) | [after](app-holidays-360-after.png) |
| `/app/incident-reports` | [before](app-incident-reports-1280-before.png) | [after](app-incident-reports-1280-after.png) | [before](app-incident-reports-360-before.png) | [after](app-incident-reports-360-after.png) |
| `/app/job-assign` | [before](app-job-assign-1280-before.png) | [after](app-job-assign-1280-after.png) | [before](app-job-assign-360-before.png) | [after](app-job-assign-360-after.png) |
| `/app/leave-requests` | [before](app-leave-requests-1280-before.png) | [after](app-leave-requests-1280-after.png) | [before](app-leave-requests-360-before.png) | [after](app-leave-requests-360-after.png) |
| `/app/line-haul` | [before](app-line-haul-1280-before.png) | [after](app-line-haul-1280-after.png) | [before](app-line-haul-360-before.png) | [after](app-line-haul-360-after.png) |
| `/app/maintenance` | [before](app-maintenance-1280-before.png) | [after](app-maintenance-1280-after.png) | [before](app-maintenance-360-before.png) | [after](app-maintenance-360-after.png) |
| `/app/operations/roles` | [before](app-operations-roles-1280-before.png) | [after](app-operations-roles-1280-after.png) | [before](app-operations-roles-360-before.png) | [after](app-operations-roles-360-after.png) |
| `/app` | [before](app-1280-before.png) | [after](app-1280-after.png) | [before](app-360-before.png) | [after](app-360-after.png) |
| `/app/payroll/config` | [before](app-payroll-config-1280-before.png) | [after](app-payroll-config-1280-after.png) | [before](app-payroll-config-360-before.png) | [after](app-payroll-config-360-after.png) |
| `/app/payroll` | [before](app-payroll-1280-before.png) | [after](app-payroll-1280-after.png) | [before](app-payroll-360-before.png) | [after](app-payroll-360-after.png) |
| `/app/payroll/penalties` | [before](app-payroll-penalties-1280-before.png) | [after](app-payroll-penalties-1280-after.png) | [before](app-payroll-penalties-360-before.png) | [after](app-payroll-penalties-360-after.png) |
| `/app/renewals` | [before](app-renewals-1280-before.png) | [after](app-renewals-1280-after.png) | [before](app-renewals-360-before.png) | [after](app-renewals-360-after.png) |
| `/app/security-center/api-keys` | [before](app-security-center-api-keys-1280-before.png) | [after](app-security-center-api-keys-1280-after.png) | [before](app-security-center-api-keys-360-before.png) | [after](app-security-center-api-keys-360-after.png) |
| `/app/security-center/audit` | [before](app-security-center-audit-1280-before.png) | [after](app-security-center-audit-1280-after.png) | [before](app-security-center-audit-360-before.png) | [after](app-security-center-audit-360-after.png) |
| `/app/security-center/mobile-clients` | [before](app-security-center-mobile-clients-1280-before.png) | [after](app-security-center-mobile-clients-1280-after.png) | [before](app-security-center-mobile-clients-360-before.png) | [after](app-security-center-mobile-clients-360-after.png) |
| `/app/security-center/mobile-release` | [before](app-security-center-mobile-release-1280-before.png) | [after](app-security-center-mobile-release-1280-after.png) | [before](app-security-center-mobile-release-360-before.png) | [after](app-security-center-mobile-release-360-after.png) |
| `/app/security-center` | [before](app-security-center-1280-before.png) | [after](app-security-center-1280-after.png) | [before](app-security-center-360-before.png) | [after](app-security-center-360-after.png) |
| `/app/security-center/roles` | [before](app-security-center-roles-1280-before.png) | [after](app-security-center-roles-1280-after.png) | [before](app-security-center-roles-360-before.png) | [after](app-security-center-roles-360-after.png) |
| `/app/security-center/status` | [before](app-security-center-status-1280-before.png) | [after](app-security-center-status-1280-after.png) | [before](app-security-center-status-360-before.png) | [after](app-security-center-status-360-after.png) |
| `/app/security-center/tenants` | [before](app-security-center-tenants-1280-before.png) | [after](app-security-center-tenants-1280-after.png) | [before](app-security-center-tenants-360-before.png) | [after](app-security-center-tenants-360-after.png) |
| `/app/security-center/users` | [before](app-security-center-users-1280-before.png) | [after](app-security-center-users-1280-after.png) | [before](app-security-center-users-360-before.png) | [after](app-security-center-users-360-after.png) |
| `/app/settings/company-profile` | [before](app-settings-company-profile-1280-before.png) | [after](app-settings-company-profile-1280-after.png) | [before](app-settings-company-profile-360-before.png) | [after](app-settings-company-profile-360-after.png) |
| `/app/settings/support-access` | [before](app-settings-support-access-1280-before.png) | [after](app-settings-support-access-1280-after.png) | [before](app-settings-support-access-360-before.png) | [after](app-settings-support-access-360-after.png) |
| `/app/sources` | [before](app-sources-1280-before.png) | [after](app-sources-1280-after.png) | [before](app-sources-360-before.png) | [after](app-sources-360-after.png) |
| `/app/standby-records` | [before](app-standby-records-1280-before.png) | [after](app-standby-records-1280-after.png) | [before](app-standby-records-360-before.png) | [after](app-standby-records-360-after.png) |
| `/app/subcontractors/[id]/edit` | [before](app-subcontractors-id-edit-1280-before.png) | [after](app-subcontractors-id-edit-1280-after.png) | [before](app-subcontractors-id-edit-360-before.png) | [after](app-subcontractors-id-edit-360-after.png) |
| `/app/subcontractors/[id]` | [before](app-subcontractors-id-1280-before.png) | [after](app-subcontractors-id-1280-after.png) | [before](app-subcontractors-id-360-before.png) | [after](app-subcontractors-id-360-after.png) |
| `/app/subcontractors/new` | [before](app-subcontractors-new-1280-before.png) | [after](app-subcontractors-new-1280-after.png) | [before](app-subcontractors-new-360-before.png) | [after](app-subcontractors-new-360-after.png) |
| `/app/subcontractors` | [before](app-subcontractors-1280-before.png) | [after](app-subcontractors-1280-after.png) | [before](app-subcontractors-360-before.png) | [after](app-subcontractors-360-after.png) |
| `/app/support` | [before](app-support-1280-before.png) | [after](app-support-1280-after.png) | [before](app-support-360-before.png) | [after](app-support-360-after.png) |
| `/app/truck-assignment` | [before](app-truck-assignment-1280-before.png) | [after](app-truck-assignment-1280-after.png) | [before](app-truck-assignment-360-before.png) | [after](app-truck-assignment-360-after.png) |
| `/app/trucks/edit` | [before](app-trucks-edit-1280-before.png) | [after](app-trucks-edit-1280-after.png) | [before](app-trucks-edit-360-before.png) | [after](app-trucks-edit-360-after.png) |
| `/app/trucks/maintenance` | [before](app-trucks-maintenance-1280-before.png) | [after](app-trucks-maintenance-1280-after.png) | [before](app-trucks-maintenance-360-before.png) | [after](app-trucks-maintenance-360-after.png) |
| `/app/trucks/new` | [before](app-trucks-new-1280-before.png) | [after](app-trucks-new-1280-after.png) | [before](app-trucks-new-360-before.png) | [after](app-trucks-new-360-after.png) |
| `/app/trucks` | [before](app-trucks-1280-before.png) | [after](app-trucks-1280-after.png) | [before](app-trucks-360-before.png) | [after](app-trucks-360-after.png) |
| `/app/trucks/renew` | [before](app-trucks-renew-1280-before.png) | [after](app-trucks-renew-1280-after.png) | [before](app-trucks-renew-360-before.png) | [after](app-trucks-renew-360-after.png) |
| `/app/trucks/view` | [before](app-trucks-view-1280-before.png) | [after](app-trucks-view-1280-after.png) | [before](app-trucks-view-360-before.png) | [after](app-trucks-view-360-after.png) |
| `/app/unauthorized` | [before](app-unauthorized-1280-before.png) | [after](app-unauthorized-1280-after.png) | [before](app-unauthorized-360-before.png) | [after](app-unauthorized-360-after.png) |
| `/app/users` | [before](app-users-1280-before.png) | [after](app-users-1280-after.png) | [before](app-users-360-before.png) | [after](app-users-360-after.png) |
| `/app/utilities/backfill` | [before](app-utilities-backfill-1280-before.png) | [after](app-utilities-backfill-1280-after.png) | [before](app-utilities-backfill-360-before.png) | [after](app-utilities-backfill-360-after.png) |
| `/app/utilities/billing-impact` | [before](app-utilities-billing-impact-1280-before.png) | [after](app-utilities-billing-impact-1280-after.png) | [before](app-utilities-billing-impact-360-before.png) | [after](app-utilities-billing-impact-360-after.png) |
| `/app/waitlist` | [before](app-waitlist-1280-before.png) | [after](app-waitlist-1280-after.png) | [before](app-waitlist-360-before.png) | [after](app-waitlist-360-after.png) |
| `/join-network` | [before](join-network-1280-before.png) | [after](join-network-1280-after.png) | [before](join-network-360-before.png) | [after](join-network-360-after.png) |
| `/my-account` | [before](my-account-1280-before.png) | [after](my-account-1280-after.png) | [before](my-account-360-before.png) | [after](my-account-360-after.png) |
| `/` | [before](landing-1280-before.png) | [after](landing-1280-after.png) | [before](landing-360-before.png) | [after](landing-360-after.png) |

Additional proof: [mobile task summaries](mobile-tasks-th.png), [mobile task detail](mobile-task-detail-th.png), [keyboard rate-card tabs](rate-card-tabs-th-360.png), [nested gallery Escape](gallery-escape-parent-th.png), [expense form accessible names](expense-labels-en-360.png).

The audit tab confirmation replaces only its final 360px after image following the full batch; the before image preserves its original 425px overflow. Other screenshots are unchanged. EN/light is the whole-route comparison; the earlier independent representative matrix additionally covered EN/TH × light/dark × 360/1280. Native IAB confirm dialogs blocked browser automation; unit regressions verify cancellation/acceptance, unload, traversal and shell-action guards. The affected temporary tabs were recovered/closed without saving permission changes.

Login and landing pairs are explicitly signed out; account pairs wait for the complete Change password section. This replaces their initial transient redirect/chunk-loading captures. No password was entered or changed.

AX text transcripts normalize trailing whitespace for Git; screenshot pixels are preserved unchanged.
