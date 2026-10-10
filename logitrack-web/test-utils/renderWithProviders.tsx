/**
 * Renders a component under the T18 client providers (QueryClient, Language, Auth) for the React tests.
 * The language is English (`localStorage.language` is cleared first).
 */
import React from "react";
import { render } from "@testing-library/react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";

import { AuthProvider } from "@/context/auth";
import { LanguageProvider } from "@/context/language";
import { makeQueryClient } from "@/lib/queryClient";

export function renderWithProviders(ui: React.ReactElement, client: QueryClient = makeQueryClient()) {
    try {
        window.localStorage.removeItem("language");
    } catch {
        // storage unavailable: English is the default anyway
    }
    const view = render(
        <QueryClientProvider client={client}>
            <LanguageProvider>
                <AuthProvider>{ui}</AuthProvider>
            </LanguageProvider>
        </QueryClientProvider>
    );
    return { client, ...view };
}
