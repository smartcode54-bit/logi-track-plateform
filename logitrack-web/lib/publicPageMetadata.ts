import type { Metadata } from "next";

import en from "@/context/locales/en";
import { publicPageTitle, type PublicPage } from "./publicPageTitle";

/**
 * The server-rendered `metadata` of a public page (UI3E): its English title, the language a first visit starts in
 * (`DEFAULT_LANGUAGE`). Only route layouts (server components) import this module, so the English dictionary it reads
 * stays on the server; the client switches the title to the active language (components/public-page-title.tsx).
 */
export function publicPageMetadata(page: PublicPage): Metadata {
    return { title: publicPageTitle(page, (key) => en[key] ?? key) };
}
