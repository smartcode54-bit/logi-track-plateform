package realtime

import "strings"

// The topics of the catalogue without an id (Appendix B §B.4.2).
const (
	TopicGlobal           = "global"
	TopicPlatformSecurity = "platform:security"
)

// Tenant topic families: tenant:{tid}:{family} (Appendix B §B.4.2).
const (
	FamilyTasks            = "tasks"
	FamilyTrips            = "trips"
	FamilyChats            = "chats"
	FamilyFleet            = "fleet"
	FamilyHR               = "hr"
	FamilyExpenses         = "expenses"
	FamilyBilling          = "billing"
	FamilyVehicleLocations = "vehicle_locations"
	FamilyConfig           = "config"
)

// UserTopic, DriverTopic, ChatTopic, TenantTopic and DispatchTopic build the per-entity topics (ids in
// lower case, as uuid.UUID.String writes them). DispatchTopic takes FamilyTasks or FamilyTrips: the
// operational events of one billing party's work, for the dispatchers whose grant (cs) names that party;
// producers publish one per distinct party of the row (billing, linked and stop parties, the set
// app_task_in_scope / app_trip_in_scope check).
func UserTopic(userID string) string              { return "user:" + userID }
func DriverTopic(driverID string) string          { return "driver:" + driverID }
func ChatTopic(chatID string) string              { return "chat:" + chatID }
func TenantTopic(tenantID, family string) string  { return "tenant:" + tenantID + ":" + family }
func DispatchTopic(partyID, family string) string { return "dispatch:" + partyID + ":" + family }

// SSE event names the relay's event types are mapped to (Appendix B §B.4.3); the rest keep their type.
const (
	EventSessionRevoked        = "session.revoked"
	EventTasksChanged          = "tasks.changed"
	EventMaintenanceChanged    = "maintenance.changed"
	EventLeaveChanged          = "leave.changed"
	EventTripReviewChanged     = "trip.review_changed"
	EventMessageCreated        = "message.created"
	EventReadUpdated           = "read.updated"
	EventMobileSettingsChanged = "mobile_settings.changed"
	EventRolesChanged          = "roles.changed"
)

// TypeSessionsRevoked is the outbox event type the relay publishes on user:{uid} for SSE
// session.revoked (auth.RouteSessionsRevoked, R50).
const TypeSessionsRevoked = "user.sessions_revoked"

// EventName maps an event type of the relay (outbox_events.event_type) on a topic to its SSE event name
// (Appendix B §B.4.3, §B.4.4): user.sessions_revoked is session.revoked; on driver:{id} task events are
// tasks.changed, maintenance events maintenance.changed, leave events leave.changed and trip events
// trip.review_changed; on chat:{id} chat.message_created is message.created (the full message) and
// chat.read is read.updated; on global settings.changed is mobile_settings.changed; every other type
// keeps its name. ok is false for an event the topic never carries to a client: the billing events
// trip.priced, trip.repriced and standby.priced on a dispatch topic (dispatchers never see prices), an
// empty type, and a type that could break the SSE framing.
func EventName(topic, eventType string) (name string, ok bool) {
	if eventType == "" || strings.ContainsAny(eventType, "\r\n") {
		return "", false
	}
	if eventType == TypeSessionsRevoked {
		return EventSessionRevoked, true
	}
	family, _, _ := strings.Cut(eventType, ".")
	switch {
	case strings.HasPrefix(topic, "driver:"):
		switch family {
		case "task", "tasks":
			return EventTasksChanged, true
		case "maintenance":
			return EventMaintenanceChanged, true
		case "leave":
			return EventLeaveChanged, true
		case "trip":
			return EventTripReviewChanged, true
		}
	case strings.HasPrefix(topic, "chat:"):
		switch eventType {
		case "chat.message_created":
			return EventMessageCreated, true
		case "chat.read":
			return EventReadUpdated, true
		}
	case topic == TopicGlobal:
		if eventType == "settings.changed" {
			return EventMobileSettingsChanged, true
		}
	case strings.HasPrefix(topic, "dispatch:"):
		switch eventType {
		case "trip.priced", "trip.repriced", "standby.priced":
			return "", false
		}
	}
	return eventType, true
}
