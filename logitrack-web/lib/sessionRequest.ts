import { isApiError } from "./apiError";
import { endSession, onProtectedPage } from "./sessionEnd";
import { assertSessionGeneration, sessionGeneration } from "./sessionGeneration";
import { sharedRefresh } from "./sharedRefresh";

/** Auth reads and session-bound BFF calls: the usual refresh-once rule, scoped to the starting session. */
export async function sessionRequest<T>(
    send: () => Promise<T>,
    { signal, allowSignedOut = false }: { signal?: AbortSignal; allowSignedOut?: boolean } = {}
): Promise<T> {
    const generation = sessionGeneration();
    const since = Date.now();
    const attempt = async () => {
        assertSessionGeneration(generation);
        signal?.throwIfAborted();
        try {
            const result = await send();
            assertSessionGeneration(generation);
            signal?.throwIfAborted();
            return result;
        } catch (error) {
            // A received 401 still ends the current session even if its query was cancelled.
            assertSessionGeneration(generation);
            throw error;
        }
    };

    try {
        return await attempt();
    } catch (first) {
        if (!isApiError(first) || first.status !== 401) throw first;
        if (first.code !== "token_expired" && first.code !== "unauthenticated") {
            endSession(first);
            throw first;
        }
        const refreshed = await sharedRefresh({ force: first.details.reason === "claims_changed", since });
        assertSessionGeneration(generation);
        if (!refreshed) {
            if (!(allowSignedOut && first.code === "unauthenticated" && !onProtectedPage())) endSession(first);
            throw first;
        }
        signal?.throwIfAborted();
        try {
            return await attempt();
        } catch (second) {
            if (isApiError(second) && second.status === 401) endSession(second);
            throw second;
        }
    }
}
