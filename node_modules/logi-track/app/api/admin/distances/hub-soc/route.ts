import { NextResponse } from "next/server";
import { db } from "@/firebase/client";
import { computeAndSaveHubSocDistances } from "@/lib/hubSocDistances";

/**
 * POST /api/admin/distances/hub-soc
 * Fetches Hub and SOC points from Firestore, calls Google Distance Matrix API in batches,
 * and writes distance/duration to hub_soc_distances. Use on-demand (max ~4 runs/month).
 * Requires GOOGLE_MAPS_API_KEY in env (server-only).
 */
export async function POST(request: Request) {
    const apiKey = process.env.GOOGLE_MAPS_API_KEY;
    if (!apiKey) {
        return NextResponse.json(
            { error: "GOOGLE_MAPS_API_KEY is not set" },
            { status: 500 }
        );
    }
    let userId: string | null = null;
    try {
        const body = await request.json().catch(() => ({}));
        userId = typeof body?.userId === "string" ? body.userId : null;
    } catch {
        // no body or invalid JSON
    }
    try {
        const result = await computeAndSaveHubSocDistances(db, apiKey, userId);
        if (result.error) {
            return NextResponse.json(
                { error: result.error, written: 0, hubsCount: result.hubsCount, socsCount: result.socsCount },
                { status: 400 }
            );
        }
        return NextResponse.json({
            ok: true,
            written: result.written,
            hubsCount: result.hubsCount,
            socsCount: result.socsCount,
        });
    } catch (e) {
        const message = e instanceof Error ? e.message : String(e);
        console.error("Hub-SOC distance computation failed:", e);
        return NextResponse.json(
            { error: message },
            { status: 500 }
        );
    }
}
