/**
 * The tab's one realtime stream (developer-spec.md §8, §10.8; Appendix B §B.4.1; Appendix E §E.5.1):
 * `EventSource('/api/go/v1/events')`, same origin, so the HttpOnly `lt_at` cookie travels with it and
 * the BFF turns it into the bearer (no SSE ticket on the web, R42). Implicit topics come from the
 * token (`user:{uid}` carries `session.revoked`, R50).
 *
 * Wire format (internal/sse): every event is `id: <seq>`, `event: <name>`, `data:
 * {"type","topic","eventId","data"}`; the control events `reconnect` and `resync` carry `{reason}`.
 *
 * - A network drop the browser can retry itself (`error` while `CONNECTING`): nothing is done. The
 *   browser reconnects after the server's `retry:` (3 s) and sends its own `Last-Event-ID`, which also
 *   counts the id-only frame Go writes at connect, so Go replays what the tab missed (R52).
 * - `reconnect` (drain, or the access token's `exp`): an unforced shared refresh, then reopen after a
 *   jittered 1-3 s with `?lastEventId=`, which the BFF turns into `Last-Event-ID` (§10.3).
 * - `resync` (the id is older than the replay window): the owner refetches its active queries.
 * - `error` with `readyState === CLOSED` (EventSource hides the status: a 401 of an expired token,
 *   a 429 past `SSE_MAX_CONN_PER_USER`, the api or the BFF down): refresh, then reopen with a backoff
 *   that doubles from 1-3 s up to 60 s; a refused refresh means the session is gone. `resume()` (the
 *   browser is back online, the tab is visible again) cuts the wait short.
 * - A reopen after an error or `reconnect` resumes from the last id the tab saw. Every dispatched
 *   event carries the stream's current id (`MessageEvent.lastEventId`, the id-only frame at connect
 *   included), but an error carries none: a stream that closed before its first event has no
 *   position, so the reopened stream cannot replay the gap and the owner is told to resync once it
 *   is open (`onResync`).
 * - Every other named event goes to `onEvent`; the names to listen to are registered with `listen`
 *   (EventSource delivers a named event only to listeners of that name).
 * - Explicit topics (`setTopics`, `?topics=`: the open chat room's `chat:{id}`, T50) recreate the stream
 *   only when a wanted topic is not on the connection, resuming from the last id seen, so the replay spans
 *   the new topic too (one global sequence, R52); a stream recreated without a position (no named event
 *   seen yet) asks the owner to resync once it is open, as a reopen after an error does, since Go cannot
 *   replay the gap. A topic no longer wanted stays on the connection (its events find no cache, or keep a
 *   closed room's cache current) until the stream reopens for another reason, so leaving a room, or coming
 *   back to one, costs no new connection and no new `SSE_MAX_CONN_PER_USER` lease; at most
 *   `MAX_EXPLICIT_TOPICS` ride along, the least recently wanted dropped first. Go authorises them at
 *   connect and refuses the whole stream when one is not allowed (403, which EventSource hides): after
 *   `TOPIC_FAILURES_BEFORE_DROP` consecutive failed opens with explicit topics the stream reopens without
 *   them, so one unreadable chat never costs the tab its implicit topics; `hasTopic` says which explicit
 *   topics the open connection carries (the event map falls back to the tenant topic's thin events for the
 *   others). A stream without them that fails too was an outage, not a refusal: the next attempt carries
 *   them again. A new set is tried afresh.
 * - `onStatus` follows the stream's state (lib/realtime/status.ts): queries whose disposition is SSE
 *   poll while it is not `open` (Appendix E §E.5.1 "Backpressure").
 */

/** The parts of `EventSource` the stream uses (a fake in tests). */
export interface EventSourceLike {
    readonly readyState: number;
    onopen: ((this: EventSource, ev: Event) => unknown) | null;
    onerror: ((this: EventSource, ev: Event) => unknown) | null;
    addEventListener(type: string, listener: (ev: MessageEvent) => void): void;
    close(): void;
}

/** One decoded event. */
export interface RealtimeFrame<T = unknown> {
    /** The SSE event name (`session.revoked`, `task.assigned`, ...). */
    event: string;
    /** The envelope's `type` (equal to `event` for domain events). */
    type: string;
    topic: string;
    eventId: string;
    data: T;
    /** The SSE `id` (global sequence), "" when absent. */
    id: string;
    /** `Date.now()` when the browser received it. */
    receivedAt: number;
}

