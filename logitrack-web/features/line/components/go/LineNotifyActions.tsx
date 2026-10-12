"use client";

/**
 * The LINE actions of a trip or a standby record over Go (T49): "send the LINE card" (`force`: resends a card already
 * sent, and replaces a revoked evidence link) and "revoke the evidence link" (R47). Shown to `operations:edit_trip_details`
 * only; the result of the card is the worker's job, reported as the legacy page reported the callable's answer.
 */
import { useEffect, useRef, useState } from "react";
import { Ban, Loader2, Send } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { Button } from "@/components/ui/button";

import { lineReasonKey, revokeEvidence, useLineNotify, type LineOutcome, type LineRecordKind } from "../../api/lineNotify.go";

export function LineNotifyActions({ kind, id, evidence = true }: { kind: LineRecordKind; id: string; evidence?: boolean }) {
    const { t } = useLanguage();
    const allowed = useCan("operations:edit_trip_details").allowed;
    const line = useLineNotify(kind, id);
    const [revoking, setRevoking] = useState(false);
    const reported = useRef<string | null>(null);

    useEffect(() => {
        if (!line.jobId || !line.outcome || reported.current === line.jobId) return;
        reported.current = line.jobId;
        toast[line.outcome.kind === "sent" ? "success" : line.outcome.kind === "skipped" ? "warning" : "error"](outcomeText(line.outcome, t));
    }, [line.jobId, line.outcome, t]);

    useEffect(() => {
        if (line.error) toast.error(`${t("lineNotify.failed")}: ${apiErrorText(line.error, t)}`);
    }, [line.error, t]);

    if (!allowed) return null;

    const revoke = async () => {
        if (kind === "tasks" || !window.confirm(t("lineNotify.revokeConfirm"))) return;
        setRevoking(true);
        try {
            await revokeEvidence(kind, id);
            toast.success(t("lineNotify.revoked"));
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setRevoking(false);
        }
    };

    return (
        <div className="flex flex-wrap items-center gap-2">
            <Button type="button" variant="outline" size="sm" disabled={line.running} onClick={() => void line.start(true)}
                title={t("lineNotify.sendHint")}>
                {line.running ? <Loader2 className="mr-1 h-4 w-4 animate-spin" /> : <Send className="mr-1 h-4 w-4" />}
                {line.running ? t("lineNotify.sending") : t("lineNotify.send")}
            </Button>
            {evidence && kind !== "tasks" ? (
                <Button type="button" variant="ghost" size="sm" disabled={revoking} onClick={() => void revoke()}
                    title={t("lineNotify.revokeHint")}>
                    {revoking ? <Loader2 className="mr-1 h-4 w-4 animate-spin" /> : <Ban className="mr-1 h-4 w-4" />}
                    {t("lineNotify.revoke")}
                </Button>
            ) : null}
        </div>
    );
}

/** The toast of a settled card, in the legacy page's words. */
export function outcomeText(o: LineOutcome, t: (key: string) => string): string {
    if (o.kind === "sent") return t("lineNotify.sent");
    const key = lineReasonKey(o.reason);
    if (o.kind === "skipped") return key ? t(key) : `${t("lineNotify.skipGeneric")}${o.reason ? `: ${o.reason}` : ""}`;
    return `${t("lineNotify.failed")}${o.reason ? `: ${o.reason}` : ""}`;
}
