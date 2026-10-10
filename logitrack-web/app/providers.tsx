"use client";

/**
 * The client providers of every page (developer-spec.md §10.6): QueryClientProvider > LanguageProvider
 * > AuthProvider (`['me']` + the Firebase bridge, T18) > RealtimeProvider (the session stream, T18;
 * domain events in TW5) > children + Toaster. One QueryClient per tab. TW4 tunes the query defaults,
 * adds `useWebFlagsSync()` and the devtools here.
 */
import { useState } from "react";
import { QueryClientProvider } from "@tanstack/react-query";

import { Toaster } from "@/components/ui/sonner";
import { AuthProvider } from "@/context/auth";
import { LanguageProvider } from "@/context/language";
import { RealtimeProvider } from "@/context/realtime";
import { makeQueryClient } from "@/lib/queryClient";

export function Providers({ children }: { children: React.ReactNode }) {
    const [client] = useState(makeQueryClient);
    return (
        <QueryClientProvider client={client}>
            <LanguageProvider>
                <AuthProvider>
                    <RealtimeProvider>
                        {children}
                        <Toaster duration={5000} closeButton richColors />
                    </RealtimeProvider>
                </AuthProvider>
            </LanguageProvider>
        </QueryClientProvider>
    );
}
