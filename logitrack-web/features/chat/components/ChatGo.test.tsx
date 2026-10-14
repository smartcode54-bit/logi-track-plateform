// T50 (issue #70; Appendix E §E.2.1, §E.5 rows 1-4, §E.10 row 15): the chat console and broadcasts over Go,
// rendered under the app's providers with every request through the BFF (/api/go/v1/...). The web has no Firebase
// SDK since TW7 (AC: no Firestore listener on chats or messages; features/chat/firebaseImports.test.ts walks the
// import graph). Pinned here: the lists are Go's queue and mine (closed chats left out by Go,
// AC note), a room loads 50 messages per page (AC), a new message arrives through SSE chat:{id} in well under
// 5 s (AC), the read state is one POST per newest message seen, and the broadcast picker searches
// GET /v1/drivers?fields=minimal instead of reading every driver (AC).
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { act, screen, waitFor, within } from "@testing-library/react";
import type { QueryClient } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";

const nav = vi.hoisted(() => ({ params: new URLSearchParams(), pathname: "/app/chat", replace: vi.fn(), push: vi.fn() }));
vi.mock("next/navigation", () => {
    // One router object, as Next's is (an effect that depends on it does not restart on every render).
    const router = { replace: (href: string) => nav.replace(href), push: (href: string) => nav.push(href), prefetch: () => undefined, back: () => undefined, refresh: () => undefined };
    return { useRouter: () => router, usePathname: () => nav.pathname, useSearchParams: () => nav.params };
});

import { resetQueryClientForTests } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { RealtimeProvider } from "@/lib/realtime/RealtimeProvider";
import { CLOSED, CONNECTING, OPEN, type EventSourceLike } from "@/lib/realtime/realtimeStream";
import { setRealtimeStatus } from "@/lib/realtime/status";
import { resetRealtimeTopicsForTests } from "@/lib/realtime/topics";
import { fakeWeb, goErr, json, makeMe, type FakeCall } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

import { BROADCAST_PAGE_SIZE, type BroadcastDTO } from "../api/broadcasts.go";
import { CHAT_PAGE_SIZE, chatWithDriverHref, type ChatDTO, type ChatDetailDTO, type ChatMessageDTO } from "../api/chats.go";
import { BroadcastComposerGo } from "./BroadcastComposerGo";
import { ChatLayoutGo } from "./ChatLayoutGo";
import { ChatRoomGo } from "./ChatRoomGo";
import { ChatWithDriverGo, OPEN_ATTEMPTS, OPEN_RETRY_MS } from "./ChatWithDriverGo";
import { ConversationsPanelGo } from "./ConversationsPanelGo";

beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
});
beforeEach(() => {
    window.history.replaceState(null, "", "/app/chat");
    nav.params = new URLSearchParams();
    nav.pathname = "/app/chat";
    nav.replace.mockReset();
    nav.push.mockReset();
    resetRealtimeTopicsForTests();
});
afterEach(() => {
    resetQueryClientForTests();
    setRealtimeStatus("idle");
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

const CAPS = ["chat:view", "chat:send", "broadcasts:view", "broadcasts:send", "drivers:view", "operations:edit_trip_details"];
const CHAT_ID = "0192b3a4-0000-7000-8000-0000000000c1";
const DRIVER_ID = "0192b3a4-0000-7000-8000-0000000000d1";
const ME = "u-1";

const q = (call: FakeCall) => new URL(call.url, "http://web.test").searchParams;
const path = (call: FakeCall) => call.url.split("?")[0];

function chat(over: Partial<ChatDetailDTO> = {}): ChatDetailDTO {
    return {
        id: CHAT_ID,
        tenantId: "t-own",
        legacyDocId: null,
        driverId: DRIVER_ID,
        driverName: "สมชาย ใจดี",
        driverUserId: "u-driver",
        driverLegacyUid: null,
        status: "open",
        priority: "normal",
        assignedAdminUserId: null,
        assignedAdminName: null,
        assignedAt: null,
        closedAt: null,
        lastMessagePreview: "hello",
        lastMessageAt: "2026-10-12T01:00:50Z",
        lastMessageByUserId: "u-driver",
        lastMessageType: "normal",
        createdAt: "2026-10-01T00:00:00Z",
        updatedAt: "2026-10-12T01:00:50Z",
        unreadForMe: 2,
        lastReadAt: "2026-10-12T01:00:40Z",
        reads: [],
        driverLastReadAt: null,
        ...over,
    };
}

function message(n: number, over: Partial<ChatMessageDTO> = {}): ChatMessageDTO {
    return {
        id: `0192b3a4-0000-7000-8000-${String(n).padStart(12, "0")}`,
        chatId: CHAT_ID,
        legacyDocId: null,
        senderUserId: "u-driver",
        senderRole: "driver",
        senderName: "สมชาย ใจดี",
        text: `message ${n}`,
        imageFileId: null,
        imageUrl: null,
        messageType: "normal",
        clientMessageId: null,
        createdAt: new Date(Date.UTC(2026, 9, 12, 1, 0, 0) + n * 1000).toISOString(),
        ...over,
    };
}

function setup(over: { chat?: Partial<ChatDetailDTO>; capabilities?: string[]; steward?: boolean } = {}) {
    const web = fakeWeb();
    let detail = chat(over.chat);
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ id: ME, capabilities: over.capabilities ?? CAPS, steward: over.steward ?? false }) }));
    web.on("GET", "/api/go/v1/chats", (call) => {
        const filter = q(call).get("filter");
        const row = (n: number, extra: Partial<ChatDTO> = {}): ChatDTO => ({ ...chat(), id: `0192b3a4-0000-7000-8000-00000000c${n}0`, driverName: `${filter} driver ${n}`, ...extra });
        if (filter === "queue") return json(200, { data: [row(1), row(2, { unreadForMe: 0, priority: "urgent" })], nextCursor: "q2" });
        return json(200, { data: [row(3, { assignedAdminUserId: ME, status: "in_progress", unreadForMe: 0 })] });
    });
    web.on("GET", `/api/go/v1/chats/${CHAT_ID}`, () => json(200, { data: detail }));
    web.on("GET", `/api/go/v1/chats/${CHAT_ID}/messages`, (call) => {
        const before = q(call).get("before");
        if (before === "older") return json(200, { data: [message(2), message(1)] });
        // Newest first: 50 messages, 52..3, and a cursor to the older ones.
        const data = Array.from({ length: CHAT_PAGE_SIZE }, (_, i) => message(52 - i, i === 0 ? { text: "newest from driver" } : {}));
        return json(200, { data, nextCursor: "older" });
    });
    web.on("POST", `/api/go/v1/chats/${CHAT_ID}/messages`, (call) => {
        const body = call.body as { text: string; clientMessageId: string };
        return json(201, { data: message(60, { senderRole: "admin", senderUserId: ME, senderName: null, text: body.text, clientMessageId: body.clientMessageId }) });
    });
    web.on("POST", `/api/go/v1/chats/${CHAT_ID}/read`, () => new Response(null, { status: 204 }));
    web.on("POST", `/api/go/v1/chats/${CHAT_ID}/assign`, (call) => {
        const body = call.body as { adminUserId?: string | null };
        detail = { ...detail, assignedAdminUserId: body.adminUserId === null ? null : ME, status: body.adminUserId === null ? "open" : "in_progress" };
        return json(200, { data: detail });
    });
    web.on("POST", `/api/go/v1/chats/${CHAT_ID}/close`, () => {
        detail = { ...detail, status: "closed" };
        return json(200, { data: detail });
    });
    web.on("GET", `/api/go/v1/drivers/${DRIVER_ID}`, () =>
        json(200, {
            data: {
                id: DRIVER_ID,
                userId: "u-driver",
                legacyDocId: null,
                displayName: "สมชาย ใจดี",
                firstName: "Somchai",
                lastName: "Jaidee",
                fullNameTh: "สมชาย ใจดี",
                mobile: "0812345678",
                profileUrl: null,
                status: "active",
                login: { userId: "u-driver" },
                currentAssignment: { id: "as1", truckId: "tr1", truckPlate: "70-1234", truckModel: "Isuzu" },
                activeTruck: null,
            },
        })
    );
    web.on("GET", "/api/go/v1/drivers", (call) => {
        const term = q(call).get("q") ?? "";
        return json(200, { data: [{ id: DRIVER_ID, displayName: `สมชาย ${term}` }, { id: "0192b3a4-0000-7000-8000-0000000000d2", displayName: "No login" }] });
    });
    web.on("GET", "/api/go/v1/drivers/0192b3a4-0000-7000-8000-0000000000d2", () =>
        json(200, { data: { id: "0192b3a4-0000-7000-8000-0000000000d2", userId: null, login: null, displayName: "No login", firstName: "", lastName: "", fullNameTh: null, mobile: "", profileUrl: null, status: "active", currentAssignment: null, activeTruck: null, legacyDocId: null } })
    );
    return { web };
}

class FakeEventSource implements EventSourceLike {
    readyState = CONNECTING;
    onopen: ((this: EventSource, ev: Event) => unknown) | null = null;
    onerror: ((this: EventSource, ev: Event) => unknown) | null = null;
    closed = false;
    private readonly listeners = new Map<string, ((ev: MessageEvent) => void)[]>();
    constructor(readonly url: string) {}
    addEventListener(type: string, listener: (ev: MessageEvent) => void): void {
        this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
    }
    close(): void {
        this.closed = true;
        this.readyState = CLOSED;
    }
    open(): void {
        this.readyState = OPEN;
        this.onopen?.call(this as unknown as EventSource, new Event("open"));
    }
    emit(type: string, topic: string, data: unknown, id: string): void {
        const ev = { data: JSON.stringify({ type, topic, eventId: `e${id}`, data }), lastEventId: id } as MessageEvent;
        for (const l of this.listeners.get(type) ?? []) l(ev);
    }
}

