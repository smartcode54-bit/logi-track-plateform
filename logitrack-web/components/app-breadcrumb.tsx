"use client";

/**
 * The header breadcrumb of the app shell (UI3, issue #143). Its crumbs are the menu's own labels
 * (components/app-nav.ts, i18n keys), so Thai mode reads Thai. A detail page keeps its entry as a link back to the
 * list and ends with its own name (`useBreadcrumb().setCustomLastItem`, e.g. a truck's plate) or, until it has one,
 * a generic "New" / "Edit" / "Details". A crumb is a link only when the edge gate would open it for this principal
 * (lib/routeCapabilities.ts, as the sidebar filters); otherwise it is plain text.
 *
 * Width: the current page always shows (truncated); Home joins from `md` and the group and entry crumbs from `lg`,
 * so the trail never runs under the tenant switcher or the user menu. Below `sm` the header shows the scope line
 * instead (app/app/layout.tsx, UI3B).
 *
 * The tab's title (UI3B, WCAG 2.4.2) comes from the same trail: "<page> · LogiTrack Pro", a detail page
 * "<its name> · <entry> · LogiTrack Pro" (the record name a page's PageHeader sets, UI3D), in the active language. It
 * was "LogiTrack Pro by GORATECH" on every route. One title behaviour for the console and the public pages: the shared
 * `useDocumentTitle` (hooks/useDocumentTitle.ts, UI3E).
 */
import { Fragment, useMemo } from "react";
import Link from "next/link";
import { House } from "lucide-react";

import {
    Breadcrumb,
    BreadcrumbItem,
    BreadcrumbLink,
    BreadcrumbList,
    BreadcrumbPage,
    BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { useBreadcrumb } from "@/context/breadcrumb";
import { useLanguage } from "@/context/language";
import type { MeDTO } from "@/features/auth/api/me";
import { useMe } from "@/features/auth/api/useMe";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { routeAllowed } from "@/lib/routeCapabilities";
import { cn } from "@/lib/utils";
import { breadcrumbTrail } from "./app-nav";

interface Item {
    label: string;
    href?: string;
    home?: boolean;
}

const NO_CAPABILITIES: readonly string[] = [];
const selectCapabilities = (me: MeDTO | null) => me?.capabilities ?? NO_CAPABILITIES;

/** The tab's title from the page's names, most specific first: "Edit · Customers · LogiTrack Pro". */
export function documentTitle(names: readonly string[], brand: string): string {
    return [...names, brand].filter(Boolean).join(" · ");
}

export function AppBreadcrumb({ pathname, homeHref, className }: { pathname: string; homeHref: string; className?: string }) {
    const { customLastItem } = useBreadcrumb();
    const { t } = useLanguage();
    const { data: capabilities = NO_CAPABILITIES } = useMe(selectCapabilities);
    const held = useMemo(() => new Set(capabilities), [capabilities]);
    // The groups of this principal's menu (its home is the first entry, components/app-nav.ts).
    const { crumbs, detailKey } = breadcrumbTrail(pathname, homeHref);

    const items: Item[] = [
        { label: t("nav.home"), href: homeHref, home: true },
        ...crumbs.map((crumb) => ({ label: t(crumb.labelKey), href: crumb.href && routeAllowed(held, crumb.href) ? crumb.href : undefined })),
    ];
    // The page's own name, else the generic name of a detail page; either way the entry above stays a link.
    const detailName = customLastItem || (detailKey ? t(detailKey) : "");
    if (detailName) items.push({ label: detailName });

    const entry = crumbs.length > 0 ? t(crumbs[crumbs.length - 1].labelKey) : "";
    const title = documentTitle([detailName, entry], t("brand.name"));
    useDocumentTitle(title);

    return (
        <Breadcrumb aria-label={t("shell.breadcrumb")} className={cn("min-w-0 overflow-hidden", className)}>
            <BreadcrumbList className="flex-nowrap">
                {items.map((item, index) => {
                    const last = index === items.length - 1;
                    // Home from md, the crumbs between Home and the page from lg; the page at every width.
                    const shown = last ? "min-w-0" : index === 0 ? "hidden md:inline-flex" : "hidden min-w-0 lg:inline-flex";
                    return (
                        <Fragment key={`${index}-${item.label}`}>
                            <BreadcrumbItem className={shown}>
                                {last ? (
                                    <BreadcrumbPage className="truncate font-medium">{item.label}</BreadcrumbPage>
                                ) : item.href ? (
                                    <BreadcrumbLink asChild>
                                        <Link href={item.href} prefetch={false} className="flex min-w-0 items-center gap-1.5">
                                            {item.home ? <House className="size-4 shrink-0" aria-hidden /> : null}
                                            <span className="max-w-48 truncate">{item.label}</span>
                                        </Link>
                                    </BreadcrumbLink>
                                ) : (
                                    <span className="max-w-48 truncate">{item.label}</span>
                                )}
                            </BreadcrumbItem>
                            {last ? null : <BreadcrumbSeparator className={cn(index === 0 ? "hidden md:inline-flex" : "hidden lg:inline-flex")} />}
                        </Fragment>
                    );
                })}
            </BreadcrumbList>
        </Breadcrumb>
    );
}
