"use client";

import { useState, useEffect } from "react";
import { usePathname, useSearchParams } from "next/navigation";
import { doc, getDoc } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { ChevronLeft, ChevronRight, MessageCircle, User } from "lucide-react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { useIsLg } from "@/hooks/use-mobile";
import { ConversationsPanel } from "./components/ConversationsPanel";
import { DriverProfileSidebar } from "./components/DriverProfileSidebar";
import { ReportIncidentModal, type ReportIncidentContext } from "./components/ReportIncidentModal";

const LEFT_OPEN_KEY = "logitrack_chat_left_open";
const RIGHT_OPEN_KEY = "logitrack_chat_right_open";

function getStored(key: string, fallback: boolean): boolean {
  if (typeof window === "undefined") return fallback;
  try {
    const v = localStorage.getItem(key);
    if (v === null) return fallback;
    return v === "true";
  } catch {
    return fallback;
  }
}

function setStored(key: string, value: boolean) {
  try {
    localStorage.setItem(key, String(value));
  } catch {
    // ignore
  }
}

export default function ChatLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const [leftOpen, setLeftOpen] = useState(true);
  const [rightOpen, setRightOpen] = useState(true);
  const [mounted, setMounted] = useState(false);
  const [conversationsSheetOpen, setConversationsSheetOpen] = useState(false);
  const [driverSheetOpen, setDriverSheetOpen] = useState(false);
  const [activeDriverId, setActiveDriverId] = useState<string | null>(null);
  const [reportIncidentOpen, setReportIncidentOpen] = useState(false);
  const [reportIncidentContext, setReportIncidentContext] = useState<ReportIncidentContext | null>(null);
  const isLg = useIsLg();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const chatIdFromUrl = pathname?.includes("/room") ? searchParams?.get("chatId") ?? null : null;

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
      <div className="flex h-[calc(100vh-4rem)] w-full overflow-hidden items-center justify-center">
        <div className="animate-pulse text-muted-foreground text-sm">Loading...</div>
      </div>
    );
  }

  // Mobile/tablet: single column + Sheets for conversations and driver panel
  if (!isLg) {
    return (
      <div className="flex flex-col h-[calc(100vh-4rem)] w-full overflow-hidden">
        <div className="shrink-0 flex items-center gap-2 border-b bg-muted/30 px-3 py-2">
          <Sheet open={conversationsSheetOpen} onOpenChange={setConversationsSheetOpen}>
            <SheetTrigger asChild>
              <Button variant="outline" size="sm" className="gap-1.5">
                <MessageCircle className="h-4 w-4" />
                Conversations
              </Button>
            </SheetTrigger>
            <SheetContent side="left" className="w-full max-w-[320px] p-0 flex flex-col">
              <SheetHeader className="p-3 border-b">
                <SheetTitle>Conversations</SheetTitle>
              </SheetHeader>
              <div className="flex-1 overflow-hidden">
                <ConversationsPanel />
              </div>
            </SheetContent>
          </Sheet>
          <Sheet open={driverSheetOpen} onOpenChange={setDriverSheetOpen}>
            <SheetTrigger asChild>
              <Button variant="outline" size="sm" className="gap-1.5">
                <User className="h-4 w-4" />
                Driver
              </Button>
            </SheetTrigger>
            <SheetContent side="right" className="w-full max-w-[320px] flex flex-col">
              <SheetHeader>
                <SheetTitle>Driver & assignment</SheetTitle>
              </SheetHeader>
              <div className="flex-1 min-h-0 overflow-hidden">
                <DriverProfileSidebar activeDriverId={activeDriverId} onReportIncident={handleReportIncident} />
              </div>
            </SheetContent>
          </Sheet>
        </div>
        <main className="flex-1 min-w-0 flex flex-col overflow-hidden bg-background">
          {children}
        </main>

        <ReportIncidentModal
          open={reportIncidentOpen}
          onOpenChange={setReportIncidentOpen}
          context={reportIncidentContext}
        />
      </div>
    );
  }

  // Desktop: 3-column with collapsible sidebars
  return (
    <div className="flex h-[calc(100vh-4rem)] w-full overflow-hidden">
      {/* Left: Conversations (collapsible) */}
      <Collapsible open={leftOpen} onOpenChange={handleLeftOpenChange} className="shrink-0">
        <aside
          className={`border-r bg-muted/30 flex flex-col overflow-hidden transition-[width] duration-200 ${
            leftOpen ? "w-[280px]" : "w-12"
          }`}
        >
          <CollapsibleTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="shrink-0 m-1 h-8 w-8"
              aria-label={leftOpen ? "Collapse conversations" : "Expand conversations"}
            >
              {leftOpen ? <ChevronLeft className="h-4 w-4" /> : <MessageCircle className="h-4 w-4" />}
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent className="flex-1 flex flex-col min-h-0 data-[state=closed]:hidden">
            <ConversationsPanel />
          </CollapsibleContent>
        </aside>
      </Collapsible>

      {/* Center: Room or empty state */}
      <main className="flex-1 min-w-0 flex flex-col overflow-hidden bg-background">
        {children}
      </main>

      {/* Right: Driver profile (collapsible) */}
      <Collapsible open={rightOpen} onOpenChange={handleRightOpenChange} className="shrink-0">
        <aside
          className={`border-l bg-muted/30 flex flex-col overflow-hidden transition-[width] duration-200 ${
            rightOpen ? "w-[320px]" : "w-12"
          }`}
        >
          <CollapsibleTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="shrink-0 m-1 h-8 w-8 ml-auto"
              aria-label={rightOpen ? "Collapse driver panel" : "Expand driver panel"}
            >
              {rightOpen ? <ChevronRight className="h-4 w-4" /> : <User className="h-4 w-4" />}
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent className="flex-1 flex flex-col min-h-0 data-[state=closed]:hidden">
            <DriverProfileSidebar activeDriverId={activeDriverId} onReportIncident={handleReportIncident} />
          </CollapsibleContent>
        </aside>
      </Collapsible>

      <ReportIncidentModal
        open={reportIncidentOpen}
        onOpenChange={setReportIncidentOpen}
        context={reportIncidentContext}
      />
    </div>
  );
}