/**
 * The stream's state: `idle` (no stream: signed out, closed), `connecting` (opening, or the browser
 * retrying by itself), `open`, `down` (closed, waiting to reopen or refreshing first).
 */
export type RealtimeStatus = "idle" | "connecting" | "open" | "down";

export interface RealtimeStreamDeps {
    url: string;
    createEventSource: (url: string) => EventSourceLike;
    /** Unforced shared refresh (lib/sharedRefresh.ts): `false` when the session is gone; rejects on a network failure. */
    refresh: (since: number) => Promise<boolean>;
    /** The session is gone (a refused refresh): end it in this tab. */
    onSessionLost: () => void;
    /** `event: resync`, and a reopened stream that had no position to resume from. */
    onResync: () => void;
    /** Every other registered event. */
    onEvent: (frame: RealtimeFrame) => void;
    /** Every change of `status`. */
    onStatus?: (status: RealtimeStatus) => void;
    now?: () => number;
    random?: () => number;
    setTimer?: (fn: () => void, ms: number) => unknown;
    clearTimer?: (handle: unknown) => void;
}

export const CONNECTING = 0;
export const OPEN = 1;
export const CLOSED = 2;
/** First reopen delay: a jittered 1-3 s (§10.8). */
export const REOPEN_MIN_MS = 1_000;
export const REOPEN_JITTER_MS = 2_000;
/** Ceiling of the doubling backoff after failed opens. */
export const REOPEN_MAX_MS = 60_000;

/** Consecutive failed opens with explicit topics after which the stream reopens without them. */
export const TOPIC_FAILURES_BEFORE_DROP = 2;
/** Go's cap on `?topics=` (internal/sse `MaxExplicitTopics`; more is `422 topics too_many`). */
export const MAX_EXPLICIT_TOPICS = 20;

export const EVENT_RECONNECT = "reconnect";
export const EVENT_RESYNC = "resync";

function parseFrame(event: string, ev: MessageEvent, receivedAt: number): RealtimeFrame {
    let env: unknown;
    try {
        env = typeof ev.data === "string" && ev.data !== "" ? JSON.parse(ev.data) : {};
    } catch {
        env = {};
    }
    const e = (typeof env === "object" && env !== null ? env : {}) as Record<string, unknown>;
    const isEnvelope = typeof e.type === "string";
    return {
        event,
        type: isEnvelope ? (e.type as string) : event,
        topic: typeof e.topic === "string" ? e.topic : "",
        eventId: typeof e.eventId === "string" ? e.eventId : "",
        data: isEnvelope ? e.data : e,
        id: typeof ev.lastEventId === "string" ? ev.lastEventId : "",
        receivedAt,
    };
}

export class RealtimeStream {
    private source: EventSourceLike | null = null;
    private readonly names = new Set<string>();
    private lastEventId = "";
    private failures = 0;
    private timer: unknown = null;
    /** Trigger time of the reopen the timer waits for (the refresh's `since`). */
    private pendingSince = 0;
    private stopped = true;
    private current: RealtimeStatus = "idle";
    /** The explicit topics wanted (sorted, unique). */
    private explicit: readonly string[] = [];
    /**
     * The explicit topics the current connection asks for: the wanted ones and those no longer wanted that
     * it still carries, least recently wanted first.
     */
    private carried: readonly string[] = [];
    /** The explicit topics in the current connection's URL (none once they were dropped). */
    private requested: readonly string[] = [];
    /** The explicit topics are left out of the next connections (they kept the stream from opening). */
    private topicsDropped = false;
    /** Consecutive opens with explicit topics that failed before the stream opened. */
    private topicFailures = 0;
    /** The explicit topics of the open connection (Go accepted them at connect). */
    private openTopics: ReadonlySet<string> = new Set();
    private readonly now: () => number;
    private readonly random: () => number;
    private readonly setTimer: (fn: () => void, ms: number) => unknown;
    private readonly clearTimer: (handle: unknown) => void;

    constructor(private readonly deps: RealtimeStreamDeps) {
        this.now = deps.now ?? Date.now;
        this.random = deps.random ?? Math.random;
        this.setTimer = deps.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
        this.clearTimer = deps.clearTimer ?? ((h) => clearTimeout(h as ReturnType<typeof setTimeout>));
    }

