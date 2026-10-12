"use client";

/**
 * The chat console on Firestore (the `comms` web flag off; T50 keeps it until TW7): the shell with the legacy
 * conversations panel (two listeners) and driver panel, the room's driver read from its chat document.
 */
import { useState, useEffect } from "react";
import { usePathname, useSearchParams } from "next/navigation";
import { doc, getDoc } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { ChatShell } from "../components/ChatShell";
import { LegacyConversationsPanel } from "./LegacyConversationsPanel";
import { LegacyDriverProfileSidebar } from "./LegacyDriverProfileSidebar";
import { ReportIncidentModal, type ReportIncidentContext } from "@/app/app/chat/components/ReportIncidentModal";

export default function LegacyChatLayout({ children }: { children: React.ReactNode }) {
    const [activeDriverId, setActiveDriverId] = useState<string | null>(null);
    const [reportIncidentOpen, setReportIncidentOpen] = useState(false);
    const [reportIncidentContext, setReportIncidentContext] = useState<ReportIncidentContext | null>(null);
    const pathname = usePathname();
    const searchParams = useSearchParams();
    const chatIdFromUrl = pathname?.includes("/room") ? (searchParams?.get("chatId") ?? null) : null;

    useEffect(() => {
        if (!chatIdFromUrl) {
            setActiveDriverId(null);
            return;
        }
        let cancelled = false;
        getDoc(doc(db, COLLECTIONS.CHATS, chatIdFromUrl))
            .then((snap) => {
                if (cancelled || !snap.exists()) {
                    if (!cancelled) setActiveDriverId(null);
                    return;
                }
                const driverId = (snap.data() as { driverId?: string }).driverId ?? null;
                if (!cancelled) setActiveDriverId(driverId);
            })
            .catch(() => {
                if (!cancelled) setActiveDriverId(null);
            });
        return () => {
            cancelled = true;
        };
    }, [chatIdFromUrl]);

    const handleReportIncident = (context: ReportIncidentContext) => {
        setReportIncidentContext(context);
        setReportIncidentOpen(true);
    };

    return (
        <ChatShell
            conversations={<LegacyConversationsPanel />}
            driverPanel={<LegacyDriverProfileSidebar activeDriverId={activeDriverId} onReportIncident={handleReportIncident} />}
            overlays={<ReportIncidentModal open={reportIncidentOpen} onOpenChange={setReportIncidentOpen} context={reportIncidentContext} />}
        >
            {children}
        </ChatShell>
    );
}
