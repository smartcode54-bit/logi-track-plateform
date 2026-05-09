"use client";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Loader2 } from "lucide-react";

interface BroadcastDeleteConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void | Promise<void>;
  loading: boolean;
}

export function BroadcastDeleteConfirmDialog({
  open,
  onOpenChange,
  onConfirm,
  loading,
}: BroadcastDeleteConfirmDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="sm:max-w-md z-[1003]"
        onPointerDownOutside={(e) => loading && e.preventDefault()}
        onEscapeKeyDown={(e) => loading && e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>ยืนยันการยกเลิกส่ง</DialogTitle>
          <DialogDescription className="text-left space-y-2 pt-1">
            <span className="block">
              ลบรายการนี้จากประวัติและจากแอปคนขับ
            </span>
            <span className="block text-amber-600 dark:text-amber-500 text-sm">
              การแจ้งเตือนที่ส่งไปยังอุปกรณ์คนขับแล้วจะไม่ถูกเรียกคืน
            </span>
          </DialogDescription>
        </DialogHeader>
        <DialogFooter className="gap-2 sm:gap-0">
          <Button
            type="button"
            variant="outline"
            disabled={loading}
            onClick={() => onOpenChange(false)}
          >
            กลับ
          </Button>
          <Button
            type="button"
            variant="destructive"
            disabled={loading}
            onClick={() => void onConfirm()}
          >
            {loading ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              "ยกเลิกส่ง"
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
