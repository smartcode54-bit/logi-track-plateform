"use client";

/**
 * The chat console's frame (both sources, T50): conversations on the left, the room or the broadcast
 * composer in the middle, the driver panel on the right; collapsible columns on a wide screen (their state
 * remembered in this browser), two sheets on a narrow one. The panels are slots: the Go layout passes its
 * hooks' panels, the Firestore layout its legacy ones. The middle column is a plain `div`: the page's one `<main>`
 * landmark is the app shell's (app/app/layout.tsx, UI3B).
 */
import { useEffect, useState, type ReactNode } from "react";
import { ChevronLeft, ChevronRight, MessageCircle, User } from "lucide-react";

import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { useIsLg } from "@/hooks/use-mobile";

const LEFT_OPEN_KEY = "logitrack_chat_left_open";
const RIGHT_OPEN_KEY = "logitrack_chat_right_open";

function getStored(key: string, fallback: boolean): boolean {
    if (typeof window === "undefined") return fallback;
    try {
        const v = localStorage.getItem(key);
        return v === null ? fallback : v === "true";
    } catch {
        return fallback;
    }
}

function setStored(key: string, value: boolean) {
    try {
        localStorage.setItem(key, String(value));
    } catch {
        // storage unavailable: the column state is not remembered
    }
}

export interface ChatShellProps {
    conversations: ReactNode;
    driverPanel: ReactNode;
    /** Dialogs rendered once beside the columns (the incident report). */
    overlays?: ReactNode;
    children: ReactNode;
}

export function ChatShell({ conversations, driverPanel, overlays, children }: ChatShellProps) {
    const { t } = useLanguage();
    const [leftOpen, setLeftOpen] = useState(true);
    const [rightOpen, setRightOpen] = useState(true);
    const [mounted, setMounted] = useState(false);
    const [conversationsSheetOpen, setConversationsSheetOpen] = useState(false);
    const [driverSheetOpen, setDriverSheetOpen] = useState(false);
    const isLg = useIsLg();

    useEffect(() => {
        setLeftOpen(getStored(LEFT_OPEN_KEY, true));
        setRightOpen(getStored(RIGHT_OPEN_KEY, true));
        setMounted(true);
    }, []);

    const handleLeftOpenChange = (open: boolean) => {
        setLeftOpen(open);
        setStored(LEFT_OPEN_KEY, open);
    };
    const handleRightOpenChange = (open: boolean) => {
        setRightOpen(open);
        setStored(RIGHT_OPEN_KEY, open);
    };

    if (!mounted) {
        return (
            <div className="flex h-[calc(100vh-4rem)] w-full items-center justify-center overflow-hidden">
                <div className="animate-pulse text-sm text-muted-foreground">{t("chat.shell.loading")}</div>
            </div>
        );
    }

    // Mobile/tablet: one column, conversations and the driver panel in sheets.
    if (!isLg) {
        return (
            <div className="flex h-[calc(100vh-4rem)] w-full flex-col overflow-hidden">
                <div className="flex shrink-0 items-center gap-2 border-b bg-muted/30 px-3 py-2">
                    <Sheet open={conversationsSheetOpen} onOpenChange={setConversationsSheetOpen}>
                        <SheetTrigger asChild>
                            <Button variant="outline" size="sm" className="gap-1.5">
                                <MessageCircle className="h-4 w-4" />
                                {t("chat.shell.conversations")}
                            </Button>
                        </SheetTrigger>
                        <SheetContent side="left" className="flex w-full max-w-[320px] flex-col p-0">
                            <SheetHeader className="border-b p-3">
                                <SheetTitle>{t("chat.shell.conversations")}</SheetTitle>
                            </SheetHeader>
                            <div className="flex-1 overflow-hidden">{conversations}</div>
                        </SheetContent>
                    </Sheet>
                    <Sheet open={driverSheetOpen} onOpenChange={setDriverSheetOpen}>
                        <SheetTrigger asChild>
                            <Button variant="outline" size="sm" className="gap-1.5">
                                <User className="h-4 w-4" />
                                {t("chat.shell.driver")}
                            </Button>
                        </SheetTrigger>
                        <SheetContent side="right" className="flex w-full max-w-[320px] flex-col">
                            <SheetHeader>
                                <SheetTitle>{t("chat.shell.driverPanel")}</SheetTitle>
                            </SheetHeader>
                            <div className="min-h-0 flex-1 overflow-hidden">{driverPanel}</div>
                        </SheetContent>
                    </Sheet>
                </div>
                <div className="flex min-w-0 flex-1 flex-col overflow-hidden bg-background">{children}</div>
                {overlays}
            </div>
        );
    }

    // Desktop: three columns, the side ones collapsible.
    return (
        <div className="flex h-[calc(100vh-4rem)] w-full overflow-hidden">
            <Collapsible open={leftOpen} onOpenChange={handleLeftOpenChange} className="shrink-0">
                <aside className={`flex flex-col overflow-hidden border-r bg-muted/30 transition-[width] duration-200 ${leftOpen ? "w-[280px]" : "w-12"}`}>
                    <CollapsibleTrigger asChild>
                        <Button
                            variant="ghost"
                            size="icon"
                            className="m-1 h-8 w-8 shrink-0"
                            aria-label={leftOpen ? t("chat.shell.collapseConversations") : t("chat.shell.expandConversations")}
                        >
                            {leftOpen ? <ChevronLeft className="h-4 w-4" /> : <MessageCircle className="h-4 w-4" />}
                        </Button>
                    </CollapsibleTrigger>
                    <CollapsibleContent className="flex min-h-0 flex-1 flex-col data-[state=closed]:hidden">{conversations}</CollapsibleContent>
                </aside>
            </Collapsible>

            <div className="flex min-w-0 flex-1 flex-col overflow-hidden bg-background">{children}</div>

            <Collapsible open={rightOpen} onOpenChange={handleRightOpenChange} className="shrink-0">
                <aside className={`flex flex-col overflow-hidden border-l bg-muted/30 transition-[width] duration-200 ${rightOpen ? "w-[320px]" : "w-12"}`}>
                    <CollapsibleTrigger asChild>
                        <Button
                            variant="ghost"
                            size="icon"
                            className="m-1 ml-auto h-8 w-8 shrink-0"
                            aria-label={rightOpen ? t("chat.shell.collapseDriver") : t("chat.shell.expandDriver")}
                        >
                            {rightOpen ? <ChevronRight className="h-4 w-4" /> : <User className="h-4 w-4" />}
                        </Button>
                    </CollapsibleTrigger>
                    <CollapsibleContent className="flex min-h-0 flex-1 flex-col data-[state=closed]:hidden">{driverPanel}</CollapsibleContent>
                </aside>
            </Collapsible>

            {overlays}
        </div>
    );
}
