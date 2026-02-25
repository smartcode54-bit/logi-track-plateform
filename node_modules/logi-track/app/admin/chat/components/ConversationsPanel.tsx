"use client";

import { useState, useEffect, useMemo } from "react";
import Link from "next/link";
import {
  collection,
  query,
  where,
  orderBy,
  onSnapshot,
  getDocs,
  limit,
  Timestamp,
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useAuth } from "@/context/auth";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Loader2, MessageCircle, Inbox, User, Megaphone, Search } from "lucide-react";
import type { ChatStatus, ChatPriority, MessageType } from "@/lib/chat";

export interface DriverSearchItem {
  id: string;
  authId: string;
  firstName: string;
  lastName: string;
  mobile?: string;
  /** เลขใบอนุญาตขับรถ (8 หลัก) */
  truckLicenseId?: string;
  /** ทะเบียนรถ (จาก currentAssignment) */
  truckPlate?: string;
}

export interface ChatListItem {
  id: string;
  driverId: string;
  driverDisplayName: string;
  status: ChatStatus;
  assignedAdminId: string | null;
  priority: ChatPriority;
  lastMessage: string;
  lastMessageAt: Timestamp;
  lastMessageBy: string;
  lastMessageType?: MessageType;
}

function formatTime(ts: Timestamp): string {
  const d = ts?.toDate?.();
  if (!d) return "";
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  if (sameDay) return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return d.toLocaleDateString([], { month: "short", day: "numeric" });
}

const DRIVER_SEARCH_LIMIT = 200;
const DRIVER_SEARCH_RESULTS_SHOW = 8;

