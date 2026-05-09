"use client";

import { useState, useEffect, useRef, useMemo, Suspense, type ReactNode } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import {
  doc,
  getDoc,
  collection,
  query,
  orderBy,
  onSnapshot,
  addDoc,
  updateDoc,
  serverTimestamp,
  Timestamp,
  type DocumentData,
  type UpdateData,
} from "firebase/firestore";
import { db, functions } from "@/firebase/client";
import { httpsCallable } from "firebase/functions";
import { COLLECTIONS } from "@/lib/collections";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Loader2, ArrowLeft, Send, User, CheckCircle, RotateCcw } from "lucide-react";
import type { MessageDoc, ChatStatus } from "@/lib/chat";

function startOfLocalDay(d: Date): Date {
  const x = new Date(d);
  x.setHours(0, 0, 0, 0);
  return x;
}

function isSameCalendarDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );
}

function AdminChatRoomContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const chatId = searchParams.get("chatId") ?? "";
  const auth = useAuth();
  const { t, language } = useLanguage();
  const currentUser = auth?.currentUser ?? null;
  const [chat, setChat] = useState<{
    driverDisplayName: string;
    status: ChatStatus;
    assignedAdminId: string | null;
    lastReadByDriver?: Timestamp;
    lastReadByAdmin?: Record<string, Timestamp>;
  } | null>(null);
  const [messages, setMessages] = useState<(MessageDoc & { id: string })[]>([]);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [actionLoading, setActionLoading] = useState(false);
  /** lastReadByAdmin[uid] (ms) ก่อนเข้าห้อง — ใช้คั่น “ยังไม่ได้อ่าน” แบบ LINE */
  const [readBaselineMs, setReadBaselineMs] = useState<number | null>(null);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!chatId || !currentUser?.uid) {
      if (!chatId) router.replace("/app/chat");
      return;
    }

    const chatRef = doc(db, COLLECTIONS.CHATS, chatId);
    const unsubChat = onSnapshot(chatRef, (snap) => {
      if (!snap.exists()) {
        router.replace("/app/chat");
        return;
      }
      const d = snap.data();
      setChat({
        driverDisplayName: d.driverDisplayName ?? "Driver",
        status: d.status ?? "open",
        assignedAdminId: d.assignedAdminId ?? null,
        lastReadByDriver: d.lastReadByDriver as Timestamp | undefined,
        lastReadByAdmin: (d.lastReadByAdmin as Record<string, Timestamp>) ?? undefined,
      });
    });

    const messagesRef = collection(db, COLLECTIONS.CHATS, chatId, "messages");
    const unsub = onSnapshot(
      query(messagesRef, orderBy("createdAt", "asc")),
      (snap) => {
        const list: (MessageDoc & { id: string })[] = [];
        snap.forEach((doc) => {
          const d = doc.data();
          list.push({
            id: doc.id,
            senderId: d.senderId,
            senderRole: d.senderRole,
            text: d.text ?? "",
            createdAt: d.createdAt,
            imageUrl: d.imageUrl,
            type: (d.type as "normal" | "broadcast" | undefined) ?? "normal",
          });
        });
        setMessages(list);
        // เมื่อข้อความล่าสุดมาจากคนขับ = แอดมินกำลังดูอยู่ → อัปเดต lastReadByAdmin ทันที เพื่อให้มือถือขึ้น "อ่าน" แบบ realtime
        if (list.length > 0 && currentUser?.uid && list[list.length - 1].senderRole === "driver") {
          updateDoc(chatRef, {
            [`lastReadByAdmin.${currentUser.uid}`]: serverTimestamp(),
            updatedAt: serverTimestamp(),
          }).catch(() => {});
        }
      },
      (err) => console.error("Messages snapshot error:", err)
    );

    return () => {
      unsubChat();
      unsub();
    };
  }, [chatId, currentUser?.uid, router]);

  useEffect(() => {
    if (!chatId || !currentUser?.uid) return;
    let cancelled = false;
    const chatRef = doc(db, COLLECTIONS.CHATS, chatId);
    (async () => {
      try {
        const snap = await getDoc(chatRef);
        if (cancelled || !snap.exists()) return;
        const d = snap.data();
        const ms =
          (d.lastReadByAdmin as Record<string, Timestamp> | undefined)?.[currentUser.uid]?.toMillis?.() ?? 0;
        setReadBaselineMs(ms);
      } catch {
        if (!cancelled) setReadBaselineMs(0);
      }
      if (cancelled) return;
      await updateDoc(chatRef, {
        [`lastReadByAdmin.${currentUser.uid}`]: serverTimestamp(),
        updatedAt: serverTimestamp(),
      }).catch(() => {});
    })();
    return () => {
      cancelled = true;
    };
  }, [chatId, currentUser?.uid]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  const sendMessage = async () => {
    const text = input.trim();
    if (!text || !currentUser?.uid || !chatId) return;
    setSending(true);
    setInput("");

    const chatRef = doc(db, COLLECTIONS.CHATS, chatId);
    const messagesRef = collection(db, COLLECTIONS.CHATS, chatId, "messages");

    try {
      const isFirstReply = chat?.assignedAdminId == null;
      const messageRef = await addDoc(messagesRef, {
        senderId: currentUser.uid,
        senderRole: "admin",
        text,
        createdAt: serverTimestamp(),
      });
      // Notify driver (FCM) and run auto-assign; Firestore trigger not used (DB in asia-southeast3).
      const notifyChatMessageCreated = httpsCallable<{ chatId: string; messageId: string }, { ok: boolean }>(
        functions,
        "notifyChatMessageCreated"
      );
      await notifyChatMessageCreated({ chatId, messageId: messageRef.id });
      const updates: UpdateData<DocumentData> = {
        lastMessage: text,
        lastMessageAt: serverTimestamp(),
        lastMessageBy: currentUser.uid,
        updatedAt: serverTimestamp(),
        [`lastReadByAdmin.${currentUser.uid}`]: serverTimestamp(),
      };
      if (isFirstReply) {
        updates.assignedAdminId = currentUser.uid;
        updates.status = "in_progress";
        updates.assignedAt = serverTimestamp();
      }
      // Reopen chat when admin sends a message after it was closed
      if (chat?.status === "closed") {
        updates.status = "in_progress";
        updates.closedAt = null;
      }
      await updateDoc(chatRef, updates);
      setChat((prev) =>
        prev
          ? {
              ...prev,
              status: isFirstReply || prev.status === "closed" ? "in_progress" : prev.status,
              assignedAdminId: isFirstReply ? currentUser.uid : prev.assignedAdminId,
            }
          : null
      );
    } catch (e) {
      console.error("Send message error:", e);
      setInput(text);
    } finally {
      setSending(false);
    }
  };

  const closeCase = async () => {
    if (!chatId || !currentUser?.uid) return;
    setActionLoading(true);
    try {
      await updateDoc(doc(db, COLLECTIONS.CHATS, chatId), {
        status: "closed",
        closedAt: serverTimestamp(),
        updatedAt: serverTimestamp(),
      });
      setChat((prev) => (prev ? { ...prev, status: "closed" as const } : null));
      router.push("/app/chat");
    } catch (e) {
      console.error("Close case error:", e);
    } finally {
      setActionLoading(false);
    }
  };

  const returnToQueue = async () => {
    if (!chatId) return;
    setActionLoading(true);
    try {
      await updateDoc(doc(db, COLLECTIONS.CHATS, chatId), {
        status: "open",
        assignedAdminId: null,
        assignedAt: null,
        updatedAt: serverTimestamp(),
      });
      setChat((prev) =>
        prev ? { ...prev, status: "open", assignedAdminId: null } : null
      );
    } catch (e) {
      console.error("Return to queue error:", e);
    } finally {
      setActionLoading(false);
    }
  };

  const formatTime = (ts: Timestamp | undefined) => {
    const d = ts?.toDate?.();
    if (!d) return "";
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  };

  const isMessageReadByRecipient = (m: MessageDoc & { id: string }): boolean => {
    const msgTime = m.createdAt?.toMillis?.();
    if (msgTime == null) return false;
    if (m.senderRole === "admin") {
      const driverRead = chat?.lastReadByDriver?.toMillis?.();
      return driverRead != null && driverRead >= msgTime;
    }
    if (m.senderRole === "driver" && currentUser?.uid && chat?.lastReadByAdmin) {
      const adminRead = chat.lastReadByAdmin[currentUser.uid]?.toMillis?.();
      return adminRead != null && adminRead >= msgTime;
    }
    return false;
  };

  const firstUnreadDriverIndex = useMemo(() => {
    if (readBaselineMs === null) return -1;
    return messages.findIndex((m) => {
      if (m.senderRole !== "driver") return false;
      const ms = m.createdAt?.toMillis?.();
      return ms != null && ms > readBaselineMs;
    });
  }, [messages, readBaselineMs]);

  const formatChatDateLabel = (d: Date) => {
    const now = new Date();
    const today = startOfLocalDay(now);
    const yesterday = new Date(today);
    yesterday.setDate(yesterday.getDate() - 1);
    const msgDay = startOfLocalDay(d);
    if (msgDay.getTime() === today.getTime()) return t("chat.dateToday");
    if (msgDay.getTime() === yesterday.getTime()) return t("chat.dateYesterday");
    return d.toLocaleDateString(language === "th" ? "th-TH" : "en-US", {
      weekday: "long",
      year: "numeric",
      month: "long",
      day: "numeric",
    });
  };

  if (!chatId) {
    return null;
  }

  if (!chat) {
    return (
      <div className="flex items-center justify-center min-h-[50vh]">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full min-h-0">
      <div className="flex items-center gap-4 p-4 border-b shrink-0">
        <Button variant="ghost" size="icon" asChild>
          <Link href="/app/chat" prefetch={false}>
            <ArrowLeft className="h-4 w-4" />
          </Link>
        </Button>
        <div className="h-10 w-10 rounded-full bg-muted flex items-center justify-center">
          <User className="h-5 w-5 text-muted-foreground" />
        </div>
        <div className="flex-1">
          <h1 className="font-semibold">{chat.driverDisplayName}</h1>
          <p className="text-sm text-muted-foreground capitalize">{chat.status.replace("_", " ")}</p>
        </div>
        {chat.status !== "closed" && (
          <div className="flex gap-2">
            {chat.assignedAdminId === currentUser?.uid && (
              <>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={returnToQueue}
                  disabled={actionLoading}
                  className="gap-1"
                >
                  <RotateCcw className="h-4 w-4" />
                  Return to Queue
                </Button>
                <Button
                  variant="default"
                  size="sm"
                  onClick={closeCase}
                  disabled={actionLoading}
                  className="gap-1"
                >
                  <CheckCircle className="h-4 w-4" />
                  Close Case
                </Button>
              </>
            )}
          </div>
        )}
      </div>

      <Card className="flex-1 flex flex-col min-h-0 mx-4 mb-4 rounded-lg">
        <CardContent className="flex-1 overflow-y-auto p-4 space-y-3 min-h-0">
          {messages.length === 0 && (
            <p className="text-center text-muted-foreground text-sm py-8">No messages yet. Send the first message to take this chat.</p>
          )}
          {messages.flatMap((m, index) => {
            const isAdmin = m.senderRole === "admin";
            const isBroadcast = m.type === "broadcast";
            const senderName = chat?.driverDisplayName ?? "Driver";
            const senderInitial = senderName.charAt(0).toUpperCase() || "?";
            const tCurr = m.createdAt?.toDate?.();
            const tPrev = index > 0 ? messages[index - 1].createdAt?.toDate?.() : undefined;
            const rows: ReactNode[] = [];
            if (tCurr && (!tPrev || !isSameCalendarDay(tPrev, tCurr))) {
              const label = formatChatDateLabel(tCurr);
              rows.push(
                <div
                  key={`date-${m.id}`}
                  className="flex justify-center py-2"
                  role="separator"
                  aria-label={label}
                >
                  <span className="text-xs text-muted-foreground bg-muted/80 px-3 py-1 rounded-full">
                    {label}
                  </span>
                </div>
              );
            }
            if (readBaselineMs !== null && firstUnreadDriverIndex === index) {
              rows.push(
                <div
                  key={`unread-${m.id}`}
                  className="flex items-center gap-3 py-2"
                  role="separator"
                  aria-label={t("chat.unreadDivider")}
                >
                  <div className="h-px flex-1 bg-primary/35" />
                  <span className="text-xs font-medium text-primary shrink-0">{t("chat.unreadDivider")}</span>
                  <div className="h-px flex-1 bg-primary/35" />
                </div>
              );
            }
            rows.push(
              <div
                key={m.id}
                className={`flex gap-2 ${isAdmin ? "justify-end" : "justify-start"}`}
              >
                {!isAdmin && (
                  <>
                    <div
                      className="shrink-0 w-8 h-8 rounded-full flex items-center justify-center text-sm font-medium bg-muted text-muted-foreground"
                      title={senderName}
                    >
                      {senderInitial}
                    </div>
                    <div className="flex flex-col max-w-[75%] items-start">
                      <span className="text-xs text-muted-foreground mb-0.5 px-1">{senderName}</span>
                      <div className="rounded-lg px-3 py-2 bg-muted">
                        <p className="text-sm whitespace-pre-wrap wrap-break-word">
                          {isBroadcast && <span className="mr-1" aria-hidden>📢 </span>}
                          {m.text}
                        </p>
                        {m.imageUrl && (
                          <a
                            href={m.imageUrl}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="block mt-1 text-xs underline"
                          >
                            View image
                          </a>
                        )}
                        <p className="text-xs mt-1 text-muted-foreground">
                          {formatTime(m.createdAt)}
                        </p>
                      </div>
                    </div>
                  </>
                )}
                {isAdmin && (
                  <div className="rounded-lg px-3 py-2 bg-primary text-primary-foreground max-w-[75%]">
                    <p className="text-sm whitespace-pre-wrap wrap-break-word">
                      {isBroadcast && <span className="mr-1" aria-hidden>📢 </span>}
                      {m.text}
                    </p>
                    {m.imageUrl && (
                      <a
                        href={m.imageUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="block mt-1 text-xs underline text-primary-foreground/90"
                      >
                        View image
                      </a>
                    )}
                    <p className="text-xs mt-1 text-primary-foreground/80">
                      {formatTime(m.createdAt)}
                      {isMessageReadByRecipient(m) && (
                        <span className="ml-1.5 opacity-90">· {t("chat.read")}</span>
                      )}
                    </p>
                  </div>
                )}
              </div>
            );
            return rows;
          })}
          <div ref={bottomRef} />
        </CardContent>
        <CardHeader className="border-t pt-4">
          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              sendMessage();
            }}
          >
            <Input
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder={chat.status === "closed" ? "Type a message to reopen chat..." : "Type a message..."}
              disabled={sending}
              className="flex-1"
            />
            <Button type="submit" disabled={sending || !input.trim()}>
              {sending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
            </Button>
          </form>
        </CardHeader>
      </Card>
    </div>
  );
}

export default function AdminChatRoomPage() {
  return (
    <Suspense fallback={<div className="flex items-center justify-center min-h-[50vh]"><Loader2 className="h-8 w-8 animate-spin text-muted-foreground" /></div>}>
      <AdminChatRoomContent />
    </Suspense>
  );
}