describe("conversations panel over Go", () => {
    it("lists Go's queue and mine (closed chats left out by Go), keyset pages of 50, with Go's unread counts", async () => {
        const { web } = setup();
        renderWithProviders(<ConversationsPanelGo />);
        const queue = await screen.findByTestId("chat-list-queue");
        expect(within(queue).getByText("queue driver 1")).toBeInTheDocument();
        expect(within(queue).getByText("Urgent")).toBeInTheDocument();
        expect(within(queue).getByLabelText("2 unread")).toHaveTextContent("2");
        const lists = web.calls.filter((c) => path(c) === "/api/go/v1/chats");
        expect(lists.map((c) => q(c).get("filter")).sort()).toEqual(["mine", "queue"]);
        for (const c of lists) expect(q(c).get("limit")).toBe(String(CHAT_PAGE_SIZE));
        // The tab counters read the same cache entries (no third request); "+" while more pages exist.
        expect(screen.getByRole("tab", { name: /Queued/ })).toHaveTextContent("2+");
        expect(screen.getByRole("tab", { name: /My Chats/ })).toHaveTextContent("1");
        // The next page of the queue by its cursor.
        await userEvent.setup().click(screen.getByRole("button", { name: "Load more" }));
        await waitFor(() => expect(web.calls.some((c) => path(c) === "/api/go/v1/chats" && q(c).get("cursor") === "q2")).toBe(true));
    });

    it("starts a chat from a server search of drivers (fields=minimal), never a list of every driver", async () => {
        const { web } = setup();
        const user = userEvent.setup();
        renderWithProviders(<ConversationsPanelGo />);
        // The shared searchable select (UI2): a combobox, options with listbox roles, keyboard picking.
        await user.click(await screen.findByRole("combobox", { name: "Search a driver by name..." }));
        await user.type(screen.getByPlaceholderText("Search a driver by name..."), "som");
        await screen.findByRole("option", { name: /สมชาย som/ });
        await user.keyboard("{Enter}");
        expect(nav.push).toHaveBeenCalledWith(`/app/chat/with-driver?driverId=${DRIVER_ID}`);
        const searches = web.calls.filter((c) => path(c) === "/api/go/v1/drivers");
        expect(searches.length).toBeGreaterThan(0);
        // Opening the list reads one page (limit 20), typing searches with q: never every driver.
        for (const c of searches) {
            expect(q(c).get("fields")).toBe("minimal");
            expect(q(c).get("limit")).toBe("20");
        }
        expect(searches.map((c) => q(c).get("q"))).toContain("som");
    });
});