export function ConversationsPanel() {
  const auth = useAuth();
  const currentUser = auth?.currentUser ?? null;
  const [queued, setQueued] = useState<ChatListItem[]>([]);
  const [myChats, setMyChats] = useState<ChatListItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [driverSearchQuery, setDriverSearchQuery] = useState("");
  const [allDrivers, setAllDrivers] = useState<DriverSearchItem[]>([]);
  const [driversLoadDone, setDriversLoadDone] = useState(false);

  // Fetch drivers for search (only those with authId can be chatted)
  useEffect(() => {
    let cancelled = false;
    getDocs(
      query(collection(db, COLLECTIONS.DRIVERS), limit(DRIVER_SEARCH_LIMIT))
    )
      .then((snap) => {
        if (cancelled) return;
        const list: DriverSearchItem[] = [];
        snap.forEach((doc) => {
          const d = doc.data();
          const authId = d.authId as string | undefined;
          if (!authId) return;
          const currentAssignment = d.currentAssignment as { truckPlate?: string } | undefined;
          list.push({
            id: doc.id,
            authId,
            firstName: d.firstName ?? "",
            lastName: d.lastName ?? "",
            mobile: d.mobile ?? undefined,
            truckLicenseId: d.truckLicenseId ?? undefined,
            truckPlate: currentAssignment?.truckPlate ?? undefined,
          });
        });
        setAllDrivers(list);
        setDriversLoadDone(true);
      })
      .catch(() => {
        if (!cancelled) setDriversLoadDone(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const driverSearchResults = useMemo(() => {
    const q = driverSearchQuery.trim().toLowerCase();
    if (!q) return [];
    return allDrivers
      .filter(
        (d) =>
          `${d.firstName} ${d.lastName}`.toLowerCase().includes(q) ||
          d.firstName?.toLowerCase().includes(q) ||
          d.lastName?.toLowerCase().includes(q) ||
          d.mobile?.toLowerCase().includes(q) ||
          d.truckLicenseId?.toLowerCase().includes(q) ||
          d.truckPlate?.toLowerCase().includes(q)
      )
      .slice(0, DRIVER_SEARCH_RESULTS_SHOW);
  }, [allDrivers, driverSearchQuery]);

  useEffect(() => {
    if (!currentUser?.uid) return;

    const unsubQueued = onSnapshot(
      query(
        collection(db, COLLECTIONS.CHATS),
        where("assignedAdminId", "==", null),
        orderBy("lastMessageAt", "desc")
      ),
      (snap) => {
        const list: ChatListItem[] = [];
        snap.forEach((doc) => {
          const d = doc.data();
          if (d.status === "closed") return;
          list.push({
            id: doc.id,
            driverId: d.driverId,
            driverDisplayName: d.driverDisplayName ?? "Driver",
            status: d.status ?? "open",
            assignedAdminId: d.assignedAdminId ?? null,
            priority: d.priority ?? "normal",
            lastMessage: d.lastMessage ?? "",
            lastMessageAt: d.lastMessageAt,
            lastMessageBy: d.lastMessageBy ?? "",
            lastMessageType: (d.lastMessageType as "normal" | "broadcast" | undefined) ?? "normal",
          });
        });
        setQueued(list);
      },
      (err) => {
        console.error("Queued chats error:", err);
        setQueued([]);
      }
    );

    const unsubMyChats = onSnapshot(
      query(
        collection(db, COLLECTIONS.CHATS),
        where("assignedAdminId", "==", currentUser.uid),
        orderBy("lastMessageAt", "desc")
      ),
      (snap) => {
        const list: ChatListItem[] = [];
        snap.forEach((doc) => {
          const d = doc.data();
          if (d.status === "closed") return;
          list.push({
            id: doc.id,
            driverId: d.driverId,
            driverDisplayName: d.driverDisplayName ?? "Driver",
            status: d.status ?? "in_progress",
            assignedAdminId: d.assignedAdminId ?? null,
            priority: d.priority ?? "normal",
            lastMessage: d.lastMessage ?? "",
            lastMessageAt: d.lastMessageAt,
            lastMessageBy: d.lastMessageBy ?? "",
            lastMessageType: (d.lastMessageType as "normal" | "broadcast" | undefined) ?? "normal",
          });
        });
        setMyChats(list);
      },
      (err) => {
        console.error("My Chats error:", err);
        setMyChats([]);
      }
    );

    setLoading(false);
    return () => {
      unsubQueued();
      unsubMyChats();
    };
  }, [currentUser?.uid]);

  const renderList = (items: ChatListItem[]) => {
    if (items.length === 0)
      return (
        <div className="text-center py-12 text-muted-foreground">
          <MessageCircle className="h-12 w-12 mx-auto mb-2 opacity-50" />
          <p>No chats</p>
        </div>
      );
    return (
      <ul className="divide-y divide-border">
        {items.map((c) => (
          <li key={c.id}>
            <Link
              href={`/admin/chat/room?chatId=${c.id}`}
              className="flex items-center gap-4 p-4 hover:bg-muted/50 transition-colors"
            >
              <div className="h-10 w-10 rounded-full bg-muted flex items-center justify-center">
                <User className="h-5 w-5 text-muted-foreground" />
              </div>
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="font-medium truncate">{c.driverDisplayName}</span>
                  {c.priority === "urgent" && (
                    <Badge variant="destructive" className="shrink-0">Urgent</Badge>
                  )}
                </div>
                <p className="text-sm text-muted-foreground truncate">
                  {c.lastMessageType === "broadcast" && <span aria-hidden>📢 </span>}
                  {c.lastMessage || "No messages yet"}
                </p>
              </div>
              <span className="text-xs text-muted-foreground shrink-0">{formatTime(c.lastMessageAt)}</span>
            </Link>
          </li>
        ))}
      </ul>
    );
  };

  return (
    <div className="flex flex-col h-full">
      <div className="p-3 border-b space-y-2">
        <h2 className="font-semibold text-sm">Conversations</h2>
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            type="search"
            placeholder="ค้นหาชื่อ หรือทะเบียน..."
            value={driverSearchQuery}
            onChange={(e) => setDriverSearchQuery(e.target.value)}
            className="pl-8 h-9 text-sm"
          />
          {driverSearchQuery.trim() && (
            <ul className="absolute top-full left-0 right-0 mt-1 rounded-md border bg-popover shadow-md z-50 max-h-[240px] overflow-y-auto">
              {!driversLoadDone ? (
                <li className="p-3 text-sm text-muted-foreground flex items-center gap-2">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  Loading drivers...
                </li>
              ) : driverSearchResults.length === 0 ? (
                <li className="p-3 text-sm text-muted-foreground">No driver found</li>
              ) : (
                driverSearchResults.map((d) => (
                  <li key={d.id}>
                    <Link
                      href={`/admin/chat/with-driver?authId=${encodeURIComponent(d.authId)}`}
                      className="flex items-center justify-between gap-2 p-3 hover:bg-muted/80 transition-colors text-left"
                    >
                      <div className="min-w-0 flex-1">
                        <span className="font-medium truncate block">
                          {d.firstName} {d.lastName}
                        </span>
                        {(d.truckPlate || d.truckLicenseId) && (
                          <span className="text-xs text-muted-foreground font-mono">
                            {[d.truckPlate, d.truckLicenseId].filter(Boolean).join(" · ")}
                          </span>
                        )}
                      </div>
                      <span className="text-xs text-muted-foreground shrink-0">Start chat</span>
                    </Link>
                  </li>
                ))
              )}
            </ul>
          )}
        </div>
        <Button variant="default" size="sm" className="w-full gap-2" asChild>
          <Link href="/admin/chat?view=broadcast">
            <Megaphone className="h-4 w-4" />
            New Broadcast
          </Link>
        </Button>
      </div>
      <Tabs defaultValue="queued" className="flex-1 flex flex-col min-h-0">
        <TabsList className="grid grid-cols-2 w-full shrink-0 mx-2 mt-2">
          <TabsTrigger value="queued" className="gap-1 text-xs">
            <Inbox className="h-3.5 w-3.5" />
            Queued
            {queued.length > 0 && (
              <Badge variant="secondary" className="ml-1 text-xs px-1">{queued.length}</Badge>
            )}
          </TabsTrigger>
          <TabsTrigger value="mychats" className="gap-1 text-xs">
            <MessageCircle className="h-3.5 w-3.5" />
            My Chats
            {myChats.length > 0 && (
              <Badge variant="secondary" className="ml-1 text-xs px-1">{myChats.length}</Badge>
            )}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="queued" className="flex-1 mt-2 min-h-0 data-[state=inactive]:hidden">
          <Card className="h-full flex flex-col border-0 shadow-none rounded-none">
            <CardHeader className="py-2 px-3">
              <CardTitle className="text-xs font-medium text-muted-foreground">Unassigned (reply to take)</CardTitle>
            </CardHeader>
            <CardContent className="p-0 flex-1 overflow-y-auto min-h-0">
              {loading ? (
                <div className="flex justify-center py-8">
                  <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                </div>
              ) : (
                renderList(queued)
              )}
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="mychats" className="flex-1 mt-2 min-h-0 data-[state=inactive]:hidden">
          <Card className="h-full flex flex-col border-0 shadow-none rounded-none">
            <CardHeader className="py-2 px-3">
              <CardTitle className="text-xs font-medium text-muted-foreground">Assigned to you</CardTitle>
            </CardHeader>
            <CardContent className="p-0 flex-1 overflow-y-auto min-h-0">
              {loading ? (
                <div className="flex justify-center py-8">
                  <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                </div>
              ) : (
                renderList(myChats)
              )}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}
