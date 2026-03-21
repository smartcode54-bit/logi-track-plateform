"use client";

import { useEffect, useState } from "react";
import { collection, query, where, onSnapshot } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ClipboardCheck, ArrowRight } from "lucide-react";
import Link from "next/link";
import { useLanguage } from "@/context/language";

export function ExpenseAuditWidget() {
  const { t } = useLanguage();
  const [count, setCount] = useState<number | null>(null);

  useEffect(() => {
    const q = query(
      collection(db, COLLECTIONS.VEHICLE_EXPENSES),
      where("status", "==", "PENDING")
    );
    const unsub = onSnapshot(q, (snap) => {
      setCount(snap.size);
    }, (err) => {
      console.error("Expense audit snapshot error:", err);
    });
    return () => unsub();
  }, []);

  return (
    <Card className="shadow-sm">
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle className="text-sm font-semibold flex items-center gap-2">
          <ClipboardCheck className="h-4 w-4 text-emerald-600" />
          {t("dashboard.expenses.title", "จัดการรายจ่าย/ตรวจสอบ")}
        </CardTitle>
        <Link href="/admin/accounting/audit" className="text-xs text-primary hover:underline flex items-center gap-1">
          {t("dashboard.vehiclePosition.viewAll", "ดูทั้งหมด")} <ArrowRight className="h-3 w-3" />
        </Link>
      </CardHeader>
      <CardContent>
        <div className="flex items-center justify-between">
          <div className="space-y-1">
            <p className="text-3xl font-bold tracking-tight">
              {count !== null ? count : "..."}
            </p>
            <p className="text-xs text-muted-foreground">
              {t("dashboard.expenses.pendingApproval", "รายการรออนุมัติ")}
            </p>
          </div>
          <div className={`p-3 rounded-full ${count && count > 0 ? "bg-amber-100 dark:bg-amber-950 text-amber-600" : "bg-muted text-muted-foreground"}`}>
            <ClipboardCheck className="h-6 w-6" />
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
