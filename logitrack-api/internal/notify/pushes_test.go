package notify

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"
)

// The legacy contract (main spec §7.6, §11.8): data.type strings, data keys, titles, bodies and Android
// channels exactly as fn:triggers.ts:279-391 and fn:chat.ts:33-212 send them, so APK 3.x routes and
// shows them unchanged (main.dart:71-82, main_layout.dart:95-142).
func TestLegacyPushContract(t *testing.T) {
	cases := []struct {
		name            string
		p               Push
		kind, channel   string
		title, body     string
		data            map[string]string
		legacyFormulaJS string // the expression the strings come from
	}{
		{"assigned first mile", TaskAssigned("first_mile", "fsTask1", "fsDrv1", "SPK-GW", "SOCE", "2026-10-10", "08:30"),
			KindTaskAssigned, ChannelTaskAssignments, "New task assigned", "SPK-GW → SOCE (2026-10-10 08:30)",
			map[string]string{"type": "first_mile_task_assigned", "taskId": "fsTask1", "driverId": "fsDrv1"},
			"`${sourceHub ?? \"\"} → ${destination ?? \"\"}${dateStr ? ` (${dateStr} ${timeStr})` : \"\"}`.trim()"},
		{"assigned line haul without time", TaskAssigned("line_haul", "t2", "d2", "SOCE", "SPK890103 - ลาดกระบัง", "2026-10-11", ""),
			KindTaskAssigned, ChannelTaskAssignments, "New task assigned", "SOCE → SPK890103 - ลาดกระบัง (2026-10-11 )",
			map[string]string{"type": "line_haul_task_assigned", "taskId": "t2", "driverId": "d2"}, "time undefined -> \"\""},
		{"assigned without date", TaskAssigned("first_mile", "t3", "d3", " ", "B", "", "09:00"),
			KindTaskAssigned, ChannelTaskAssignments, "New task assigned", "→ B",
			map[string]string{"type": "first_mile_task_assigned", "taskId": "t3", "driverId": "d3"}, "no date part, trimmed"},
		{"unassigned", TaskUnassigned("line_haul", "t4", "d4"),
			KindTaskUnassigned, ChannelTaskAssignments, "Assignment cancelled", "You have been unassigned from this task.",
			map[string]string{"type": "line_haul_task_unassigned", "taskId": "t4", "driverId": "d4"}, ""},
		{"cancelled", TaskCancelled("first_mile", "t5", "d5"),
			KindTaskCancelled, ChannelTaskAssignments, "Task cancelled", "This first mile task has been cancelled.",
			map[string]string{"type": "first_mile_task_cancelled", "taskId": "t5", "driverId": "d5"}, "taskLabel.toLowerCase()"},
		{"cancelled line haul", TaskCancelled("line_haul", "t6", "d6"),
			KindTaskCancelled, ChannelTaskAssignments, "Task cancelled", "This line haul task has been cancelled.",
			map[string]string{"type": "line_haul_task_cancelled", "taskId": "t6", "driverId": "d6"}, ""},
		{"maintenance defaults", MaintenanceScheduled("m1", "d7", "", ""),
			KindMaintenanceScheduled, ChannelTaskAssignments, "นัดเช็คระยะ", "มีงานเช็คระยะ — กรุณาเข้าอู่ตามนัด (ดูรายละเอียดในแอป)",
			map[string]string{"type": "maintenance_scheduled", "maintenanceId": "m1", "driverId": "d7"}, "title || ..., body || ..."},
		{"maintenance staff text", MaintenanceScheduled("m2", "d8", "เข้าอู่พรุ่งนี้", "09:00 อู่ A"),
			KindMaintenanceScheduled, ChannelTaskAssignments, "เข้าอู่พรุ่งนี้", "09:00 อู่ A",
			map[string]string{"type": "maintenance_scheduled", "maintenanceId": "m2", "driverId": "d8"}, ""},
		{"chat", ChatMessage("chat1", "msg1", "รถเสียที่ปั๊ม"),
			KindChat, ChannelChat, "New message from Admin", "รถเสียที่ปั๊ม",
			map[string]string{"type": "chat", "chatId": "chat1", "messageId": "msg1"}, "{ ...data, chatId: data.chatId || \"\" }"},
		{"broadcast", Broadcast("ประกาศ", "หยุดสงกรานต์"),
			KindBroadcast, ChannelChat, "ประกาศ", "หยุดสงกรานต์",
			map[string]string{"type": "broadcast", "chatId": ""}, "sendFcmToUser adds chatId \"\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			if p.Kind != tc.kind || p.Channel != tc.channel || p.Silent() {
				t.Fatalf("kind %s channel %s silent %v", p.Kind, p.Channel, p.Silent())
			}
			if p.Notification.Title != tc.title || p.Notification.Body != tc.body {
				t.Fatalf("title %q body %q, want %q %q (%s)", p.Notification.Title, p.Notification.Body, tc.title, tc.body, tc.legacyFormulaJS)
			}
			if !maps.Equal(p.Data, tc.data) {
				t.Fatalf("data %v, want %v", p.Data, tc.data)
			}
			m := p.Message("tok")
			if m.Android.Priority != "high" || m.Android.Notification.ChannelID != tc.channel || m.APNS.Payload.Aps.Sound != "default" ||
				m.APNS.Headers != nil || m.Token != "tok" {
				t.Fatalf("delivery options %+v %+v", m.Android, m.APNS)
			}
		})
	}
}

