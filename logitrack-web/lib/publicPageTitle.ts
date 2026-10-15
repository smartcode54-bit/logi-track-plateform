/**
 * The tab titles of the public pages (UI3E, WCAG 2.4.2): "<page> · LogiTrack Pro", the console's format
 * (components/app-breadcrumb.tsx `documentTitle`), in the active language. Each name is the page's own i18n key: the
 * navigation's label where the navigation links the page, else the page's heading. The landing `/` keeps the product's
 * full name, "LogiTrack Pro by GORATECH" (app/layout.tsx), in both languages.
 *
 * The server sets the Thai title through each route's `metadata` (lib/publicPageMetadata.ts), matching the Thai
 * default; the client puts the active language's title in place (components/public-page-title.tsx), including an
 * explicit stored choice. This module holds no dictionary, so a page that imports it carries no translation in its
 * initial JS.
 */

export const PUBLIC_PAGE_TITLE_KEYS = {
    "/login": "auth.login.title",
    "/waitlist": "auth.register.title",
    "/forgot-password": "auth.forgot.title",
    "/reset-password": "auth.reset.title",
    "/join-network": "nav.joinNetwork",
    "/about": "landing.nav.about",
} as const;

export type PublicPage = keyof typeof PUBLIC_PAGE_TITLE_KEYS;

/** The brand at the end of every title (`brand.name`, the same in both languages). */
export const TITLE_BRAND_KEY = "brand.name";

type Translate = (key: string) => string;

/** "<page> · LogiTrack Pro" in the language of `t`. */
export function publicPageTitle(page: PublicPage, t: Translate): string {
    return [t(PUBLIC_PAGE_TITLE_KEYS[page]), t(TITLE_BRAND_KEY)].filter(Boolean).join(" · ");
}
