"use client";

/**
 * The client providers of every page (developer-spec.md §10.6; TW4, T18, TW7; UI1):
 *
 *   ThemeProvider (next-themes: the `dark` class on <html>, stored under `theme`, following the system
 *   until the user picks one; UI1)
 *   > QueryClientProvider > LanguageProvider > AuthProvider (`['me']`, T18)
 *     > RealtimeProvider (the tab's event stream: session.revoked, T18; the event -> cache map, TW5)
 *     > children + Toaster
 *
 * plus, beside them, the runtime that belongs to the cache rather than to any page: `['me']` is
 * fetched at once (in parallel with the language chunk, which the LanguageProvider waits for), the
 * cache follows the session (`bindQueryClientToSession`: emptied on a session end and on any change
 * of principal in `['me']`, invalidated after a `claims_changed` refresh), a 403 shows one toast in
 * the active language, and the TanStack devtools load lazily in development. The AuthProvider owns
 * the sign-in, logout and tenant-switch flows; it never clears or invalidates the cache on those
 * session events itself. Since TW7 every page reads Go only: no Firebase bridge and no
 * `['webFlags']` poll (the per-domain Firestore/Go switch went with the Firestore pages).
 */
import { useEffect, useState, type ReactNode } from "react";
import dynamic from "next/dynamic";
import { ThemeProvider } from "next-themes";
import { QueryClientProvider, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { AuthProvider } from "@/context/auth";
import { LanguageProvider, useLanguage } from "@/context/language";
import { RealtimeProvider } from "@/lib/realtime/RealtimeProvider";
import { Toaster } from "@/components/ui/sonner";
import { apiErrorText } from "@/lib/apiError";
import { bindQueryClientToSession, getQueryClient, setForbiddenNotifier } from "@/lib/queryClient";
import { meQueryOptions } from "@/features/auth/api/me";

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

/** Cache-level runtime that needs no translation: session binding and `['me']`. */
function QueryRuntime() {
    const client = useQueryClient();
    useEffect(() => bindQueryClientToSession(client), [client]);
    // Starts `GET /v1/me` before the language chunk is in; the AuthProvider observes the same query.
    useQuery(meQueryOptions);
    return null;
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
        <ThemeProvider attribute="class" defaultTheme="system" enableSystem storageKey="theme" disableTransitionOnChange>
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
        </ThemeProvider>
    );
}
