"use client";

/**
 * Opens a driver's chat over Go (T50; Appendix E §E.2.1 `/app/chat/with-driver`): one `POST /v1/chats
 * {driverId}` answers the driver's chat that is not closed or a new one (one open chat per driver, R14), then
 * the room, instead of three sequential Firestore reads and a browser `addDoc`. The link names the driver by
 * its PostgreSQL id (`?driverId=`; the drivers list and the conversations search pass it). While Go writes
 * chats back to Firestore a new chat may answer `202` before the mirror has its row: the page asks again.
 */
import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";

import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { queryKeys } from "@/lib/queryKeys";
import { Button } from "@/components/ui/button";

import { isGoId, isWriteBackPending, openChatWithDriver } from "../api/chats.go";

/** Attempts while a written-back chat is not mirrored yet, and the pause between them. */
export const OPEN_ATTEMPTS = 5;
export const OPEN_RETRY_MS = 1_500;

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

export function ChatWithDriverGo() {
    const router = useRouter();
    const client = useQueryClient();
    const { t } = useLanguage();
    const searchParams = useSearchParams();
    const driverId = searchParams.get("driverId") ?? "";
    const legacyOnly = driverId === "" && (searchParams.get("authId") ?? "") !== "";
    const [error, setError] = useState<unknown>(null);
    const [pendingTooLong, setPendingTooLong] = useState(false);
    const started = useRef<string | null>(null);

    useEffect(() => {
        if (!isGoId(driverId)) {
            if (!legacyOnly) router.replace("/app/chat");
            return;
        }
        if (started.current === driverId) return;
        started.current = driverId;
        let cancelled = false;
        void (async () => {
            try {
                for (let attempt = 1; attempt <= OPEN_ATTEMPTS && !cancelled; attempt++) {
                    const res = await openChatWithDriver(driverId);
                    const id = res.id;
                    // A written-back chat the mirror has not loaded yet has no PostgreSQL id: ask again.
                    if (!isWriteBackPending(res) || isGoId(id)) {
                        void client.invalidateQueries({ queryKey: queryKeys.chats.all() });
                        if (!cancelled) router.replace(`/app/chat/room?chatId=${encodeURIComponent(id)}`);
                        return;
                    }
                    if (attempt < OPEN_ATTEMPTS) await sleep(OPEN_RETRY_MS);
                }
                if (!cancelled) setPendingTooLong(true);
            } catch (e) {
                if (!cancelled) setError(e);
            }
        })();
        return () => {
            cancelled = true;
            started.current = null;
        };
    }, [driverId, legacyOnly, router, client]);

    if (legacyOnly || error !== null || pendingTooLong) {
        const text = legacyOnly ? t("chat.withDriver.legacyLink") : pendingTooLong ? t("chat.withDriver.pending") : apiErrorText(error, t);
        return (
            <div className="flex min-h-[50vh] flex-col items-center justify-center gap-3 p-8 text-center text-sm text-muted-foreground" data-testid="chat-open-error">
                <p>{text}</p>
                <Button variant="outline" size="sm" asChild>
                    <Link href="/app/chat" prefetch={false}>
                        {t("chat.room.backToConsole")}
                    </Link>
                </Button>
            </div>
        );
    }
    return (
        <div className="flex min-h-[50vh] items-center justify-center gap-2" role="status" aria-busy="true">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            <span className="text-muted-foreground">{t("chat.withDriver.opening")}</span>
        </div>
    );
}
