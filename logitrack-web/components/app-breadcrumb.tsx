"use client";

/**
 * The header breadcrumb of the app shell (UI3, issue #143). Its crumbs are the menu's own labels
 * (components/app-nav.ts, i18n keys), so Thai mode reads Thai; a detail page shows its entry's label until the
 * page names itself (`useBreadcrumb().setCustomLastItem`, e.g. a truck's plate). Below `md` only the current
 * page is shown, as the header's title.
 */
import { Fragment } from "react";
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
import { cn } from "@/lib/utils";
import { breadcrumbTrail } from "./app-nav";

interface Item {
    label: string;
    href?: string;
    home?: boolean;
}

export function AppBreadcrumb({ pathname, homeHref }: { pathname: string; homeHref: string }) {
    const { customLastItem } = useBreadcrumb();
    const { t } = useLanguage();
    const { crumbs } = breadcrumbTrail(pathname);

    const items: Item[] = [
        { label: t("nav.home"), href: homeHref, home: true },
        ...crumbs.map((crumb) => ({ label: t(crumb.labelKey), href: crumb.href })),
    ];
    if (customLastItem) items.push({ label: customLastItem });

    return (
        <Breadcrumb aria-label={t("shell.breadcrumb")} className="min-w-0">
            <BreadcrumbList className="flex-nowrap">
                {items.map((item, index) => {
                    const last = index === items.length - 1;
                    return (
                        <Fragment key={`${index}-${item.label}`}>
                            <BreadcrumbItem className={cn(last ? "min-w-0" : "hidden md:inline-flex")}>
                                {last ? (
                                    <BreadcrumbPage className="truncate font-medium">{item.label}</BreadcrumbPage>
                                ) : item.href ? (
                                    <BreadcrumbLink asChild>
                                        <Link href={item.href} prefetch={false} className="flex items-center gap-1.5 whitespace-nowrap">
                                            {item.home ? <House className="size-4" aria-hidden /> : null}
                                            {item.label}
                                        </Link>
                                    </BreadcrumbLink>
                                ) : (
                                    <span className="whitespace-nowrap">{item.label}</span>
                                )}
                            </BreadcrumbItem>
                            {last ? null : <BreadcrumbSeparator className="hidden md:inline-flex" />}
                        </Fragment>
                    );
                })}
            </BreadcrumbList>
        </Breadcrumb>
    );
}
