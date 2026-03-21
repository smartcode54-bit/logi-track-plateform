"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { collection, query, where, onSnapshot } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { MessageCircle, AlertTriangle, Loader2 } from "lucide-react";
import { useLanguage } from "@/context/language";

export function ChatStatusWidget() {
  const { t } = useLanguage();
  const [pending, setPending] = useState(0);
  const [urgent, setUrgent] = useState(0);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const q = query(
      collection(db, COLLECTIONS.CHATS),
      where("assignedAdminId", "==", null)
    );
    const unsub = onSnapshot(
      q,
      (snap) => {
        let p = 0;
        let u = 0;
        snap.forEach((doc) => {
          const d = doc.data();
          if (d.status === "closed") return;
          p += 1;
          if (d.priority === "urgent") u += 1;
        });
        setPending(p);
        setUrgent(u);
        setLoading(false);
      },
      () => setLoading(false)
    );
    return () => unsub();
  }, []);

  return (
    <Card className="bg-card border border-border">
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-foreground">{t("dashboard.chats.title")}</h3>
          <Link
            href="/admin/chat"
            className="text-xs font-medium text-primary hover:underline"
          >
            {t("dashboard.chats.viewAll")}
          </Link>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {loading ? (
          <div className="flex justify-center py-4">
            <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
          </div>
        ) : (
          <>
            <Link
              href="/admin/chat"
              className="flex items-center gap-3 p-3 rounded-lg bg-muted/50 hover:bg-muted transition-colors"
            >
              <div className="p-2 rounded-full bg-amber-500/10 text-amber-600 dark:text-amber-400">
                <MessageCircle className="h-4 w-4" />
              </div>
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium text-foreground">{t("dashboard.chats.pending")}</p>
                <p className="text-xs text-muted-foreground">{t("dashboard.chats.unassigned")}</p>
              </div>
              <span className="text-lg font-bold tabular-nums">{pending}</span>
            </Link>
            <Link
              href="/admin/chat"
              className="flex items-center gap-3 p-3 rounded-lg bg-muted/50 hover:bg-muted transition-colors"
            >
              <div className="p-2 rounded-full bg-red-500/10 text-red-600 dark:text-red-400">
                <AlertTriangle className="h-4 w-4" />
              </div>
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium text-foreground">{t("dashboard.chats.urgent")}</p>
                <p className="text-xs text-muted-foreground">{t("dashboard.chats.needsAttention")}</p>
              </div>
              <span className="text-lg font-bold tabular-nums">{urgent}</span>
            </Link>
          </>
        )}
      </CardContent>
    </Card>
  );
}
