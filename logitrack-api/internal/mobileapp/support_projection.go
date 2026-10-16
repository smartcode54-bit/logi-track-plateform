package mobileapp

import (
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func init() { httpx.RegisterSafeErrorCode(authz.CodeTenantRequired) }

var _ = httpx.NewSchema[Installation]("mobileapp.installation", map[string]httpx.Field{
	"driverId": {Kind: httpx.Safe}, "driverName": {Kind: httpx.Person}, "installId": {Kind: httpx.FreeText}, "tenantId": {Kind: httpx.Safe}, "tenantName": {Kind: httpx.Person}, "platform": {Kind: httpx.Safe}, "appVersion": {Kind: httpx.Safe}, "buildNumber": {Kind: httpx.Safe}, "flavor": {Kind: httpx.Safe}, "firstSeenAt": {Kind: httpx.Safe}, "lastSeenAt": {Kind: httpx.Safe}, "versionStatus": {Kind: httpx.Safe},
}, func(r Installation) string { return r.TenantID.String() })
var _ = httpx.NewSchema[InstallationMeta]("mobileapp.installation-meta", map[string]httpx.Field{"flavor": {Kind: httpx.Safe}, "minAllowedVersion": {Kind: httpx.Safe}, "latestVersion": {Kind: httpx.Safe}})
var statusCountsSchema = httpx.NewSchema[StatusCounts]("mobileapp.status-counts", map[string]httpx.Field{"current": {Kind: httpx.Safe}, "outdated": {Kind: httpx.Safe}, "blocked": {Kind: httpx.Safe}, "ahead": {Kind: httpx.Safe}, "unknown": {Kind: httpx.Safe}})
var belowCountSchema = httpx.NewSchema[BelowCount]("mobileapp.below-count", map[string]httpx.Field{"version": {Kind: httpx.Safe}, "count": {Kind: httpx.Safe}})
var _ = httpx.NewSchema[InstallationStats]("mobileapp.installation-stats", map[string]httpx.Field{
	"since": {Kind: httpx.Safe}, "total": {Kind: httpx.Safe}, "byStatus": {Kind: httpx.Nested, Schema: statusCountsSchema}, "byFlavor": {Kind: httpx.Safe, MapKeys: []string{"dev", "prod"}}, "byPlatform": {Kind: httpx.Safe, MapKeys: []string{"android", "ios", "other"}}, "belowVersion": {Kind: httpx.Nested, Schema: belowCountSchema}, "flavor": {Kind: httpx.Safe}, "minAllowedVersion": {Kind: httpx.Safe}, "latestVersion": {Kind: httpx.Safe},
})