describe("chat room over Go", () => {
    it("loads 50 messages per page, older ones on demand (AC), oldest first, with the unread divider", async () => {
        const { web } = setup();
        nav.params = new URLSearchParams({ chatId: CHAT_ID });
        const user = userEvent.setup();
        renderWithProviders(<ChatRoomGo />);
        expect(await screen.findByText("newest from driver")).toBeInTheDocument();
        const first = web.calls.find((c) => path(c) === `/api/go/v1/chats/${CHAT_ID}/messages`)!;
        expect(q(first).get("limit")).toBe(String(CHAT_PAGE_SIZE));
        expect(q(first).get("before")).toBeNull();
        expect(screen.getAllByTestId("chat-message")).toHaveLength(CHAT_PAGE_SIZE);
        // The divider sits above the first driver message after the caller's read state (01:00:40 -> message 41).
        expect(screen.getByRole("separator", { name: "New messages" })).toBeInTheDocument();

        await user.click(screen.getByRole("button", { name: "Older messages" }));
        expect(await screen.findByText("message 1")).toBeInTheDocument();
        const older = web.calls.filter((c) => path(c) === `/api/go/v1/chats/${CHAT_ID}/messages`).at(-1)!;
        expect(q(older).get("before")).toBe("older");
        expect(q(older).get("limit")).toBe(String(CHAT_PAGE_SIZE));
        const texts = screen.getAllByTestId("chat-message").map((el) => el.textContent ?? "");
        expect(texts[0]).toContain("message 1");
        expect(texts.at(-1)).toContain("newest from driver");
        expect(screen.queryByRole("button", { name: "Older messages" })).toBeNull();
    });

    it("marks the newest message read with one POST (debounced, never inside a data callback)", async () => {
        const { web } = setup();
        nav.params = new URLSearchParams({ chatId: CHAT_ID });
        renderWithProviders(<ChatRoomGo />);
        await screen.findByText("newest from driver");
        await waitFor(() => expect(web.count("POST", `/api/go/v1/chats/${CHAT_ID}/read`)).toBe(1), { timeout: 3000 });
        const read = web.calls.find((c) => c.method === "POST" && path(c) === `/api/go/v1/chats/${CHAT_ID}/read`)!;
        expect(read.body).toEqual({ lastReadAt: message(52).createdAt });
        await new Promise((r) => setTimeout(r, 1000));
        expect(web.count("POST", `/api/go/v1/chats/${CHAT_ID}/read`)).toBe(1);
    });

    it("sends with a clientMessageId and shows the message at once; take, then close, are Go actions", async () => {
        const { web } = setup();
        nav.params = new URLSearchParams({ chatId: CHAT_ID });
        const user = userEvent.setup();
        renderWithProviders(<ChatRoomGo />);
        await screen.findByText("newest from driver");
        await user.type(screen.getByLabelText("Type a message..."), "on my way{Enter}");
        expect(await screen.findByText("on my way")).toBeInTheDocument();
        const sent = web.calls.find((c) => c.method === "POST" && path(c) === `/api/go/v1/chats/${CHAT_ID}/messages`)!;
        const body = sent.body as { text: string; clientMessageId: string };
        expect(body.text).toBe("on my way");
        expect(body.clientMessageId).toMatch(/^[0-9a-f-]{36}$/);
        await waitFor(() => expect(screen.queryByText(/Sending/)).toBeNull());
        expect(screen.getAllByText("on my way")).toHaveLength(1);

        await user.click(screen.getByRole("button", { name: /Take chat/ }));
        await waitFor(() => expect(web.count("POST", `/api/go/v1/chats/${CHAT_ID}/assign`)).toBe(1));
        expect(web.calls.find((c) => path(c) === `/api/go/v1/chats/${CHAT_ID}/assign`)!.body).toEqual({});
        await user.click(await screen.findByRole("button", { name: /Close Case/ }));
        await waitFor(() => expect(nav.push).toHaveBeenCalledWith("/app/chat"));
    });

    it("a failed send stays with Retry, which reuses its clientMessageId (R63)", async () => {
        const { web } = setup();
        let fail = true;
        web.handlers.unshift((call) =>
            call.method === "POST" && path(call) === `/api/go/v1/chats/${CHAT_ID}/messages` && fail ? goErr(503, "unavailable") : undefined
        );
        nav.params = new URLSearchParams({ chatId: CHAT_ID });
        const user = userEvent.setup();
        renderWithProviders(<ChatRoomGo />);
        await screen.findByText("newest from driver");
        await user.type(screen.getByLabelText("Type a message..."), "retry me{Enter}");
        expect(await screen.findByTestId("chat-send-failed")).toBeInTheDocument();
        fail = false;
        await user.click(screen.getByRole("button", { name: "Retry" }));
        await waitFor(() => expect(screen.queryByTestId("chat-send-failed")).toBeNull());
        const sends = web.calls.filter((c) => c.method === "POST" && path(c) === `/api/go/v1/chats/${CHAT_ID}/messages`);
        expect(sends).toHaveLength(2);
        expect((sends[1].body as { clientMessageId: string }).clientMessageId).toBe((sends[0].body as { clientMessageId: string }).clientMessageId);
    });

    it("a new driver message arrives through SSE chat:{id} without a reload (AC: within 5 s)", async () => {
        setup();
        nav.params = new URLSearchParams({ chatId: CHAT_ID });
        const sources: FakeEventSource[] = [];
        const createEventSource = (url: string) => {
            const s = new FakeEventSource(url);
            sources.push(s);
            return s;
        };
        renderWithProviders(
            <RealtimeProvider createEventSource={createEventSource}>
                <ChatRoomGo />
            </RealtimeProvider>
        );
        await screen.findByText("newest from driver");
        // The room joins chat:{id} once Go showed it the chat: the stream carries the topic.
        await waitFor(() => expect(sources.some((s) => s.url.includes(`topics=${encodeURIComponent(`chat:${CHAT_ID}`)}`))).toBe(true));
        const stream = sources.at(-1)!;
        act(() => stream.open());
        const startedAt = Date.now();
        act(() =>
            stream.emit("message.created", `chat:${CHAT_ID}`, { ...message(53, { text: "pushed by SSE" }), entity: "chat_message", tenantId: "t-own", at: message(53).createdAt }, "100")
        );
        expect(await screen.findByText("pushed by SSE", undefined, { timeout: 5000 })).toBeInTheDocument();
        expect(Date.now() - startedAt).toBeLessThan(5000);
    });

    it("an id of the previous chat system is not sent to Go", async () => {
        const { web } = setup();
        nav.params = new URLSearchParams({ chatId: "AbCdFirestoreDocId01" });
        renderWithProviders(<ChatRoomGo />);
        expect(await screen.findByTestId("chat-legacy-link")).toBeInTheDocument();
        expect(web.calls.some((c) => c.url.includes("/v1/chats/"))).toBe(false);
    });
});

