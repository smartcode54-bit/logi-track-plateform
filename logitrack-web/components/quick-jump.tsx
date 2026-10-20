"use client";

import { useEffect, useEffectEvent, useRef, useState, type ComponentType } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { usePathname, useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";
import { confirmNavigation } from "@/lib/navigationGuard";
import { homeRouteOf } from "@/features/auth/api/homeRoute";
import type { MeDTO } from "@/features/auth/api/me";
import { useMe } from "@/features/auth/api/useMe";
import { navigationProfileOf } from "@/features/auth/utils/navigationProfile";
import { menuFor, menuLinksFor, SECURITY_CENTER_PATH, securityMenuFor } from "./app-nav";
import type { QuickJumpDialogProps } from "./quick-jump-dialog";

export function quickJumpLinks(me: MeDTO | null | undefined, pathname: string) {
    if (!me) return [];
    const held = new Set(me.capabilities);
    const home = homeRouteOf(me);
    return menuLinksFor(pathname.startsWith(SECURITY_CENTER_PATH)
        ? securityMenuFor(held, home)
        : menuFor(held, home, navigationProfileOf(me), me));
}

const isApple = () => typeof navigator !== "undefined" && /Mac|iPhone|iPad|iPod/.test(navigator.userAgent);
const focusMain = () => document.getElementById("main-content")?.focus();

/** Reset the open dialog and query when the principal or active tenant changes, without resetting on translation. */
export function QuickJump() {
    const { data: me } = useMe();
    const identity = JSON.stringify([me?.id, me?.tenant?.id, me?.tenant?.role, me?.tenant?.kind,
        me?.platformRoles, me?.dispatcher, me?.steward, me?.driver?.id, me?.customerScopes.map((scope) => scope.billingPartyId)]);
    return <QuickJumpControl key={identity} me={me} />;
}

function QuickJumpControl({ me }: { me: MeDTO | null | undefined }) {
    const { t } = useLanguage();
    const pathname = usePathname() ?? "";
    const router = useRouter();
    const client = useQueryClient();
    const [open, setOpen] = useState(false);
    const [failed, setFailed] = useState(false);
    const [DialogComponent, setDialogComponent] = useState<ComponentType<QuickJumpDialogProps> | null>(null);
    const [apple] = useState(isApple);
    const trigger = useRef<HTMLButtonElement>(null);
    const dialog = useRef<HTMLDivElement>(null);
    const invoker = useRef<HTMLElement | null>(null);
    const pending = useRef<string | null>(null);
    const lifetime = useRef({ active: true });
    const loading = useRef(false);

    const load = () => {
        if (loading.current || DialogComponent) return;
        loading.current = true;
        setFailed(false);
        const current = lifetime.current;
        import("./quick-jump-dialog").then((module) => {
            if (!current.active) return;
            loading.current = false;
            setDialogComponent(() => module.QuickJumpDialog);
        }, () => {
            if (!current.active) return;
            loading.current = false;
            setFailed(true);
        });
    };

    const show = () => {
        if (!me) return;
        if (!open) invoker.current = document.activeElement instanceof HTMLElement ? document.activeElement : trigger.current;
        pending.current = null;
        setOpen(true);
        load();
    };

    const shortcut = useEffectEvent((event: KeyboardEvent) => {
        if (open && !DialogComponent && event.key === "Escape" && !event.defaultPrevented) {
            setOpen(false);
            restoreFocus();
            return;
        }
        if (event.defaultPrevented || event.repeat || event.isComposing || event.keyCode === 229 ||
            event.key.toLowerCase() !== "k" || event.altKey || event.shiftKey ||
            (isApple() ? !event.metaKey || event.ctrlKey : !event.ctrlKey || event.metaKey)) return;
        const otherOverlay = [...document.querySelectorAll<HTMLElement>(
            '[role="dialog"], [role="alertdialog"], [role="menu"], [role="listbox"], [data-slot="popover-content"][data-state="open"]'
        )].some((node) => !dialog.current?.contains(node) && node !== dialog.current &&
            !node.closest('[hidden], [aria-hidden="true"], [data-state="closed"]'));
        if (otherOverlay) return;
        if (open) {
            event.preventDefault();
            dialog.current?.querySelector<HTMLInputElement>('[role="combobox"]')?.focus();
            return;
        }
        if (event.target instanceof HTMLElement && (event.target.isContentEditable ||
            event.target.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"]'))) return;
        event.preventDefault();
        show();
    });

    useEffect(() => {
        const current = lifetime.current;
        current.active = true;
        const listener = (event: KeyboardEvent) => shortcut(event);
        window.addEventListener("keydown", listener);
        return () => {
            window.removeEventListener("keydown", listener);
            current.active = false;
        };
    }, []);

    useEffect(() => {
        if (pending.current !== pathname) return;
        focusMain();
    }, [pathname]);

    const restoreFocus = () => {
        if (pending.current) return;
        const target = invoker.current?.isConnected ? invoker.current : trigger.current;
        target?.focus();
        if (document.activeElement !== target) trigger.current?.focus();
    };

    const select = (href: string) => {
        // Read the cache again: a capability refresh can overtake a rendered result or its selection callback.
        if (!quickJumpLinks(client.getQueryData<MeDTO | null>(["me"]), pathname).some((link) => link.href === href)) return;
        if (href !== pathname && !confirmNavigation()) return;
        pending.current = href;
        setOpen(false);
        if (href === pathname) {
            requestAnimationFrame(focusMain);
        } else router.push(href);
    };

    const keys = apple ? "⌘K" : "Ctrl+K";
    return (
        <div className="relative shrink-0">
            <Button ref={trigger} type="button" variant="ghost" className="h-11 gap-2 px-3 md:h-9"
                aria-label={t("shell.quickJump.open")} aria-keyshortcuts={apple ? "Meta+K" : "Control+K"}
                aria-haspopup="dialog" aria-expanded={open} title={t("shell.quickJump.hint", { keys })}
                onClick={show} data-testid="quick-jump-trigger">
                <Search className="size-5" aria-hidden="true" />
                <kbd aria-hidden="true" className="hidden text-sm text-muted-foreground md:inline">{keys}</kbd>
            </Button>
            {open && DialogComponent ? <DialogComponent entries={quickJumpLinks(me, pathname)} dialogRef={dialog}
                onClose={() => setOpen(false)} onSelect={select} onRestoreFocus={restoreFocus} /> : null}
            {open && !DialogComponent ? failed ? (
                <div role="alert" className="fixed inset-x-4 top-20 z-popover mx-auto max-w-lg rounded-lg border bg-popover p-4 text-popover-foreground shadow-lg">
                    <p className="text-sm">{t("common.lazyDialog.loadErrorTitle")}</p>
                    <div className="mt-4 flex flex-wrap gap-2">
                        <Button type="button" className="min-h-11" onClick={load}>{t("shell.retry")}</Button>
                        <Button type="button" variant="outline" className="min-h-11" onClick={() => { setOpen(false); restoreFocus(); }}>
                            {t("shell.quickJump.close")}
                        </Button>
                    </div>
                </div>
            ) : <div className="fixed inset-x-4 top-20 z-popover mx-auto flex max-w-lg items-center justify-between gap-4 rounded-lg border bg-popover p-4 text-popover-foreground shadow-lg">
                <span role="status" className="text-sm">{t("common.loading")}</span>
                <Button type="button" variant="outline" className="min-h-11" onClick={() => { setOpen(false); restoreFocus(); }}>{t("shell.quickJump.close")}</Button>
            </div> : null}
        </div>
    );
}
