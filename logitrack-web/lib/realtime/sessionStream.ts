/**
 * The tab's stream (developer-spec.md §10.4 steps 3-4, §10.8; Appendix E §E.5 row 20, §E.5.1; R50):
 * one `EventSource('/api/go/v1/events')` whose implicit `user:{uid}` topic carries `session.revoked`
 * (T18), and on which TW5 receives the domain events of the event map (lib/realtime/eventMap.ts).
 *
 * - `session.revoked` -> lib/realtime/sessionRevoked.ts (`claims_changed`: forced refresh, stay signed
 *   in; any other reason: ask Go whether this session survived, end it when it did not).
 * - the names in `events` -> `onEvent` (the provider applies them to the query cache).
 * - `resync` -> the active queries refetch (what was missed cannot be replayed).
 * - a stream that cannot be reopened because the refresh was refused -> the session ends here.
 */
import { ApiError } from "../apiError";
import { endSession } from "../sessionEnd";
import { sharedRefresh } from "../sharedRefresh";
import { RealtimeStream, type EventSourceLike, type RealtimeFrame, type RealtimeStatus } from "./realtimeStream";
import { handleSessionRevoked, type SessionRevokedDeps } from "./sessionRevoked";

/** The BFF passthrough of `GET /v1/events` (same origin: the `lt_at` cookie becomes the bearer). */
export const EVENTS_URL = "/api/go/v1/events";
export const EVENT_SESSION_REVOKED = "session.revoked";

export interface SessionStreamDeps {
    /** Refetches the active queries (`event: resync`, or a reopen without a position). */
    onResync: () => void;
    /** `GET /v1/me` through goFetch, whose 401 handling ends a revoked session. */
    checkSession: () => Promise<unknown>;
    /** Domain event names to receive besides `session.revoked` (EventSource needs every name). */
    events?: Iterable<string>;
    /** Each frame of `events`. */
    onEvent?: (frame: RealtimeFrame) => void;
    /** Each change of the stream's state. */
    onStatus?: (status: RealtimeStatus) => void;
    createEventSource?: (url: string) => EventSourceLike;
    /** Replaced in tests. */
    refresh?: (options: { force?: boolean; since: number }) => Promise<boolean>;
    endSession?: (cause: ApiError) => void;
    /** Called once a `session.revoked` frame has been handled (tests). */
    onHandled?: (frame: RealtimeFrame) => void;
    setTimer?: (fn: () => void, ms: number) => unknown;
    clearTimer?: (handle: unknown) => void;
    random?: () => number;
}

/** The tab's stream; call `open()` once a principal is signed in and `close()` on sign-out. */
export function createSessionStream(deps: SessionStreamDeps): RealtimeStream {
    const refresh = deps.refresh ?? sharedRefresh;
    const end = deps.endSession ?? endSession;
    const revoked: SessionRevokedDeps = {
        forcedRefresh: (since) => refresh({ force: true, since }),
        checkSession: deps.checkSession,
        endSession: end,
    };
    const stream = new RealtimeStream({
        url: EVENTS_URL,
        createEventSource: deps.createEventSource ?? ((url) => new EventSource(url) as unknown as EventSourceLike),
        refresh: (since) => refresh({ since }),
        onSessionLost: () =>
            end(new ApiError({ status: 401, code: "unauthenticated", message: "the event stream could not refresh the session" })),
        onResync: deps.onResync,
        onEvent: (frame) => {
            if (frame.event !== EVENT_SESSION_REVOKED) {
                deps.onEvent?.(frame);
                return;
            }
            void handleSessionRevoked(frame.data, frame.receivedAt, revoked).then(() => deps.onHandled?.(frame));
        },
        onStatus: deps.onStatus,
        setTimer: deps.setTimer,
        clearTimer: deps.clearTimer,
        random: deps.random,
    });
    stream.listen([EVENT_SESSION_REVOKED, ...(deps.events ?? [])]);
    return stream;
}
