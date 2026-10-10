package notify

import (
	"strings"
	"unicode/utf16"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/push"
)

// Push kinds: notification_deliveries.kind (Appendix A §A.2.6). The data.type of a visible task push
// carries the task-type prefix on top of the kind ({first_mile|line_haul}_task_assigned, ...).
const (
	KindTaskAssigned         = "task_assigned"
	KindTaskUnassigned       = "task_unassigned"
	KindTaskCancelled        = "task_cancelled"
	KindTasksChanged         = "tasks_changed"
	KindMaintenanceScheduled = "maintenance_scheduled"
	KindChat                 = "chat"
	KindBroadcast            = "broadcast"
	KindLeaveDecided         = "leave_decided"
	KindSessionRevoked       = "session_revoked"
)

// Android notification channels of the driver app (main spec §7.6, §11.8). task_assignments is the
// channel the legacy task pushes name, although MainActivity.kt never creates it (Android then shows
// them on the app's default channel); chat is created there.
const (
	ChannelChat            = "chat"
	ChannelTaskAssignments = "task_assignments"
)

// Push is one notification, built once and sent to every device of its recipient. Data is the FCM
// data map as the app receives it and as notification_deliveries.payload stores it.
type Push struct {
	Kind         string
	Notification *push.Notification // nil: a silent, data-only push
	Channel      string             // Android channel of a visible push
	Data         map[string]string
}

// Silent reports whether p is data-only.
func (p Push) Silent() bool { return p.Notification == nil }

// Message is p addressed to token. Visible pushes keep the legacy delivery options
// (fn:triggers.ts:286-292, fn:chat.ts:40-46): Android priority high on their channel, APNs sound
// default. Silent pushes are the R21 / R50 shape: Android priority normal, APNs content-available 1
// with apns-priority 5 and apns-push-type background, no notification block.
func (p Push) Message(token string) push.Message {
	m := push.Message{Token: token, Data: p.Data}
	if p.Silent() {
		m.Android = &push.AndroidConfig{Priority: push.PriorityNormal}
		m.APNS = &push.APNSConfig{
			Headers: map[string]string{"apns-priority": "5", "apns-push-type": "background"},
			Payload: &push.APNSPayload{Aps: push.Aps{ContentAvailable: 1}},
		}
		return m
	}
	n := *p.Notification
	m.Notification = &n
	m.Android = &push.AndroidConfig{Priority: push.PriorityHigh, Notification: &push.AndroidNotification{ChannelID: p.Channel}}
	m.APNS = &push.APNSConfig{Payload: &push.APNSPayload{Aps: push.Aps{Sound: "default"}}}
	return m
}

// taskTypeNames are the legacy prefix and label of a task type: anything but line_haul is first mile,
// as fn:triggers.ts:321-322 treats anything but LINE_HAUL.
func taskTypeNames(taskType string) (prefix, label string) {
	if taskType == "line_haul" {
		return "line_haul", "Line Haul"
	}
	return "first_mile", "First Mile"
}

// TaskAssigned is "{prefix}_task_assigned" to the task's driver (fn:triggers.ts:337-345): body
// "{source} → {destination} ({date} {time})" with the plan date as yyyy-MM-dd, the date part only
// when there is a date, and the legacy fallback when the trimmed body is empty. taskRef and driverRef
// are the ids the app knows (the Firestore document ids while APK 3.x is installed).
func TaskAssigned(taskType, taskRef, driverRef, source, destination, date, timeOfDay string) Push {
	prefix, label := taskTypeNames(taskType)
	body := source + " → " + destination
	if date != "" {
		body += " (" + date + " " + timeOfDay + ")"
	}
	body = jsTrim(body)
	if body == "" {
		body = "You have a new " + strings.ToLower(label) + " task."
	}
	return Push{
		Kind: KindTaskAssigned, Channel: ChannelTaskAssignments,
		Notification: &push.Notification{Title: "New task assigned", Body: body},
		Data:         map[string]string{"type": prefix + "_task_assigned", "taskId": taskRef, "driverId": driverRef},
	}
}

// TaskUnassigned is "{prefix}_task_unassigned" to the driver a task was taken from
// (fn:triggers.ts:329-335).
func TaskUnassigned(taskType, taskRef, driverRef string) Push {
	prefix, _ := taskTypeNames(taskType)
	return Push{
		Kind: KindTaskUnassigned, Channel: ChannelTaskAssignments,
		Notification: &push.Notification{Title: "Assignment cancelled", Body: "You have been unassigned from this task."},
		Data:         map[string]string{"type": prefix + "_task_unassigned", "taskId": taskRef, "driverId": driverRef},
	}
}