    /** Whether a stream is open or about to reopen. */
    get running(): boolean {
        return !this.stopped;
    }

    /** The stream's state. */
    get status(): RealtimeStatus {
        return this.current;
    }

    /** The last event id seen (sent as `lastEventId` when the stream is recreated). */
    get lastId(): string {
        return this.lastEventId;
    }

    /** The explicit topics wanted (`?topics=`), sorted. */
    get topics(): readonly string[] {
        return this.explicit;
    }

    /** Whether the open connection carries the explicit `topic` (Go authorised it at connect). */
    hasTopic(topic: string): boolean {
        return this.openTopics.has(topic);
    }

    /**
     * Sets the explicit topics (`?topics=`). A wanted topic the connection lacks recreates a running stream
     * now, from the last id seen, so Go replays the new topics' events after it (R52), carrying along the
     * topics no longer wanted (up to `MAX_EXPLICIT_TOPICS`); a set the connection already carries (a room
     * left, or opened again) changes nothing. The caller throttles the calls (§10.8: at most once per
     * 500 ms). While the stream waits out a backoff or refreshes, the next attempt takes the new set. A new
     * set is tried afresh, even after an earlier set was dropped.
     */
    setTopics(topics: Iterable<string>): void {
        const next = [...new Set(topics)].sort();
        if (next.length === this.explicit.length && next.every((t, i) => t === this.explicit[i])) return;
        this.explicit = next;
        this.topicsDropped = false;
        this.topicFailures = 0;
        if (this.stopped || this.source === null) return;
        const onConnection = new Set(this.requested);
        // Most recently wanted last: past Go's cap the least recently wanted is dropped first.
        const merged = [...this.carried.filter((t) => onConnection.has(t) && !next.includes(t)), ...next];
        this.carried = merged.slice(Math.max(0, merged.length - Math.max(MAX_EXPLICIT_TOPICS, next.length)));
        if (next.every((t) => onConnection.has(t))) return;
        this.cancelTimer();
        this.detach();
        // No position to resume from (no named event seen yet): Go cannot replay the gap, so resync once open.
        this.connect(this.lastEventId === "", true);
    }

    /** Opens the stream (no-op while running). */
    open(): void {
        if (!this.stopped) return;
        this.stopped = false;
        this.connect(false);
    }

    /** Closes the stream for good (sign-out, unmount). */
    close(): void {
        this.stopped = true;
        this.cancelTimer();
        this.detach();
        this.setStatus("idle");
    }

    /**
     * Recreates the stream now (new claims, a tenant switch): the implicit topics come from the new
     * token. Its owner refetches what the change made stale, so no catch-up is asked for.
     */
    reopen(): void {
        if (this.stopped) return;
        this.cancelTimer();
        this.detach();
        this.failures = 0;
        this.connect(false);
    }

    /**
     * Reopens at once when the stream is waiting out its backoff (the browser is back online, the tab
     * is visible again). Nothing to do while it is open, connecting, or refreshing before a reopen.
     */
    resume(): void {
        if (this.stopped || this.timer === null) return;
        this.cancelTimer();
        this.attempt(this.pendingSince);
    }

    /** Adds event names to deliver to `onEvent`. */
    listen(names: Iterable<string>): void {
        for (const name of names) {
            if (this.names.has(name) || name === EVENT_RECONNECT || name === EVENT_RESYNC) continue;
            this.names.add(name);
            const source = this.source;
            source?.addEventListener(name, (ev) => this.dispatch(name, ev, source));
        }
    }

    private setStatus(next: RealtimeStatus): void {
        if (this.current === next) return;
        this.current = next;
        this.deps.onStatus?.(next);
    }

    private url(topics: readonly string[]): string {
        const params: string[] = [];
        if (topics.length > 0) params.push(`topics=${encodeURIComponent(topics.join(","))}`);
        if (this.lastEventId) params.push(`lastEventId=${encodeURIComponent(this.lastEventId)}`);
        if (params.length === 0) return this.deps.url;
        const sep = this.deps.url.includes("?") ? "&" : "?";
        return `${this.deps.url}${sep}${params.join("&")}`;
    }

