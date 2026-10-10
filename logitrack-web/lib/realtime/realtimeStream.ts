/**
 * The tab's one realtime stream (developer-spec.md §8, §10.8; Appendix B §B.4.1; Appendix E §E.5.1):
 * `EventSource('/api/go/v1/events')`, same origin, so the HttpOnly `lt_at` cookie travels with it and
 * the BFF turns it into the bearer (no SSE ticket on the web, R42). Implicit topics come from the
 * token (`user:{uid}` carries `session.revoked`, R50).
 *
 * Wire format (internal/sse): every event is `id: <seq>`, `event: <name>`, `data:
 * {"type","topic","eventId","data"}`; the control events `reconnect` and `resync` carry `{reason}`.
 *
 * - `reconnect` (drain, or the access token's `exp`): an unforced shared refresh, then reopen after a
 *   jittered 1-3 s with `?lastEventId=`, which the BFF turns into `Last-Event-ID` (§10.3).
 * - `resync` (the id is older than the replay window): the owner refetches its active queries.
 * - `error` with `readyState === CLOSED` (EventSource hides the status: a 401 of an expired token,
 *   a 429 past `SSE_MAX_CONN_PER_USER`, the api down): refresh, then reopen with a backoff that
 *   doubles from 1-3 s up to 60 s; a refused refresh means the session is gone. While the browser
 *   is reconnecting by itself (`CONNECTING`) nothing is done.
 * - Every other named event goes to `onEvent`; the names to listen to are registered with `listen`
 *   (EventSource delivers a named event only to listeners of that name).
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

export interface RealtimeStreamDeps {
    url: string;
    createEventSource: (url: string) => EventSourceLike;
    /** Unforced shared refresh (lib/sharedRefresh.ts): `false` when the session is gone; rejects on a network failure. */
    refresh: (since: number) => Promise<boolean>;
    /** The session is gone (a refused refresh): end it in this tab. */
    onSessionLost: () => void;
    /** `event: resync`. */
    onResync: () => void;
    /** Every other registered event. */
    onEvent: (frame: RealtimeFrame) => void;
    now?: () => number;
    random?: () => number;
    setTimer?: (fn: () => void, ms: number) => unknown;
    clearTimer?: (handle: unknown) => void;
}

export const CLOSED = 2;
/** First reopen delay: a jittered 1-3 s (§10.8). */
export const REOPEN_MIN_MS = 1_000;
export const REOPEN_JITTER_MS = 2_000;
/** Ceiling of the doubling backoff after failed opens. */
export const REOPEN_MAX_MS = 60_000;

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
    private stopped = true;
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

    /** The last event id seen (sent as `lastEventId` when the stream is recreated). */
    get lastId(): string {
        return this.lastEventId;
    }

    /** Opens the stream (no-op while running). */
    open(): void {
        if (!this.stopped) return;
        this.stopped = false;
        this.connect();
    }

    /** Closes the stream for good (sign-out, unmount). */
    close(): void {
        this.stopped = true;
        this.cancelTimer();
        this.detach();
    }

    /** Recreates the stream now (new claims, a tenant switch): the implicit topics come from the new token. */
    reopen(): void {
        if (this.stopped) return;
        this.cancelTimer();
        this.detach();
        this.failures = 0;
        this.connect();
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

    private url(): string {
        if (!this.lastEventId) return this.deps.url;
        const sep = this.deps.url.includes("?") ? "&" : "?";
        return `${this.deps.url}${sep}lastEventId=${encodeURIComponent(this.lastEventId)}`;
    }

    private connect(): void {
        const source = this.deps.createEventSource(this.url());
        this.source = source;
        source.onopen = () => {
            if (this.source === source) this.failures = 0;
        };
        source.onerror = () => {
            if (this.source !== source || source.readyState !== CLOSED) return;
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

    /** Refresh, then reopen after the backoff; a planned `reconnect` does not count as a failure. */
    private recover(since: number, planned = false): void {
        if (this.stopped) return;
        const wait = this.delay();
        if (!planned) this.failures++;
        this.timer = this.setTimer(() => {
            this.timer = null;
            if (this.stopped) return;
            this.deps.refresh(since).then(
                (ok) => {
                    if (this.stopped) return;
                    if (!ok) {
                        this.close();
                        this.deps.onSessionLost();
                        return;
                    }
                    this.connect();
                },
                () => {
                    // The BFF is unreachable: try again later; nothing is ended.
                    if (!this.stopped) this.recover(this.now());
                }
            );
        }, wait);
    }
}
