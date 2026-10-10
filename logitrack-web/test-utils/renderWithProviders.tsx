/**
 * Renders a component under the client providers of app/providers.tsx (QueryClient, Language, Auth)
 * for the React tests, with the cache bound to the session as the providers' runtime binds it
 * (`bindQueryClientToSession`; unbound when the test unmounts). The language is English
 * (`localStorage.language` is cleared first).
 */
import React, { useEffect } from "react";
import { render } from "@testing-library/react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";

import { AuthProvider } from "@/context/auth";
import { LanguageProvider } from "@/context/language";
import { bindQueryClientToSession, createQueryClient } from "@/lib/queryClient";

function SessionBinding({ client }: { client: QueryClient }) {
    useEffect(() => bindQueryClientToSession(client), [client]);
    return null;
}

export function renderWithProviders(ui: React.ReactElement, client: QueryClient = createQueryClient()) {
    try {
        window.localStorage.removeItem("language");
    } catch {
        // storage unavailable: English is the default anyway
    }
    const view = render(
        <QueryClientProvider client={client}>
            <SessionBinding client={client} />
            <LanguageProvider>
                <AuthProvider>{ui}</AuthProvider>
            </LanguageProvider>
        </QueryClientProvider>
    );
    return { client, ...view };
}
