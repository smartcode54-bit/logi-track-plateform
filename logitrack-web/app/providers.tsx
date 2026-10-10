"use client";

/**
 * The client providers of every page (developer-spec.md §10.6; TW4, T18):
 *
 *   QueryClientProvider > LanguageProvider > AuthProvider (`['me']` + the Firebase bridge, T18)
 *     > RealtimeProvider (the session stream, T18; domain events in TW5) > children + Toaster
 *
 * plus, beside them, the runtime that belongs to the cache rather than to any page: `['me']` is
 * fetched at once (in parallel with the language chunk, which the LanguageProvider waits for), the
 * cache follows the session (`bindQueryClientToSession`: emptied on a session end and on any change
 * of principal in `['me']`, invalidated after a `claims_changed` refresh), `['webFlags']` is kept
 * polled for a signed-in tab (`useWebFlagsSync`, T17), a 403 shows one toast in the active language,
 * and the TanStack devtools load lazily in development. The AuthProvider owns only what concerns the
 * Firebase bridge (sign-out, the forced re-mint) and the sign-in, logout and tenant-switch flows; it
 * never clears or invalidates the cache on those session events itself.
 */
import { useEffect, useState, type ReactNode } from "react";
import dynamic from "next/dynamic";
import { QueryClientProvider, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { AuthProvider } from "@/context/auth";
import { LanguageProvider, useLanguage } from "@/context/language";
import { RealtimeProvider } from "@/context/realtime";
import { Toaster } from "@/components/ui/sonner";
import { apiErrorText } from "@/lib/apiError";
import { bindQueryClientToSession, getQueryClient, setForbiddenNotifier } from "@/lib/queryClient";
import { meQueryOptions } from "@/features/auth/api/me";
import { useWebFlagsSync } from "@/features/platform/api/useWebFlags";

// Development only: the condition is a build-time constant, so production bundles never reference it.
const QueryDevtools =
    process.env.NODE_ENV === "development"
        ? dynamic(
              () =>
                  import("@tanstack/react-query-devtools").then(({ ReactQueryDevtools }) => {
                      function Devtools() {
                          return <ReactQueryDevtools initialIsOpen={false} buttonPosition="bottom-left" />;
                      }
                      return Devtools;
                  }),
              { ssr: false }
          )
        : () => null;

/** Keeps `['webFlags']` polled and watches for a domain flip (mounted only while signed in). */
function WebFlagsSync() {
    useWebFlagsSync();
    return null;
}

/** Cache-level runtime that needs no translation: session binding, `['me']`, web flags. */
function QueryRuntime() {
    const client = useQueryClient();
    useEffect(() => bindQueryClientToSession(client), [client]);
    // Starts `GET /v1/me` before the language chunk is in; the AuthProvider observes the same query.
    const { data: me } = useQuery(meQueryOptions);
    return me ? <WebFlagsSync /> : null;
}

/** One toast for a 403 from any query or mutation, in the active language. */
function ForbiddenToasts() {
    const { t } = useLanguage();
    useEffect(
        () => setForbiddenNotifier((error) => toast.error(apiErrorText(error, t), { id: "api-permission-denied" })),
        [t]
    );
    return null;
}

export function Providers({ children }: { children: ReactNode }) {
    const [client] = useState(getQueryClient);
    return (
        <QueryClientProvider client={client}>
            <QueryRuntime />
            <LanguageProvider>
                <ForbiddenToasts />
                <AuthProvider>
                    <RealtimeProvider>
                        {children}
                        <Toaster duration={5000} closeButton richColors />
                    </RealtimeProvider>
                </AuthProvider>
            </LanguageProvider>
            <QueryDevtools />
        </QueryClientProvider>
    );
}
