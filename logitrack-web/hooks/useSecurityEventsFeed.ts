"use client";

import { useEffect, useState } from "react";
import {
    collection,
    limit,
    onSnapshot,
    orderBy,
    query,
    type Timestamp,
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";

export type SecurityEventRow = {
    id: string;
    createdAt: Timestamp | null;
    type: string;
    severity: string;
    summary: string;
    actorUid: string;
    actorEmail: string | null;
    details: Record<string, unknown>;
};

function mapDoc(id: string, data: Record<string, unknown>): SecurityEventRow {
    const createdAt = (data.createdAt as Timestamp | undefined) ?? null;
    return {
        id,
        createdAt,
        type: typeof data.type === "string" ? data.type : "unknown",
        severity: typeof data.severity === "string" ? data.severity : "info",
        summary: typeof data.summary === "string" ? data.summary : "",
        actorUid: typeof data.actorUid === "string" ? data.actorUid : "",
        actorEmail: typeof data.actorEmail === "string" ? data.actorEmail : null,
        details: data.details && typeof data.details === "object" && !Array.isArray(data.details)
            ? (data.details as Record<string, unknown>)
            : {},
    };
}

/**
 * Live feed of security_events (newest first). Caller should gate with audit permission.
 */
export function useSecurityEventsFeed(maxDocs: number, enabled: boolean) {
    const [rows, setRows] = useState<SecurityEventRow[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    useEffect(() => {
        if (!enabled) {
            setRows([]);
            setLoading(false);
            setError(null);
            return;
        }

        setLoading(true);
        setError(null);
        const q = query(
            collection(db, COLLECTIONS.SECURITY_EVENTS),
            orderBy("createdAt", "desc"),
            limit(maxDocs),
        );
        const unsub = onSnapshot(
            q,
            (snap) => {
                setRows(
                    snap.docs.map((d) => mapDoc(d.id, d.data() as Record<string, unknown>)),
                );
                setLoading(false);
            },
            (e) => {
                console.error("[useSecurityEventsFeed]", e);
                setError((e as Error).message || "listen failed");
                setLoading(false);
            },
        );
        return () => unsub();
    }, [maxDocs, enabled]);

    return { rows, loading, error };
}
