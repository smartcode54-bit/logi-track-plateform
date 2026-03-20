"use client";

import { useState, useEffect } from "react";
import {
  collection,
  doc,
  deleteDoc,
  getDocs,
  query,
  orderBy,
  limit,
  Timestamp,
} from "firebase/firestore";
import { db, functions } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { httpsCallable } from "firebase/functions";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Megaphone, Send, Loader2, Check, Ban } from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { displayBroadcastTitle } from "@/lib/broadcastDisplay";
import { BroadcastDeleteConfirmDialog } from "./BroadcastDeleteConfirmDialog";

const GROUP_LABELS: Record<string, string> = {
  all_driver: "All Driver",
  own_fleet: "Own Fleet",
  subcontractor: "Subcontractor",
};

function formatDate(ts: Timestamp | null): string {
  if (!ts?.toDate) return "—";
  const d = ts.toDate();
  return d.toLocaleString(undefined, {
    dateStyle: "short",
    timeStyle: "short",
  });
}

export type RecipientGroup = "all_driver" | "own_fleet" | "subcontractor";

interface RecipientCounts {
  allDriver: string[];
  ownFleet: string[];
  subcontractor: string[];
}

async function fetchRecipientGroups(): Promise<RecipientCounts> {
  const snap = await getDocs(collection(db, COLLECTIONS.DRIVERS));
  const allDriver: string[] = [];
  const ownFleet: string[] = [];
  const subcontractor: string[] = [];
  snap.docs.forEach((doc) => {
    const d = doc.data();
    const authId = (d.authId as string | undefined)?.trim();
    if (!authId) return;
    allDriver.push(authId);
    const employmentType = d.employmentType as string | undefined;
    if (employmentType === "SUBCONTRACTOR") {
      subcontractor.push(authId);
    } else {
      ownFleet.push(authId);
    }
  });
  return { allDriver, ownFleet, subcontractor };
}

