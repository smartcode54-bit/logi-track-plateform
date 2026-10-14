"use client";

import { useLanguage } from "@/context/language";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { publicPageTitle, type PublicPage } from "@/lib/publicPageTitle";

/**
 * The tab title of a public page in the active language (UI3E): "Sign in · LogiTrack Pro", "เข้าสู่ระบบ · LogiTrack
 * Pro". Rendered by the route's layout next to its English `metadata` (lib/publicPageMetadata.ts); it follows a
 * language switch at once.
 */
export function PublicPageTitle({ page }: { page: PublicPage }) {
    const { t } = useLanguage();
    useDocumentTitle(publicPageTitle(page, t));
    return null;
}
