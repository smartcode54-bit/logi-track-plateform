"use client";

import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { collection, addDoc, serverTimestamp } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useAuth } from "@/context/auth";
import { Loader2 } from "lucide-react";

export interface ReportIncidentContext {
  driverId: string;
  driverDocId: string;
  truckId?: string;
  truckPlate?: string;
  lat?: number;
  lng?: number;
  tripId?: string;
}

const DELAY_CAUSE_OPTIONS: { value: string; label: string }[] = [
  { value: "incident_cause_accident", label: "Accident" },
  { value: "incident_cause_engine", label: "Engine" },
  { value: "incident_cause_tire", label: "Tire" },
  { value: "incident_cause_traffic", label: "Traffic" },
  { value: "incident_cause_weather", label: "Weather" },
  { value: "incident_cause_checkpoint", label: "Checkpoint" },
];

interface ReportIncidentModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  context: ReportIncidentContext | null;
}

export function ReportIncidentModal({ open, onOpenChange, context }: ReportIncidentModalProps) {
  const auth = useAuth();
  const currentUser = auth?.currentUser ?? null;
  const [description, setDescription] = useState("");
  const [delayCause, setDelayCause] = useState<string>("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async () => {
    if (!context || !currentUser?.uid) return;
    const desc = description.trim();
    if (!desc) {
      setError("Incident description is required.");
      return;
    }
    setError(null);
    setSubmitting(true);
    try {
      await addDoc(collection(db, COLLECTIONS.INCIDENT_REPORTS), {
        driverId: context.driverId,
        tripId: context.tripId ?? null,
        delayCause: delayCause.trim() || null,
        description: desc,
        lat: context.lat ?? null,
        lng: context.lng ?? null,
        reportedBy: "admin",
        reportedByUid: currentUser.uid,
        truckId: context.truckId ?? null,
        truckPlate: context.truckPlate ?? null,
        createdAt: serverTimestamp(),
        updatedAt: serverTimestamp(),
      });
      setDescription("");
      setDelayCause("");
      onOpenChange(false);
    } catch (e) {
      console.error("Report incident error:", e);
      setError("Failed to submit. Try again.");
    } finally {
      setSubmitting(false);
    }
  };

  const handleOpenChange = (next: boolean) => {
    if (!next) {
      setDescription("");
      setDelayCause("");
      setError(null);
    }
    onOpenChange(next);
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>Report Incident</DialogTitle>
          <DialogDescription>
            File an incident report for the selected driver. Driver and truck are pre-filled from the current chat context.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 py-4">
          {context && (
            <>
              <div className="grid grid-cols-3 items-center gap-2 text-sm">
                <Label className="text-muted-foreground">Driver ID</Label>
                <span className="col-span-2 font-mono text-xs">{context.driverId}</span>
              </div>
              {context.truckPlate && (
                <div className="grid grid-cols-3 items-center gap-2 text-sm">
                  <Label className="text-muted-foreground">Truck</Label>
                  <span className="col-span-2">
                    {context.truckPlate}
                    {context.truckId && ` (${context.truckId})`}
                  </span>
                </div>
              )}
              {(context.lat != null || context.lng != null) && (
                <div className="grid grid-cols-3 items-center gap-2 text-sm">
                  <Label className="text-muted-foreground">Location</Label>
                  <span className="col-span-2 text-xs">
                    {context.lat?.toFixed(5)}, {context.lng?.toFixed(5)}
                  </span>
                </div>
              )}
            </>
          )}
          <div className="grid gap-2">
            <Label htmlFor="description">Incident description *</Label>
            <Input
              id="description"
              placeholder="Describe the incident..."
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              disabled={submitting}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="delayCause">Delay cause (optional)</Label>
            <Select value={delayCause || undefined} onValueChange={setDelayCause} disabled={submitting}>
              <SelectTrigger id="delayCause">
                <SelectValue placeholder="Select cause" />
              </SelectTrigger>
              <SelectContent>
                {DELAY_CAUSE_OPTIONS.map((opt) => (
                  <SelectItem key={opt.value} value={opt.value}>
                    {opt.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={submitting || !context}>
            {submitting ? <Loader2 className="h-4 w-4 animate-spin" /> : "Submit"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
