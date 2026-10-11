package etl

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/tenancy"
)

// Chats, their messages and the broadcasts (Appendix A §A.3.10).

// loadChat: chats (§A.3.10): driverId an auth uid; tenant from the driver (quarantine tenant when none); lastReadByAdmin
// {uid: ts} -> chat_read_state rows (an admin uid without a user -> user_unresolved), lastReadByDriver -> the row of the
// driver's linked user. More than one non-closed chat per driver all load and raise duplicate_natural_key until the
// owner merges them (D5); once 0010 has built chats_one_open_per_driver, a further open chat of the driver (a later
// delta or the mirror) is quarantined on its status field: it loads closed, the legacy status kept in the finding.
func loadChat(c *docCtx) error {
	drv, err := c.driverField("driverId")
	if err != nil {
		return err
	}
	tenant, err := c.stamp(tenancy.Chats, map[string]any{"driverId": drv.raw})
	if err != nil {
		return err
	}
	if ok, err := c.requireDriverOutsideQuarantine(drv); !ok {
		return err
	}
	status, ok := c.vocab("status", "open", "in_progress", "closed")
	if !ok {
		return c.reject("status", ReasonStatusOutOfVocab, "neither open, in_progress nor closed", c.get("status"))
	}
	if status == "" {
		status = "open"
	}
	if status != "closed" && drv.ok() {
		var other string
		err := c.tx.QueryRow(c.ctx, `SELECT coalesce(legacy_doc_id, id::text) FROM chats WHERE driver_id = $1 AND status <> 'closed'
			AND legacy_doc_id IS DISTINCT FROM $2 LIMIT 1`, drv.id, c.doc.ID).Scan(&other)
		switch {
		case err == nil:
			enforced, err := c.run.d5Enforced(d5OpenChat)
			if err != nil {
				return err
			}
			if enforced {
				c.find("status", ReasonDuplicateNaturalKey, "the driver already has the open chat "+other+"; loaded closed (0010 chats_one_open_per_driver)", c.get("status"))
				status = "closed"
			} else {
				c.find("status", ReasonDuplicateNaturalKey, "the driver's open chat is also "+other+" (D5: both loaded until the owner merges them)", c.get("status"))
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
	}
	priority, ok := lowerVocab(c.get("priority"), "normal", "urgent")
	if !ok {
		c.find("priority", ReasonStatusOutOfVocab, "neither normal nor urgent; normal kept", c.get("priority"))
		priority = ""
	}
	if priority == "" {
		priority = "normal"
	}
	msgType, ok := lowerVocab(c.get("lastMessageType"), "normal", "broadcast")
	if !ok {
		c.find("lastMessageType", ReasonStatusOutOfVocab, "neither normal nor broadcast", c.get("lastMessageType"))
		msgType = ""
	}
	admin, err := c.user("assignedAdminId", c.str("assignedAdminId"))
	if err != nil {
		return err
	}
	lastBy, err := c.userByUID(c.str("lastMessageBy"))
	if err != nil {
		return err
	}
	r := newRow("chats").set("tenant_id", tenant).set("tenant_source", string(c.tenantSource)).set("driver_id", drv.idPtr()).
		set("legacy_driver_ref", drv.rawPtr()).set("driver_ref_match", drv.match).set("status", status).
		set("assigned_admin_user_id", admin).set("assigned_at", c.ts("assignedAt")).set("closed_at", c.ts("closedAt")).
		set("priority", priority).set("last_message_preview", c.text("lastMessage")).set("last_message_at", c.ts("lastMessageAt")).
		set("last_message_by_user_id", lastBy).set("last_message_type", strPtr(msgType))
	c.times(r)
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM chat_read_state WHERE chat_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	reads := map[uuid.UUID]time.Time{}
	admins, _ := c.get("lastReadByAdmin").(map[string]any)
	uids := make([]string, 0, len(admins))
	for k := range admins {
		uids = append(uids, k)
	}
	slices.Sort(uids)
	for _, uid := range uids {
		at, ok := toTime(admins[uid])
		if !ok {
			c.find("lastReadByAdmin."+uid, ReasonBadTimestamp, "not a timestamp", admins[uid])
			continue
		}
		u, err := c.user("lastReadByAdmin."+uid, uid)
		if err != nil {
			return err
		}
		if u != nil {
			reads[*u] = at
		}
	}
	if at := c.ts("lastReadByDriver"); at != nil && drv.ok() {
		var u *uuid.UUID
		if err := c.tx.QueryRow(c.ctx, `SELECT user_id FROM drivers WHERE id = $1`, drv.id).Scan(&u); err != nil {
			return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
		}
		if u != nil {
			reads[*u] = *at
		}
	}
	for u, at := range reads {
		if _, err := c.tx.Exec(c.ctx, `INSERT INTO chat_read_state (chat_id, user_id, last_read_at) VALUES ($1, $2, $3)`, id, u, at.Truncate(time.Microsecond)); err != nil {
			return fmt.Errorf("etl: %s: read state: %w", c.doc.Path, err)
		}
	}
	c.done("chats", id)
	return nil
}

// loadChatMessage: chats/{id}/messages/{mid} (collection group "messages") -> chat_messages: legacy_doc_id is unique
// per chat; senderId uid -> user (user_unresolved when none); imageUrl -> file (chat_image, owned by the chat, in its
// tenant); a message of a chat that did not load is rejected (the chat is its key).
func loadChatMessage(c *docCtx) error {
	parent := c.doc.ParentPath()
	chatDoc, ok := strings.CutPrefix(parent, "chats/")
	if !ok || chatDoc == "" || strings.Contains(chatDoc, "/") {
		return c.reject("", ReasonMissingRequired, "a message lives under chats/{id}", parent)
	}
	chat, err := c.one(`SELECT id, tenant_id FROM chats WHERE legacy_doc_id = $1`, chatDoc)
	if err != nil {
		return err
	}
	if !chat.ok() {
		return c.reject("", ReasonMissingRequired, "the parent chat did not load; chat_id is NOT NULL", chatDoc)
	}
	role, ok := lowerVocab(c.get("senderRole"), "admin", "driver")
	switch {
	case !ok:
		return c.reject("senderRole", ReasonStatusOutOfVocab, "neither admin nor driver", c.get("senderRole"))
	case role == "":
		return c.reject("senderRole", ReasonMissingRequired, "chat_messages.sender_role is NOT NULL", nil)
	}
	typ, ok := lowerVocab(c.get("type"), "normal", "broadcast")
	if !ok {
		c.find("type", ReasonStatusOutOfVocab, "neither normal nor broadcast; normal kept", c.get("type"))
		typ = ""
	}
	if typ == "" {
		typ = "normal"
	}
	sender, err := c.user("senderId", c.str("senderId"))
	if err != nil {
		return err
	}
	var id uuid.UUID
	err = c.tx.QueryRow(c.ctx, `SELECT id FROM chat_messages WHERE chat_id = $1 AND legacy_doc_id = $2`, chat.id, c.doc.ID).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if id, err = uuid.NewV7(); err != nil {
			return err
		}
	case err != nil:
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	img, err := c.fileRef("imageUrl", c.get("imageUrl"), "chat_image", "chat", chat.id, chat.tenant)
	if err != nil {
		return err
	}
	text := c.text("text")
	if text == nil && img == nil {
		return c.reject("text", ReasonMissingRequired, "a message needs text or an image", nil)
	}
	r := newRow("chat_messages").set("id", id).set("legacy_doc_id", c.doc.ID).set("chat_id", chat.id).set("sender_user_id", sender).
		set("sender_role", role).set("text", text).set("image_file_id", img).set("message_type", typ).setPresent("created_at", c.ts("createdAt"))
	if _, err := upsert(c.ctx, c.tx, r, "(chat_id, legacy_doc_id) WHERE legacy_doc_id IS NOT NULL", "chat_id", "legacy_doc_id", "id"); err != nil {
		return fmt.Errorf("%s: %w", c.doc.Path, err)
	}
	c.done("chat_messages", id)
	return nil
}

// loadBroadcast: broadcasts + broadcast_reads (§A.3.10): readDriverAuthIds[] -> reads (an unresolved reader is
// skipped with user_unresolved; read_at is the document's update time, the latest a legacy read can be), readCount
// dropped (derived), title may be missing, tenant_id NULL; broadcast_recipients stays empty for legacy rows.
func loadBroadcast(c *docCtx) error {
	text := c.text("messageText", "message")
	if text == nil {
		return c.reject("messageText", ReasonMissingRequired, "broadcasts.message_text is NOT NULL", nil)
	}
	by, err := c.user("createdBy", c.str("createdBy"))
	if err != nil {
		return err
	}
	count := c.integer("recipientCount")
	if count != nil && *count < 0 {
		c.find("recipientCount", ReasonBadNumber, "negative count", c.get("recipientCount"))
		count = nil
	}
	sent := c.ts("sentAt")
	if sent == nil {
		sent = c.ts("createdAt")
	}
	if sent == nil {
		t := c.doc.CreateTime
		if t.IsZero() {
			t = c.doc.UpdateTime
		}
		t = t.Truncate(time.Microsecond)
		sent = &t
	}
	r := newRow("broadcasts").set("created_by_user_id", by).set("created_by_name", c.text("createdByName")).set("title", c.text("title")).
		set("message_text", text).setPresent("recipient_group", c.text("recipientGroup")).setPresent("recipient_count", count).
		set("sent_at", sent).set("created_at", sent)
	id, err := upsertLegacy(c, r)
	if err != nil {
		return err
	}
	if _, err := c.tx.Exec(c.ctx, `DELETE FROM broadcast_reads WHERE broadcast_id = $1`, id); err != nil {
		return fmt.Errorf("etl: %s: %w", c.doc.Path, err)
	}
	readers, _ := c.get("readDriverAuthIds").([]any)
	readAt := c.doc.UpdateTime.Truncate(time.Microsecond)
	for i, x := range readers {
		uid, _ := x.(string)
		u, err := c.user(fmt.Sprintf("readDriverAuthIds[%d]", i), uid)
		if err != nil {
			return err
		}
		if u == nil {
			continue
		}
		if _, err := c.tx.Exec(c.ctx, `INSERT INTO broadcast_reads (broadcast_id, user_id, read_at) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			id, *u, readAt); err != nil {
			return fmt.Errorf("etl: %s: read: %w", c.doc.Path, err)
		}
	}
	c.done("broadcasts", id)
	return nil
}