describe("chat layout over Go", () => {
    it("the driver panel reads the room's chat (one request with the room) and the driver from Go", async () => {
        const { web } = setup();
        nav.pathname = "/app/chat/room";
        nav.params = new URLSearchParams({ chatId: CHAT_ID });
        renderWithProviders(
            <ChatLayoutGo>
                <ChatRoomGo />
            </ChatLayoutGo>
        );
        const panel = await screen.findByTestId("chat-driver-panel");
        expect(within(panel).getByText("70-1234")).toBeInTheDocument();
        expect(within(panel).getByRole("link", { name: /View Full Fleet Log/ })).toHaveAttribute("href", `/app/drivers/view?id=${DRIVER_ID}`);
        await screen.findByText("newest from driver");
        expect(web.count("GET", `/api/go/v1/chats/${CHAT_ID}`)).toBe(1);
    });
});

describe("open a chat with a driver over Go", () => {
    it("one POST /v1/chats {driverId}, then the room", async () => {
        const { web } = setup();
        web.on("POST", "/api/go/v1/chats", () => json(200, { data: chat() }));
        nav.params = new URLSearchParams({ driverId: DRIVER_ID });
        renderWithProviders(<ChatWithDriverGo />);
        await waitFor(() => expect(nav.replace).toHaveBeenCalledWith(`/app/chat/room?chatId=${CHAT_ID}`));
        expect(web.calls.filter((c) => c.method === "POST" && path(c) === "/api/go/v1/chats").map((c) => c.body)).toContainEqual({ driverId: DRIVER_ID });
    });

    it("asks again while Go answers 202 for a new written-back chat (its minted uuidv7 is no row yet); the room opens on Go's chat", async () => {
        const { web } = setup();
        const MINTED = "0199c0de-1234-7abc-8def-0123456789ab";
        let calls = 0;
        let postsWhenOpened = 0;
        nav.replace.mockImplementation(() => {
            postsWhenOpened = calls;
        });
        web.on("POST", "/api/go/v1/chats", () => {
            calls++;
            // Go's wbOpen: a new Firestore document named by a uuidv7 the mirror will give its row, 202 until then.
            return calls === 1
                ? json(202, { data: { pending: true, id: MINTED, collection: "chats", docId: MINTED, updateTime: "x", document: {} } })
                : json(200, { data: chat({ id: MINTED }) });
        });
        nav.params = new URLSearchParams({ driverId: DRIVER_ID });
        renderWithProviders(<ChatWithDriverGo />);
        await waitFor(() => expect(nav.replace).toHaveBeenCalledWith(`/app/chat/room?chatId=${MINTED}`), { timeout: 4000 });
        expect(nav.replace).toHaveBeenCalledTimes(1);
        expect(postsWhenOpened).toBe(2); // never on the pending answer: GET /v1/chats/{id} would 404 and bounce
    });

    it("gives up with a notice after OPEN_ATTEMPTS pending answers, opening no room", async () => {
        const { web } = setup();
        vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
        web.on("POST", "/api/go/v1/chats", () =>
            json(202, { data: { pending: true, id: "0199c0de-1234-7abc-8def-0123456789ab", collection: "chats", docId: "0199c0de-1234-7abc-8def-0123456789ab", updateTime: "x", document: {} } })
        );
        nav.params = new URLSearchParams({ driverId: DRIVER_ID });
        renderWithProviders(<ChatWithDriverGo />);
        for (let i = 0; i < OPEN_ATTEMPTS; i++) await act(() => vi.advanceTimersByTimeAsync(OPEN_RETRY_MS));
        expect(screen.getByTestId("chat-open-error")).toHaveTextContent(/still being created/i);
        expect(web.count("POST", "/api/go/v1/chats")).toBe(OPEN_ATTEMPTS);
        expect(nav.replace).not.toHaveBeenCalled();
    });

    it("asks again while an existing written-back chat (a Firestore auto id: the nil id) has no PostgreSQL row yet (202)", async () => {
        const { web } = setup();
        let calls = 0;
        web.on("POST", "/api/go/v1/chats", () => {
            calls++;
            return calls === 1
                ? json(202, { data: { pending: true, id: "00000000-0000-0000-0000-000000000000", collection: "chats", docId: "fs1", updateTime: "x", document: {} } })
                : json(200, { data: chat() });
        });
        nav.params = new URLSearchParams({ driverId: DRIVER_ID });
        renderWithProviders(<ChatWithDriverGo />);
        await waitFor(() => expect(nav.replace).toHaveBeenCalledWith(`/app/chat/room?chatId=${CHAT_ID}`), { timeout: 4000 });
        expect(calls).toBeGreaterThanOrEqual(2);
    });

    it("the drivers list's Chat link carries the Go id (a driver with a login) and the legacy uid, each source reading its own", () => {
        expect(chatWithDriverHref({ id: DRIVER_ID, userId: "u-driver", legacyAuthUid: "fb-uid" })).toBe(`/app/chat/with-driver?driverId=${DRIVER_ID}&authId=fb-uid`);
        expect(chatWithDriverHref({ id: DRIVER_ID, userId: "u-driver", legacyAuthUid: null })).toBe(`/app/chat/with-driver?driverId=${DRIVER_ID}`);
        // A Firestore row (document id, no Go login): the legacy uid only.
        expect(chatWithDriverHref({ id: "fsDocId", legacyAuthUid: "fb-uid" })).toBe("/app/chat/with-driver?authId=fb-uid");
        expect(chatWithDriverHref({ id: DRIVER_ID, userId: null, legacyAuthUid: "fb-uid" })).toBe("/app/chat/with-driver?authId=fb-uid");
    });

    it("a link that names only the legacy uid says so and sends nothing", async () => {
        const { web } = setup();
        nav.params = new URLSearchParams({ authId: "fb-driver-uid" });
        renderWithProviders(<ChatWithDriverGo />);
        expect(await screen.findByTestId("chat-open-error")).toBeInTheDocument();
        expect(web.calls.some((c) => c.method === "POST")).toBe(false);
    });
});