    /** A connection that never opened has failed: count it against its explicit topics (EventSource hides a 403). */
    private failedBeforeOpen(topics: readonly string[]): void {
        if (topics.length > 0) {
            this.topicFailures++;
            if (this.topicFailures >= TOPIC_FAILURES_BEFORE_DROP) this.topicsDropped = true;
        } else if (this.topicsDropped) {
            // Without them it failed too: an outage, not a refusal. Try them again next time.
            this.topicsDropped = false;
            this.topicFailures = 0;
        }
    }

    /**
     * `catchUp`: a reopen without a position, so the owner resyncs once the new stream is open. `keep`: the
     * topics a recreate carries along (setTopics); any other (re)open asks for the wanted ones only.
     */
    private connect(catchUp: boolean, keep = false): void {
        if (!keep) this.carried = this.explicit;
        const topics = this.topicsDropped ? [] : [...this.carried].sort();
        this.requested = topics;
        const source = this.deps.createEventSource(this.url(topics));
        this.source = source;
        this.openTopics = new Set();
        this.setStatus("connecting");
        let pendingCatchUp = catchUp;
        let opened = false;
        source.onopen = () => {
            if (this.source !== source) return;
            opened = true;
            this.failures = 0;
            if (topics.length > 0) this.topicFailures = 0;
            this.openTopics = new Set(topics);
            this.setStatus("open");
            if (pendingCatchUp) {
                pendingCatchUp = false;
                this.deps.onResync();
            }
        };
        source.onerror = () => {
            if (this.source !== source) return;
            if (source.readyState !== CLOSED) {
                // The browser reconnects by itself and replays from its own Last-Event-ID.
                this.setStatus("connecting");
                return;
            }
            if (!opened) this.failedBeforeOpen(topics);
            this.detach();
            this.recover(this.now());
        };
        source.addEventListener(EVENT_RECONNECT, (ev) => {
            if (this.source !== source) return;
            this.track(ev);
            this.detach();
            this.recover(this.now(), true);
        });
        source.addEventListener(EVENT_RESYNC, (ev) => {
            if (this.source !== source) return;
            this.track(ev);
            this.deps.onResync();
        });
        for (const name of this.names) source.addEventListener(name, (ev) => this.dispatch(name, ev, source));
    }

    private dispatch(name: string, ev: MessageEvent, source: EventSourceLike): void {
        if (this.source !== source) return;
        this.track(ev);
        this.deps.onEvent(parseFrame(name, ev, this.now()));
    }

    private track(ev: MessageEvent): void {
        if (typeof ev.lastEventId === "string" && ev.lastEventId !== "") this.lastEventId = ev.lastEventId;
    }

    private detach(): void {
        const s = this.source;
        this.source = null;
        this.openTopics = new Set();
        if (s) {
            s.onopen = null;
            s.onerror = null;
            s.close();
        }
    }

    private cancelTimer(): void {
        if (this.timer !== null) {
            this.clearTimer(this.timer);
            this.timer = null;
        }
    }

    /** Delay before the next open: jittered 1-3 s, doubled per consecutive failure up to 60 s. */
    delay(): number {
        const base = REOPEN_MIN_MS + this.random() * REOPEN_JITTER_MS;
        return Math.min(REOPEN_MAX_MS, base * 2 ** Math.min(this.failures, 6));
    }

    /** Waits out the backoff, then refreshes and reopens; a planned `reconnect` does not count as a failure. */
    private recover(since: number, planned = false): void {
        if (this.stopped) return;
        this.cancelTimer();
        const wait = this.delay();
        if (!planned) this.failures++;
        this.pendingSince = since;
        this.setStatus("down");
        this.timer = this.setTimer(() => {
            this.timer = null;
            this.attempt(since);
        }, wait);
    }

    /** Refresh, then reopen from the last id seen; a refused refresh ends the session. */
    private attempt(since: number): void {
        // Closed, or a stream already recreated (reopen(), or an earlier attempt that won a race).
        if (this.stopped || this.source !== null) return;
        this.setStatus("down");
        this.deps.refresh(since).then(
            (ok) => {
                // Closed meanwhile, or already recreated by reopen().
                if (this.stopped || this.source !== null) return;
                if (!ok) {
                    this.close();
                    this.deps.onSessionLost();
                    return;
                }
                this.connect(this.lastEventId === "");
            },
            () => {
                // The BFF is unreachable: try again later; nothing is ended.
                if (!this.stopped && this.source === null) this.recover(this.now());
            }
        );
    }
}
