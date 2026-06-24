import { useEffect, useState } from "react";
import { collection, getDocs, query, where } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { driverDisplayName } from "@/lib/driverName";

/**
 * Resolve driver Auth UIDs → Thai display names.
 *
 * Used to show helper names (tasks.helperDriverIds stores Auth UIDs, set at
 * check-in). Returns a map authId → name; unresolved ids fall back to the id.
 */
export function useDriverNamesByAuthId(
    authIds: string[] | null | undefined
): Record<string, string> {
    const [names, setNames] = useState<Record<string, string>>({});
    // Stable dependency key so the effect re-runs only when the set of ids changes.
    const key = Array.from(new Set((authIds ?? []).filter(Boolean))).sort().join(",");

    useEffect(() => {
        const ids = key ? key.split(",") : [];
        if (ids.length === 0) {
            setNames({});
            return;
        }
        let cancelled = false;
        (async () => {
            const result: Record<string, string> = {};
            // Firestore "in" supports up to 30 values; chunk to stay safe.
            for (let i = 0; i < ids.length; i += 30) {
                const chunk = ids.slice(i, i + 30);
                const snap = await getDocs(
                    query(collection(db, COLLECTIONS.DRIVERS), where("authId", "in", chunk))
                );
                snap.forEach((d) => {
                    const data = d.data();
                    const aid = String(data.authId ?? "");
                    if (aid) result[aid] = driverDisplayName(data, aid);
                });
            }
            if (!cancelled) setNames(result);
        })().catch(() => {
            if (!cancelled) setNames({});
        });
        return () => {
            cancelled = true;
        };
    }, [key]);

    return names;
}
