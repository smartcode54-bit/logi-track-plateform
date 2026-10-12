// T50 (issue #70; Appendix E §E.5 rows 3-4, §E.5.1): the pure updaters the room's optimistic send and the
// realtime map share. A message is kept once (by id, else by the sender's clientMessageId, R63), new ones go to
// the head of page 0, and the room shows every page oldest first.
import { describe, expect, it } from "vitest";

import {
    applyReadState,
    messageFromEvent,
    messagesOldestFirst,
    needsImageRefetch,
    readFromEvent,
    removeLocalMessage,
    upsertMessage,
    type ChatDetailDTO,
    type ChatMessageDTO,
    type MessagesCache,
} from "./chatCache";

function msg(n: number, over: Partial<ChatMessageDTO> = {}): ChatMessageDTO {
    return {
        id: `m${n}`,
        chatId: "c1",
        legacyDocId: null,
        senderUserId: "d-user",
        senderRole: "driver",
        senderName: "Somchai",
        text: `text ${n}`,
        imageFileId: null,
        imageUrl: null,
        messageType: "normal",
        clientMessageId: `cm${n}`,
        createdAt: `2026-10-12T01:00:${String(n).padStart(2, "0")}Z`,
        ...over,
    };
}

function cache(...pages: ChatMessageDTO[][]): MessagesCache {
    return { pages: pages.map((data, i) => ({ data, ...(i < pages.length - 1 ? { nextCursor: `p${i + 1}` } : {}) })), pageParams: pages.map((_, i) => (i === 0 ? undefined : `p${i}`)) };
}

describe("chat cache updaters", () => {
    it("reads the whole message of a message.created frame, and refuses an unreadable one", () => {
        const ev = { entity: "chat_message", id: "m9", chatId: "c1", tenantId: "t", at: "2026-10-12T01:00:09Z", senderUserId: "d-user", senderRole: "driver",
            senderName: "Somchai", text: "hi", imageFileId: null, messageType: "normal", clientMessageId: "cm9", createdAt: "2026-10-12T01:00:09Z", legacyDocId: null };
        expect(messageFromEvent(ev)).toEqual(msg(9, { text: "hi" }));
        for (const bad of [null, {}, { id: "m1" }, { id: "m1", chatId: "c1", createdAt: "x" }, "m1"]) expect(messageFromEvent(bad)).toBeNull();
    });

    it("puts a new message at the head of page 0 and leaves a known one alone", () => {
        const c = cache([msg(3), msg(2)], [msg(1)]);
        const next = upsertMessage(c, msg(4)) as MessagesCache;
        expect(next.pages[0].data.map((m) => m.id)).toEqual(["m4", "m3", "m2"]);
        expect(next.pages[1]).toBe(c.pages[1]);
        expect(next.pageParams).toBe(c.pageParams);
        // A replay of a message the page already holds changes nothing.
        expect(upsertMessage(next, msg(2))).toEqual(next);
        expect(upsertMessage(next, msg(1))).toEqual(next);
    });

    it("the server's copy replaces the local one of the same clientMessageId, in place; a local copy never replaces the server's", () => {
        const local = msg(5, { id: "local:cm5", senderRole: "admin", local: "sending" });
        const withLocal = upsertMessage(cache([msg(4)]), local) as MessagesCache;
        expect(withLocal.pages[0].data.map((m) => m.id)).toEqual(["local:cm5", "m4"]);
        const server = msg(5, { senderRole: "admin" });
        const replaced = upsertMessage(withLocal, server) as MessagesCache;
        expect(replaced.pages[0].data).toEqual([server, msg(4)]);
        // The send's answer after the stream's event (or a late "failed" mark): the server's copy stays.
        expect(upsertMessage(replaced, { ...local, local: "failed" })).toEqual(replaced);
        // A local state change of the local copy (sending -> failed) is written.
        const failed = upsertMessage(withLocal, { ...local, local: "failed" }) as MessagesCache;
        expect(failed.pages[0].data[0].local).toBe("failed");
        expect(removeLocalMessage(failed, "cm5")).toEqual(cache([msg(4)]));
    });

    it("writes nothing without a cache (the room is not open)", () => {
        expect(upsertMessage(undefined, msg(1))).toBeUndefined();
        expect(removeLocalMessage(undefined, "cm1")).toBeUndefined();
        expect(needsImageRefetch(undefined, msg(1, { imageFileId: "f1" }))).toBe(false);
        expect(needsImageRefetch(cache([msg(2)]), msg(1, { imageFileId: "f1" }))).toBe(true);
        expect(needsImageRefetch(cache([msg(2)]), msg(1))).toBe(false);
    });

    it("shows every page oldest first, each message once (pages overlap after a prepend and a refetch)", () => {
        const c = cache([msg(5), msg(4, { createdAt: "2026-10-12T01:00:04.500Z" }), msg(3)], [msg(3), msg(2), msg(1)]);
        expect(messagesOldestFirst(c).map((m) => m.id)).toEqual(["m1", "m2", "m3", "m4", "m5"]);
        // A local copy and the server's of one send: the server's.
        const both = cache([msg(6, { id: "local:cm6", local: "sending" }), msg(6)]);
        expect(messagesOldestFirst(both)).toEqual([msg(6)]);
        expect(messagesOldestFirst(undefined)).toEqual([]);
    });

    it("moves a reader's state forward in the chat detail; the driver's also sets driverLastReadAt", () => {
        const detail = { id: "c1", driverUserId: "d-user", reads: [{ userId: "u-1", lastReadAt: "2026-10-12T01:00:05Z", isDriver: false }], driverLastReadAt: null } as unknown as ChatDetailDTO;
        const r = readFromEvent({ entity: "chat", id: "c1", chatId: "c1", userId: "d-user", lastReadAt: "2026-10-12T01:00:09Z" });
        expect(r).toEqual({ chatId: "c1", userId: "d-user", lastReadAt: "2026-10-12T01:00:09Z" });
        const next = applyReadState(detail, r!) as ChatDetailDTO;
        expect(next.driverLastReadAt).toBe("2026-10-12T01:00:09Z");
        expect(next.reads).toContainEqual({ userId: "d-user", lastReadAt: "2026-10-12T01:00:09Z", isDriver: true });
        // Never backwards; staff reads leave the driver's mark alone.
        const older = applyReadState(next, { userId: "d-user", lastReadAt: "2026-10-12T01:00:01Z" }) as ChatDetailDTO;
        expect(older.driverLastReadAt).toBe("2026-10-12T01:00:09Z");
        const staff = applyReadState(next, { userId: "u-1", lastReadAt: "2026-10-12T01:00:10Z" }) as ChatDetailDTO;
        expect(staff.reads.find((x) => x.userId === "u-1")?.lastReadAt).toBe("2026-10-12T01:00:10Z");
        expect(staff.driverLastReadAt).toBe("2026-10-12T01:00:09Z");
        expect(applyReadState(undefined, r!)).toBeUndefined();
        expect(readFromEvent({ userId: "x" })).toBeNull();
    });
});
