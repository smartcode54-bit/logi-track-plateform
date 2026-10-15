import type { Metadata } from "next";

import th from "@/context/locales/th";
import { publicPageTitle, type PublicPage } from "./publicPageTitle";

/**
 * The server-rendered `metadata` of a public page (UI3E): its Thai title, the language a first visit starts in
 * (`DEFAULT_LANGUAGE`). Only route layouts (server components) import this module, so the Thai dictionary it reads
 * stays on the server; the client switches the title to the active language (components/public-page-title.tsx).
 */
export function publicPageMetadata(page: PublicPage): Metadata {
    return { title: publicPageTitle(page, (key) => th[key] ?? key) };
}