// R21 and R50: silent pushes carry no notification block; Android priority normal; APNs
// content-available 1, apns-priority 5, push type background.
func TestSilentPushes(t *testing.T) {
	for _, tc := range []struct {
		p    Push
		want string
	}{
		{TasksChanged(), `{"token":"tok","data":{"type":"tasks_changed"},"android":{"priority":"normal"},` +
			`"apns":{"headers":{"apns-priority":"5","apns-push-type":"background"},"payload":{"aps":{"content-available":1}}}}`},
		{SessionRevoked("admin_revoke", "0199c000-0000-7000-8000-000000000001"),
			`{"token":"tok","data":{"reason":"admin_revoke","sessionId":"0199c000-0000-7000-8000-000000000001","type":"session_revoked"},` +
				`"android":{"priority":"normal"},"apns":{"headers":{"apns-priority":"5","apns-push-type":"background"},"payload":{"aps":{"content-available":1}}}}`},
	} {
		if !tc.p.Silent() {
			t.Fatalf("%s is not silent", tc.p.Kind)
		}
		b, err := json.Marshal(tc.p.Message("tok"))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.p.Kind, b, tc.want)
		}
	}
	if TasksChanged().Kind != KindTasksChanged || SessionRevoked("x", "y").Kind != KindSessionRevoked {
		t.Fatal("kinds")
	}
}

// text.slice(0, 80) + (text.length > 80 ? "…" : "") and the 90 / 87 broadcast title, in UTF-16 units
// as JavaScript counts them (Thai is one unit per character, emoji two).
func TestJavaScriptTruncation(t *testing.T) {
	th80 := strings.Repeat("ก", 80)
	if got := ChatMessage("c", "m", th80).Notification.Body; got != th80 {
		t.Fatalf("80 units must not be cut: %q", got)
	}
	if got := ChatMessage("c", "m", th80+"ข").Notification.Body; got != th80+"…" {
		t.Fatalf("81 units: %q", got)
	}
	emoji := strings.Repeat("a", 79) + "😀" // 81 units: the pair straddles unit 80
	if got := ChatMessage("c", "m", emoji).Notification.Body; got != strings.Repeat("a", 79)+"…" {
		t.Fatalf("surrogate pair cut: %q", got)
	}
	if got := ChatMessage("c", "m", strings.Repeat("a", 78)+"😀").Notification.Body; got != strings.Repeat("a", 78)+"😀" {
		t.Fatalf("80 units with a pair: %q", got)
	}
	t90, t91 := strings.Repeat("ท", 90), strings.Repeat("ท", 91)
	if got := Broadcast(t90, "x").Notification.Title; got != t90 {
		t.Fatalf("90-unit title: %q", got)
	}
	if got := Broadcast(t91, "x").Notification.Title; got != strings.Repeat("ท", 87)+"…" {
		t.Fatalf("91-unit title: %q", got)
	}
	if jsLen("😀ก") != 3 || jsSlice("😀ก", 2) != "😀" || jsSlice("😀ก", 1) != "" || jsTrim("\ufeff a \u3000") != "a" {
		t.Fatal("helpers")
	}
}

// Claim values of idem:fcm round-trip; a value nobody writes is a finished failure (never resent).
func TestParseClaim(t *testing.T) {
	at := time.UnixMilli(1760000000123)
	for v, want := range map[string]claimResult{
		"pending|1760000000123|n1":        {state: claimInFlight, since: at},
		"sent|1760000000123|":             {state: claimDone, outcome: outcome{status: StatusSent, at: at}},
		"token_invalid|1760000000123|e|x": {state: claimDone, outcome: outcome{status: StatusTokenInvalid, err: "e|x", at: at}},
		"deduplicated|1760000000123|":     {state: claimDone, outcome: outcome{status: StatusDeduplicated, at: at}},
		"garbage":                         {state: claimDone, outcome: outcome{status: StatusFailed, err: "unreadable idem:fcm value"}},
		"weird|1760000000123|":            {state: claimDone, outcome: outcome{status: StatusFailed, err: "unreadable idem:fcm value"}},
	} {
		got := parseClaim(v)
		if got.state != want.state || got.outcome != want.outcome || !got.since.Equal(want.since) {
			t.Errorf("%s: %+v, want %+v", v, got, want)
		}
	}
	if TokenID("abc") != "ba7816bf8f01cfea" {
		t.Fatalf("TokenID: %s", TokenID("abc"))
	}
}