// TaskCancelled is "{prefix}_task_cancelled" to the driver of a cancelled task
// (fn:triggers.ts:319-327).
func TaskCancelled(taskType, taskRef, driverRef string) Push {
	prefix, label := taskTypeNames(taskType)
	return Push{
		Kind: KindTaskCancelled, Channel: ChannelTaskAssignments,
		Notification: &push.Notification{Title: "Task cancelled", Body: "This " + strings.ToLower(label) + " task has been cancelled."},
		Data:         map[string]string{"type": prefix + "_task_cancelled", "taskId": taskRef, "driverId": driverRef},
	}
}

// TasksChanged is the silent "tasks_changed" push (R21): the app refetches GET /v1/mobile/tasks.
func TasksChanged() Push {
	return Push{Kind: KindTasksChanged, Data: map[string]string{"type": KindTasksChanged}}
}

// MaintenanceScheduled is "maintenance_scheduled" to a truck's driver (fn:triggers.ts:362-391): the
// staff's title and body, else the legacy Thai defaults. The consumer of maintenance.created and
// maintenance.reminder_requested lands with T40.
func MaintenanceScheduled(maintenanceRef, driverRef, title, body string) Push {
	if title == "" {
		title = "นัดเช็คระยะ"
	}
	if body == "" {
		body = "มีงานเช็คระยะ — กรุณาเข้าอู่ตามนัด (ดูรายละเอียดในแอป)"
	}
	return Push{
		Kind: KindMaintenanceScheduled, Channel: ChannelTaskAssignments,
		Notification: &push.Notification{Title: title, Body: body},
		Data:         map[string]string{"type": KindMaintenanceScheduled, "maintenanceId": maintenanceRef, "driverId": driverRef},
	}
}

// ChatMessage is "chat" to the driver of a chat an admin wrote in (fn:chat.ts:174-178): body = the
// first 80 UTF-16 units of the text, "…" when it was longer. chatRef opens the room in the app
// (main.dart:71-82). The consumer of chat.message_created lands with T47.
func ChatMessage(chatRef, messageRef, text string) Push {
	return Push{
		Kind: KindChat, Channel: ChannelChat,
		Notification: &push.Notification{Title: "New message from Admin", Body: jsPreview(text, 80)},
		Data:         map[string]string{"type": KindChat, "chatId": chatRef, "messageId": messageRef},
	}
}

// Broadcast is "broadcast" to each recipient driver (fn:chat.ts:206-212): the title cut to 87 UTF-16
// units plus "…" when longer than 90, the body as ChatMessage; data keeps the legacy empty chatId
// (fn:chat.ts:41). The consumer of broadcast.created lands with T48.
func Broadcast(title, text string) Push {
	if jsLen(title) > 90 {
		title = jsSlice(title, 87) + "…"
	}
	return Push{
		Kind: KindBroadcast, Channel: ChannelChat,
		Notification: &push.Notification{Title: title, Body: jsPreview(text, 80)},
		Data:         map[string]string{"type": KindBroadcast, "chatId": ""},
	}
}

// SessionRevoked is the silent "session_revoked" push (R50, R84) to the device of one revoked session:
// reason is the revocation reason; sessionId lets the app ignore a push meant for a session it no
// longer holds.
func SessionRevoked(reason, sessionID string) Push {
	return Push{Kind: KindSessionRevoked, Data: map[string]string{"type": KindSessionRevoked, "reason": reason, "sessionId": sessionID}}
}

// jsPreview is JavaScript's text.slice(0, n) + (text.length > n ? "…" : "").
func jsPreview(s string, n int) string {
	if jsLen(s) > n {
		return jsSlice(s, n) + "…"
	}
	return s
}

// jsLen is JavaScript's String.length: UTF-16 code units.
func jsLen(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// jsSlice is JavaScript's s.slice(0, n) in UTF-16 code units. A surrogate pair cut in half by n is
// dropped whole: JavaScript would keep a lone high surrogate, which no UTF-8 payload can carry.
func jsSlice(s string, n int) string {
	units := 0
	for i, r := range s {
		w := utf16.RuneLen(r)
		if units+w > n {
			return s[:i]
		}
		units += w
	}
	return s
}

// jsTrim is String.prototype.trim: ECMAScript white space and line terminators (U+FEFF included,
// which strings.TrimSpace keeps).
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
			return true
		}
		return r >= 0x2000 && r <= 0x200A
	})
}