export function BroadcastComposer() {
  const [counts, setCounts] = useState<RecipientCounts | null>(null);
  const [selectedGroup, setSelectedGroup] = useState<RecipientGroup>("all_driver");
  const [loadingCount, setLoadingCount] = useState(true);
  const [broadcastTitle, setBroadcastTitle] = useState("");
  const [messageText, setMessageText] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState(false);
  const [historyList, setHistoryList] = useState<{
    id: string;
    createdByName: string;
    title: string;
    messageText: string;
    recipientCount: number;
    readCount: number;
    recipientGroup: string;
    sentAt: Timestamp | null;
  }[]>([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [removingHistoryId, setRemovingHistoryId] = useState<string | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);
  /** True after user clicked Send with missing title/body (show field errors). */
  const [showSendValidation, setShowSendValidation] = useState(false);

  const recipientIds =
    counts === null
      ? []
      : selectedGroup === "all_driver"
        ? counts.allDriver
        : selectedGroup === "own_fleet"
          ? counts.ownFleet
          : counts.subcontractor;

  const titleTrimmed = broadcastTitle.trim();
  const bodyTrimmed = messageText.trim();
  const titleFilled = titleTrimmed.length > 0;
  const bodyFilled = bodyTrimmed.length > 0;

  useEffect(() => {
    let cancelled = false;
    setLoadingCount(true);
    fetchRecipientGroups()
      .then((data) => {
        if (!cancelled) setCounts(data);
      })
      .catch((e) => {
        if (!cancelled) setError("Failed to load drivers");
        console.error(e);
      })
      .finally(() => {
        if (!cancelled) setLoadingCount(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const fetchHistory = () => {
    setHistoryLoading(true);
    setHistoryError(null);
    const q = query(
      collection(db, COLLECTIONS.BROADCASTS),
      orderBy("sentAt", "desc"),
      limit(50)
    );
    getDocs(q)
      .then((snap) => {
        const items: typeof historyList = [];
        snap.docs.forEach((doc) => {
          const d = doc.data();
          items.push({
            id: doc.id,
            createdByName: (d.createdByName as string) ?? (d.createdBy as string) ?? "",
            title: (d.title as string) ?? "",
            messageText: (d.messageText as string) ?? "",
            recipientCount: (d.recipientCount as number) ?? 0,
            readCount: (d.readCount as number) ?? 0,
            recipientGroup: (d.recipientGroup as string) ?? "all_driver",
            sentAt: (d.sentAt as Timestamp) ?? null,
          });
        });
        setHistoryList(items);
      })
      .catch((e) => {
        setHistoryList([]);
        setHistoryError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => setHistoryLoading(false));
  };

  useEffect(() => {
    fetchHistory();
  }, [sent]);

  const executeDeleteBroadcast = async () => {
    const id = confirmDeleteId;
    if (!id) return;
    setRemovingHistoryId(id);
    try {
      await deleteDoc(doc(db, COLLECTIONS.BROADCASTS, id));
      setHistoryList((prev) => prev.filter((row) => row.id !== id));
      setConfirmDeleteId(null);
      toast.success("ลบรายการจากประวัติแล้ว");
    } catch (e) {
      console.error("delete broadcast:", e);
      toast.error(e instanceof Error ? e.message : "ลบรายการไม่สำเร็จ");
    } finally {
      setRemovingHistoryId(null);
    }
  };

  const handleSend = async () => {
    if (sending || recipientIds.length === 0) return;

    const title = titleTrimmed;
    const text = bodyTrimmed;
    if (!title || !text) {
      setShowSendValidation(true);
      setError(null);
      return;
    }

    setShowSendValidation(false);
    setError(null);
    setSending(true);
    try {
      const sendBroadcast = httpsCallable<
        {
          recipientDriverIds: string[];
          title: string;
          messageText: string;
          recipientGroup?: string;
        },
        { ok: boolean; recipientCount: number }
      >(functions, "sendBroadcast");
      const res = await sendBroadcast({
        recipientDriverIds: recipientIds,
        title,
        messageText: text,
        recipientGroup: selectedGroup,
      });
      const data = res.data as { ok?: boolean; recipientCount?: number };
      if (data?.ok) {
        setSent(true);
        setBroadcastTitle("");
        setMessageText("");
      } else {
        setError("Broadcast did not complete.");
      }
    } catch (e) {
      console.error("sendBroadcast error:", e);
      setError(
        e instanceof Error ? e.message : "Failed to send broadcast. Try again."
      );
    } finally {
      setSending(false);
    }
  };

  if (loadingCount) {
    return (
      <div className="flex flex-1 items-center justify-center p-8">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="flex flex-1 flex-col p-6 max-w-4xl mx-auto w-full">
      <div className="mb-6">
        <div className="flex items-center gap-2">
          <Megaphone className="h-6 w-6 text-primary" />
          <div>
            <h2 className="text-lg font-semibold">New Broadcast Message</h2>
            <p className="text-sm text-muted-foreground">
              Reach multiple drivers simultaneously.
            </p>
          </div>
        </div>
      </div>

      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void handleSend();
        }}
        noValidate
      >
        <div>
          <p className="text-sm font-medium text-muted-foreground mb-2">
            Recipients
          </p>
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
            <button
              type="button"
              onClick={() => setSelectedGroup("all_driver")}
              className={cn(
                "flex flex-col items-start p-3 rounded-lg border-2 text-left transition-colors",
                selectedGroup === "all_driver"
                  ? "border-primary bg-primary/5"
                  : "border-border hover:bg-muted/50"
              )}
            >
              <span className="font-medium text-sm">All Driver</span>
              <span className="text-xs text-muted-foreground mt-0.5">
                {counts?.allDriver.length ?? 0} active contacts
              </span>
              {selectedGroup === "all_driver" && (
                <Check className="h-4 w-4 text-primary mt-1" />
              )}
            </button>
            <button
              type="button"
              onClick={() => setSelectedGroup("own_fleet")}
              className={cn(
                "flex flex-col items-start p-3 rounded-lg border-2 text-left transition-colors",
                selectedGroup === "own_fleet"
                  ? "border-primary bg-primary/5"
                  : "border-border hover:bg-muted/50"
              )}
            >
              <span className="font-medium text-sm">Own Fleet</span>
              <span className="text-xs text-muted-foreground mt-0.5">
                {counts?.ownFleet.length ?? 0} drivers
              </span>
              {selectedGroup === "own_fleet" && (
                <Check className="h-4 w-4 text-primary mt-1" />
              )}
            </button>
            <button
              type="button"
              onClick={() => setSelectedGroup("subcontractor")}
              className={cn(
                "flex flex-col items-start p-3 rounded-lg border-2 text-left transition-colors",
                selectedGroup === "subcontractor"
                  ? "border-primary bg-primary/5"
                  : "border-border hover:bg-muted/50"
              )}
            >
              <span className="font-medium text-sm">Subcontractor</span>
              <span className="text-xs text-muted-foreground mt-0.5">
                {counts?.subcontractor.length ?? 0} verified partners
              </span>
              {selectedGroup === "subcontractor" && (
                <Check className="h-4 w-4 text-primary mt-1" />
              )}
            </button>
          </div>
          {recipientIds.length === 0 && (
            <p className="text-sm text-amber-600 dark:text-amber-500 mt-2">
              ไม่มีผู้รับในกลุ่มนี้ — ส่ง Broadcast ไม่ได้จนกว่าจะมีคนขับในระบบ
            </p>
          )}
        </div>

        <div>
          <label htmlFor="broadcast-title" className="text-sm font-medium mb-2 block">
            Subject / title <span className="text-destructive">*</span>
          </label>
          <Input
            id="broadcast-title"
            placeholder="e.g. Route change — Bangkok hub"
            value={broadcastTitle}
            onChange={(e) => setBroadcastTitle(e.target.value)}
            disabled={sending}
            maxLength={200}
            required
            aria-invalid={showSendValidation && !titleFilled}
            aria-describedby={
              showSendValidation && !titleFilled ? "broadcast-title-error" : undefined
            }
            className={cn(
              showSendValidation &&
                !titleFilled &&
                "border-destructive focus-visible:ring-destructive/40"
            )}
          />
          {showSendValidation && !titleFilled && (
            <p id="broadcast-title-error" className="text-sm text-destructive mt-1.5">
              กรุณากรอกชื่อเรื่องก่อนส่ง
            </p>
          )}
        </div>

        <div>
          <label htmlFor="broadcast-message" className="text-sm font-medium mb-2 block">
            Message body <span className="text-destructive">*</span>
          </label>
          <Textarea
            id="broadcast-message"
            placeholder="Full details for drivers..."
            value={messageText}
            onChange={(e) => setMessageText(e.target.value)}
            disabled={sending}
            rows={5}
            className={cn(
              "resize-none",
              showSendValidation &&
                !bodyFilled &&
                "border-destructive focus-visible:ring-destructive/40"
            )}
            required
            aria-invalid={showSendValidation && !bodyFilled}
            aria-describedby={
              showSendValidation && !bodyFilled ? "broadcast-message-error" : undefined
            }
          />
          {showSendValidation && !bodyFilled && (
            <p id="broadcast-message-error" className="text-sm text-destructive mt-1.5">
              กรุณากรอกข้อความก่อนส่ง
            </p>
          )}
        </div>

        {error && (
          <p className="text-sm text-destructive">{error}</p>
        )}

        {sent && (
          <p className="text-sm text-green-600 dark:text-green-400">
            Broadcast sent successfully.
          </p>
        )}

        <div className="flex items-center justify-between pt-2">
          <span className="text-sm text-muted-foreground">
            Total recipients: {recipientIds.length}
          </span>
          <Button
            type="submit"
            disabled={sending || recipientIds.length === 0}
            className="gap-2"
          >
            {sending ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Send className="h-4 w-4" />
            )}
            Send Broadcast Now
          </Button>
        </div>
      </form>

      <div className="mt-10 border-t pt-8">
        <h3 className="text-base font-semibold mb-4">ประวัติ Broadcast</h3>
        {historyLoading ? (
          <div className="flex justify-center py-12">
            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
          </div>
        ) : historyError ? (
          <div className="py-8 px-4 text-center space-y-2">
            <p className="text-sm text-destructive font-medium">โหลดประวัติไม่สำเร็จ</p>
            <p className="text-xs text-muted-foreground">{historyError}</p>
          </div>
        ) : historyList.length === 0 ? (
          <p className="text-sm text-muted-foreground py-8 text-center">
            ยังไม่มีประวัติการส่ง Broadcast
          </p>
        ) : (
          <div className="border rounded-md overflow-auto max-h-[50vh]">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="min-w-[160px] max-w-[220px]">หัวข้อ</TableHead>
                  <TableHead className="min-w-[200px]">ข้อความ</TableHead>
                  <TableHead className="w-[140px]">ผู้ส่ง</TableHead>
                  <TableHead className="w-[160px]">วันที่ส่ง</TableHead>
                  <TableHead className="w-[100px] text-right">จำนวนผู้รับ</TableHead>
                  <TableHead className="w-[88px] text-right">อ่านแล้ว</TableHead>
                  <TableHead className="w-[130px] text-right"> </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {historyList.map((item) => {
                  const head = displayBroadcastTitle(item.title);
                  return (
                  <TableRow key={item.id}>
                    <TableCell className="align-top py-3">
                      <div className="text-sm font-medium whitespace-pre-wrap break-words max-w-[220px]">
                        {head}
                      </div>
                    </TableCell>
                    <TableCell className="align-top py-3">
                      <div className="text-sm whitespace-pre-wrap break-words max-w-md">
                        {item.messageText || "—"}
                      </div>
                    </TableCell>
                    <TableCell className="align-top py-3 text-muted-foreground text-sm">
                      {item.createdByName || "—"}
                    </TableCell>
                    <TableCell className="align-top py-3 text-muted-foreground text-sm">
                      {formatDate(item.sentAt)}
                    </TableCell>
                    <TableCell className="align-top py-3 text-right text-sm">
                      {item.recipientCount}{" "}
                      <span className="text-muted-foreground">
                        ({GROUP_LABELS[item.recipientGroup] ?? item.recipientGroup})
                      </span>
                    </TableCell>
                    <TableCell className="align-top py-3 text-right text-sm tabular-nums">
                      {item.readCount}
                    </TableCell>
                    <TableCell className="align-top py-2 text-right">
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        className="text-destructive border-destructive/40 hover:bg-destructive/10"
                        disabled={removingHistoryId === item.id}
                        onClick={() => setConfirmDeleteId(item.id)}
                      >
                        {removingHistoryId === item.id ? (
                          <Loader2 className="h-4 w-4 animate-spin" />
                        ) : (
                          <>
                            <Ban className="h-4 w-4 mr-1" />
                            ยกเลิกส่ง
                          </>
                        )}
                      </Button>
                    </TableCell>
                  </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </div>

      <BroadcastDeleteConfirmDialog
        open={confirmDeleteId !== null}
        onOpenChange={(open) => {
          if (!open && removingHistoryId === null) setConfirmDeleteId(null);
        }}
        onConfirm={executeDeleteBroadcast}
        loading={
          confirmDeleteId !== null && removingHistoryId === confirmDeleteId
        }
      />
    </div>
  );
}