describe("broadcasts over Go", () => {
    function broadcast(n: number, over: Partial<BroadcastDTO> = {}): BroadcastDTO {
        return {
            id: `b${n}`,
            tenantId: "t-own",
            legacyDocId: null,
            createdByUserId: ME,
            createdByName: "Admin",
            title: `Notice ${n}`,
            messageText: `Body ${n}`,
            recipientGroup: "all_driver",
            recipientCount: 12,
            readCount: 3,
            sentAt: "2026-10-11T03:00:00Z",
            voidedAt: null,
            ...over,
        };
    }

    function setupBroadcasts(over: { selectedUnsupported?: boolean; steward?: boolean } = {}) {
        const env = setup({ steward: over.steward });
        const rows = new Map([["b1", broadcast(1)], ["b2", broadcast(2, { recipientGroup: "own_fleet" })]]);
        env.web.on("GET", "/api/go/v1/broadcasts", (call) =>
            q(call).get("cursor") === "next" ? json(200, { data: [broadcast(3)] }) : json(200, { data: [...rows.values()], nextCursor: "next" })
        );
        env.web.on("POST", "/api/go/v1/broadcasts", (call) => {
            const body = call.body as { recipientUserIds?: string[] };
            if (body.recipientUserIds && over.selectedUnsupported) {
                return goErr(422, "invalid_argument", { fields: [{ field: "recipientUserIds", reason: "unsupported_until_p7b", params: {} }] });
            }
            const b = broadcast(9, { recipientCount: body.recipientUserIds ? body.recipientUserIds.length : 40 });
            rows.set("b9", b);
            return json(201, { data: b });
        });
        env.web.on("DELETE", /^\/api\/go\/v1\/broadcasts\/[^/]+$/, (call) => {
            rows.delete(path(call).split("/").pop()!);
            return new Response(null, { status: 204 });
        });
        return env;
    }

    it("never reads every driver: a group is resolved by Go when the broadcast is sent", async () => {
        const { web } = setupBroadcasts();
        const user = userEvent.setup();
        renderWithProviders(<BroadcastComposerGo />);
        expect(await screen.findByText("Notice 1")).toBeInTheDocument();
        await user.click(screen.getByRole("radio", { name: /Own Fleet/ }));
        await user.type(screen.getByLabelText(/Subject/), "Route change");
        await user.type(screen.getByLabelText(/Message body/), "Use gate 2");
        await user.click(screen.getByRole("button", { name: /Send Broadcast Now/ }));
        expect(await screen.findByText("Broadcast sent to 40 drivers.", { selector: "p" })).toBeInTheDocument();
        expect(web.calls.find((c) => c.method === "POST" && path(c) === "/api/go/v1/broadcasts")!.body).toEqual({
            recipientGroup: "own_fleet",
            title: "Route change",
            messageText: "Use gate 2",
        });
        expect(web.calls.some((c) => path(c) === "/api/go/v1/drivers")).toBe(false);
        // Only stewards may address every tenant (R60).
        expect(screen.queryByRole("radio", { name: /All tenants/ })).toBeNull();
    });

    it("the driver picker searches GET /v1/drivers?fields=minimal and sends the picked drivers' logins (AC)", async () => {
        const { web } = setupBroadcasts();
        const user = userEvent.setup();
        renderWithProviders(<BroadcastComposerGo />);
        await screen.findByText("Notice 1");
        await user.click(screen.getByRole("radio", { name: /Selected drivers/ }));
        // The shared multi select (UI2): the picked drivers are its chips.
        await user.click(screen.getByRole("combobox", { name: "Search a driver to add..." }));
        const box = screen.getByPlaceholderText("Search a driver to add...");
        await user.type(box, "sc");
        await user.click(await screen.findByRole("option", { name: /สมชาย sc/ }));
        expect(await within(screen.getByTestId("broadcast-picked")).findByText("สมชาย sc")).toBeInTheDocument();
        // A driver without an app login cannot be picked.
        await user.clear(box);
        await user.type(box, "no");
        await user.click(await screen.findByRole("option", { name: /No login/ }));
        await waitFor(() => expect(web.count("GET", "/api/go/v1/drivers/0192b3a4-0000-7000-8000-0000000000d2")).toBe(1));
        await waitFor(() => expect(within(screen.getByTestId("broadcast-picked")).queryByText("No login")).toBeNull());
        await user.keyboard("{Escape}");
        expect(within(screen.getByTestId("broadcast-picked")).getByText("สมชาย sc")).toBeInTheDocument();

        const searches = web.calls.filter((c) => path(c) === "/api/go/v1/drivers");
        expect(searches.length).toBeGreaterThan(0);
        for (const c of searches) expect(q(c).get("fields")).toBe("minimal");

        await user.type(screen.getByLabelText(/Subject/), "Hi");
        await user.type(screen.getByLabelText(/Message body/), "Check in");
        await user.click(screen.getByRole("button", { name: /Send Broadcast Now/ }));
        await waitFor(() => expect(web.count("POST", "/api/go/v1/broadcasts")).toBe(1));
        expect(web.calls.find((c) => c.method === "POST" && path(c) === "/api/go/v1/broadcasts")!.body).toEqual({
            recipientUserIds: ["u-driver"],
            title: "Hi",
            messageText: "Check in",
        });
    });

    it("says why Go refused a selected send before P7b", async () => {
        setupBroadcasts({ selectedUnsupported: true });
        const user = userEvent.setup();
        renderWithProviders(<BroadcastComposerGo />);
        await screen.findByText("Notice 1");
        await user.click(screen.getByRole("radio", { name: /Selected drivers/ }));
        await user.click(screen.getByRole("combobox", { name: "Search a driver to add..." }));
        await user.type(screen.getByPlaceholderText("Search a driver to add..."), "s");
        await user.click(await screen.findByRole("option", { name: /สมชาย s/ }));
        await within(screen.getByTestId("broadcast-picked")).findByText("สมชาย s");
        await user.keyboard("{Escape}");
        await user.type(screen.getByLabelText(/Subject/), "Hi");
        await user.type(screen.getByLabelText(/Message body/), "Body");
        await user.click(screen.getByRole("button", { name: /Send Broadcast Now/ }));
        expect(await screen.findByRole("alert")).toHaveTextContent(/new driver app/);
    });

    it("the history pages by cursor (no cap of 50) and a withdrawal is a DELETE", async () => {
        const { web } = setupBroadcasts({ steward: true });
        const user = userEvent.setup();
        renderWithProviders(<BroadcastComposerGo />);
        await screen.findByText("Notice 1");
        expect(screen.getByRole("radio", { name: /All tenants/ })).toBeInTheDocument();
        const first = web.calls.find((c) => path(c) === "/api/go/v1/broadcasts" && c.method === "GET")!;
        expect(q(first).get("limit")).toBe(String(BROADCAST_PAGE_SIZE));
        await user.click(screen.getByRole("button", { name: "Load more" }));
        expect(await screen.findByText("Notice 3")).toBeInTheDocument();

        const row = screen.getByText("Notice 2").closest("tr")!;
        await user.click(within(row).getByRole("button", { name: /Withdraw/ }));
        await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Withdraw" }));
        await waitFor(() => expect(web.count("DELETE", "/api/go/v1/broadcasts/b2")).toBe(1));
        await waitFor(() => expect(screen.queryByText("Notice 2")).toBeNull());
    });
});

