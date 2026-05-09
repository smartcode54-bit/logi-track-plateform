"use client";

import { useState, useEffect } from "react";
import {
  collection,
  doc,
  deleteDoc,
  query,
  orderBy,
  limit,
  getDocs,
  Timestamp,
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Loader2, Megaphone, Ban } from "lucide-react";
import { toast } from "sonner";
import { displayBroadcastTitle } from "@/lib/broadcastDisplay";
import { BroadcastDeleteConfirmDialog } from "./BroadcastDeleteConfirmDialog";

export interface BroadcastRecord {
  id: string;
  createdBy: string;
  createdByName: string;
  title: string;
  messageText: string;
  recipientCount: number;
  readCount: number;
  recipientGroup: string;
  sentAt: Timestamp | null;
}

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

interface BroadcastHistoryModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function BroadcastHistoryModal({ open, onOpenChange }: BroadcastHistoryModalProps) {
  const [list, setList] = useState<BroadcastRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [removingId, setRemovingId] = useState<string | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);

  const executeDeleteBroadcast = async () => {
    const id = confirmDeleteId;
    if (!id) return;
    setRemovingId(id);
    try {
      await deleteDoc(doc(db, COLLECTIONS.BROADCASTS, id));
      setList((prev) => prev.filter((row) => row.id !== id));
      setConfirmDeleteId(null);
      toast.success("ลบรายการจากประวัติแล้ว");
    } catch (e) {
      console.error("delete broadcast:", e);
      toast.error(e instanceof Error ? e.message : "ลบรายการไม่สำเร็จ");
    } finally {
      setRemovingId(null);
    }
  };

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    const q = query(
      collection(db, COLLECTIONS.BROADCASTS),
      orderBy("sentAt", "desc"),
      limit(50)
    );
    getDocs(q)
      .then((snap) => {
        if (cancelled) return;
        const items: BroadcastRecord[] = [];
        snap.docs.forEach((docSnap) => {
          const d = docSnap.data();
          items.push({
            id: docSnap.id,
            createdBy: (d.createdBy as string) ?? "",
            createdByName: (d.createdByName as string) ?? (d.createdBy as string) ?? "",
            title: (d.title as string) ?? "",
            messageText: (d.messageText as string) ?? "",
            recipientCount: (d.recipientCount as number) ?? 0,
            readCount: (d.readCount as number) ?? 0,
            recipientGroup: (d.recipientGroup as string) ?? "all_driver",
            sentAt: (d.sentAt as Timestamp) ?? null,
          });
        });
        setList(items);
      })
      .catch((e) => {
        if (!cancelled) {
          setList([]);
          setError(e instanceof Error ? e.message : String(e));
        }
        console.error("Broadcast history error:", e);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open]);

  return (
    <>
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-4xl max-h-[85vh] flex flex-col">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Megaphone className="h-5 w-5" />
            ประวัติ Broadcast
          </DialogTitle>
        </DialogHeader>
        {loading ? (
          <div className="flex justify-center py-12">
            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
          </div>
        ) : error ? (
          <div className="py-8 px-4 text-center space-y-2">
            <p className="text-sm text-destructive font-medium">โหลดประวัติไม่สำเร็จ</p>
            <p className="text-xs text-muted-foreground">{error}</p>
            <p className="text-xs text-muted-foreground mt-2">
              ตรวจสอบว่าได้ deploy Firestore rules และมี collection &quot;broadcasts&quot;
            </p>
          </div>
        ) : list.length === 0 ? (
          <p className="text-sm text-muted-foreground py-8 text-center">
            ยังไม่มีประวัติการส่ง Broadcast
            <span className="block text-xs mt-2">(อ่านจาก collection &quot;broadcasts&quot;)</span>
          </p>
        ) : (
          <div className="flex-1 min-h-0 overflow-auto border rounded-md">
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
                {list.map((item) => {
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
                        {item.createdByName || item.createdBy || "—"}
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
                          disabled={removingId === item.id}
                          onClick={() => setConfirmDeleteId(item.id)}
                        >
                          {removingId === item.id ? (
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
      </DialogContent>
    </Dialog>

      <BroadcastDeleteConfirmDialog
        open={confirmDeleteId !== null}
        onOpenChange={(open) => {
          if (!open && removingId === null) setConfirmDeleteId(null);
        }}
        onConfirm={executeDeleteBroadcast}
        loading={confirmDeleteId !== null && removingId === confirmDeleteId}
      />
    </>
  );
}
