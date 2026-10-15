"use client";

import { useLanguage } from "@/context/language";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { publicPageTitle, type PublicPage } from "@/lib/publicPageTitle";

/**
 * The tab title of a public page in the active language (UI3E): "Sign in · LogiTrack Pro", "เข้าสู่ระบบ · LogiTrack
 * Pro". The route layout's server `metadata` starts in Thai from the `th` dictionary (UI4G); this client title follows
 * the active language, including an explicit stored choice, at once.
 */
export function PublicPageTitle({ page }: { page: PublicPage }) {
    const { t } = useLanguage();
    useDocumentTitle(publicPageTitle(page, t));
    return null;
}