describe("chat room over Go: refetches and sends in flight (T50 review)", () => {
    const MSGS = `/api/go/v1/chats/${CHAT_ID}/messages`;
    const messagesIdle = (client: QueryClient) =>
        waitFor(() => expect(client.getQueryState(queryKeys.chat.messages(CHAT_ID))?.fetchStatus).toBe("idle"));

    it("a failed send keeps its text, Retry and Discard when the messages refetch (the fallback poll, a resync)", async () => {
        const { web } = setup();
        let fail = true;
        web.handlers.unshift((call) => (call.method === "POST" && path(call) === MSGS && fail ? goErr(503, "unavailable") : undefined));
        nav.params = new URLSearchParams({ chatId: CHAT_ID });
        const user = userEvent.setup();
        const { client } = renderWithProviders(<ChatRoomGo />);
        await screen.findByText("newest from driver");
        await user.type(screen.getByLabelText("Type a message..."), "keep me{Enter}");
        expect(await screen.findByTestId("chat-send-failed")).toBeInTheDocument();
        const before = web.count("GET", MSGS);
        await act(() => client.invalidateQueries({ queryKey: queryKeys.chat.messages(CHAT_ID) }));
        await messagesIdle(client);
        expect(web.count("GET", MSGS)).toBeGreaterThan(before);
        expect(screen.getByTestId("chat-send-failed")).toBeInTheDocument();
        expect(screen.getByText("keep me")).toBeInTheDocument();
        fail = false;
        await user.click(screen.getByRole("button", { name: "Retry" }));
        await waitFor(() => expect(screen.queryByTestId("chat-send-failed")).toBeNull());
        const sends = web.calls.filter((c) => c.method === "POST" && path(c) === MSGS).map((c) => (c.body as { clientMessageId: string }).clientMessageId);
        expect(sends).toHaveLength(2);
        expect(sends[1]).toBe(sends[0]);
        expect(screen.getAllByText("keep me")).toHaveLength(1);
    });

    it("a written-back send (202) stays syncing through the refetch 3 s later while the mirror has not caught up", async () => {
        const { web } = setup();
        web.handlers.unshift((call) =>
            call.method === "POST" && path(call) === MSGS
                ? json(202, { data: { pending: true, id: "00000000-0000-0000-0000-000000000000", collection: `chats/fs1/messages`, docId: "fsm1", updateTime: "x", document: {} } })
                : undefined
        );
        nav.params = new URLSearchParams({ chatId: CHAT_ID });
        const user = userEvent.setup();
        const { client } = renderWithProviders(<ChatRoomGo />);
        await screen.findByText("newest from driver");
        const before = web.count("GET", MSGS);
        await user.type(screen.getByLabelText("Type a message..."), "via the old app{Enter}");
        expect(await screen.findByText(/Syncing with the driver app/)).toBeInTheDocument();
        await waitFor(() => expect(web.count("GET", MSGS)).toBeGreaterThan(before), { timeout: 5000 });
        await messagesIdle(client);
        expect(screen.getByText("via the old app")).toBeInTheDocument();
        expect(screen.getByText(/Syncing with the driver app/)).toBeInTheDocument();
    });

    it("a send while the first page is loading cancels nothing: the page and the message both show", async () => {
        const { web } = setup();
        let release!: () => void;
        const held = new Promise<void>((r) => (release = r));
        let first = true;
        web.handlers.unshift(async (call) => {
            if (call.method === "GET" && path(call) === MSGS && first) {
                first = false;
                await held; // a slow link: then the default page
            }
            return undefined;
        });
        nav.params = new URLSearchParams({ chatId: CHAT_ID });
        const user = userEvent.setup();
        renderWithProviders(<ChatRoomGo />);
        await user.type(await screen.findByLabelText("Type a message..."), "early bird{Enter}");
        await waitFor(() => expect(web.count("POST", MSGS)).toBe(1));
        release();
        expect(await screen.findByText("newest from driver")).toBeInTheDocument();
        expect(await screen.findByText("early bird")).toBeInTheDocument();
        await waitFor(() => expect(screen.queryByText(/Sending/)).toBeNull());
        expect(web.count("GET", MSGS)).toBe(1);
    });

    it("a send during the image refetch a photo caused cancels nothing: the photo gets its link and the message shows", async () => {
        const { web } = setup();
        const photo = message(53, { text: null, imageFileId: "f53" });
        let committed = false;
        let release!: () => void;
        const held = new Promise<void>((r) => (release = r));
        web.handlers.unshift(async (call) => {
            if (call.method !== "GET" || path(call) !== MSGS || !committed || q(call).get("before")) return undefined;
            await held; // the image refetch is slow
            const data = [{ ...photo, imageUrl: "https://files.test/f53" }, ...Array.from({ length: CHAT_PAGE_SIZE - 1 }, (_, i) => message(52 - i))];
            return json(200, { data, nextCursor: "older" });
        });
        nav.params = new URLSearchParams({ chatId: CHAT_ID });
        const sources: FakeEventSource[] = [];
        const user = userEvent.setup();
        renderWithProviders(
            <RealtimeProvider
                createEventSource={(url) => {
                    const s = new FakeEventSource(url);
                    sources.push(s);
                    return s;
                }}
            >
                <ChatRoomGo />
            </RealtimeProvider>
        );
        await screen.findByText("newest from driver");
        await waitFor(() => expect(sources.some((s) => s.url.includes("topics="))).toBe(true));
        const stream = sources.at(-1)!;
        act(() => stream.open());
        const before = web.count("GET", MSGS);
        committed = true;
        act(() => stream.emit("message.created", `chat:${CHAT_ID}`, { ...photo, entity: "chat_message", tenantId: "t-own", at: photo.createdAt }, "100"));
        // The event carries no signed URL: the key refetches once its 500 ms window closes.
        await waitFor(() => expect(web.count("GET", MSGS)).toBe(before + 1), { timeout: 3000 });
        await user.type(screen.getByLabelText("Type a message..."), "got it{Enter}");
        await waitFor(() => expect(web.count("POST", MSGS)).toBe(1));
        release();
        expect(await screen.findByRole("link", { name: "View image" })).toHaveAttribute("href", "https://files.test/f53");
        expect(await screen.findByText("got it")).toBeInTheDocument();
    });
});
