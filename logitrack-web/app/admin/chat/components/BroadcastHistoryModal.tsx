"use client";

import { useState, useEffect } from "react";
import {
  collection,
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
import { Loader2, Megaphone } from "lucide-react";

export interface BroadcastRecord {
  id: string;
  createdBy: string;
  createdByName: string;
  messageText: string;
  recipientCount: number;
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
        snap.docs.forEach((doc) => {
          const d = doc.data();
          items.push({
            id: doc.id,
            createdBy: (d.createdBy as string) ?? "",
            createdByName: (d.createdByName as string) ?? (d.createdBy as string) ?? "",
            messageText: (d.messageText as string) ?? "",
            recipientCount: (d.recipientCount as number) ?? 0,
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
                  <TableHead className="min-w-[200px]">ข้อความ</TableHead>
                  <TableHead className="w-[140px]">ผู้ส่ง</TableHead>
                  <TableHead className="w-[160px]">วันที่ส่ง</TableHead>
                  <TableHead className="w-[120px] text-right">จำนวนผู้รับ</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map((item) => (
                  <TableRow key={item.id}>
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
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
