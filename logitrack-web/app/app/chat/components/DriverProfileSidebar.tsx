"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { collection, query, where, getDocs, doc, getDoc } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { getDriverAssignmentHistory } from "@/app/app/truck-assignment/actions.client";
import { Button } from "@/components/ui/button";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Truck, MapPin, Loader2, FileText, AlertTriangle } from "lucide-react";
import type { ReportIncidentContext } from "./ReportIncidentModal";

interface DriverProfileSidebarProps {
  activeDriverId: string | null;
  onReportIncident?: (context: ReportIncidentContext) => void;
}

interface DriverInfo {
  id: string;
  firstName: string;
  lastName: string;
  profileImage?: string;
  authId?: string;
}

interface AssignmentInfo {
  id: string;
  truckId: string;
  truckPlate: string;
  truckModel?: string;
  driverName: string;
}

export function DriverProfileSidebar({ activeDriverId, onReportIncident }: DriverProfileSidebarProps) {
  const [driver, setDriver] = useState<DriverInfo | null>(null);
  const [assignment, setAssignment] = useState<AssignmentInfo | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!activeDriverId) {
      setDriver(null);
      setAssignment(null);
      return;
    }

    let cancelled = false;
    setLoading(true);

    (async () => {
      try {
        const driversRef = collection(db, COLLECTIONS.DRIVERS);
        const q = query(driversRef, where("authId", "==", activeDriverId));
        const snap = await getDocs(q);
        if (cancelled || snap.empty) {
          if (!cancelled) {
            setDriver(null);
            setAssignment(null);
          }
          return;
        }
        const driverDoc = snap.docs[0];
        const d = driverDoc.data();
        const driverInfo: DriverInfo = {
          id: driverDoc.id,
          firstName: (d.firstName as string) ?? "",
          lastName: (d.lastName as string) ?? "",
          profileImage: d.profileImage as string | undefined,
          authId: d.authId as string | undefined,
        };
        if (cancelled) return;
        setDriver(driverInfo);

        const history = await getDriverAssignmentHistory(driverDoc.id);
        const active = history.find((a) => a.status === "active");
        if (cancelled) return;
        if (active) {
          setAssignment({
            id: active.id,
            truckId: active.truckId,
            truckPlate: active.truckPlate,
            truckModel: active.truckModel,
            driverName: active.driverName,
          });
        } else {
          setAssignment(null);
        }
      } catch (e) {
        if (!cancelled) {
          setDriver(null);
          setAssignment(null);
        }
        console.error("DriverProfileSidebar fetch error:", e);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [activeDriverId]);

  if (!activeDriverId) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center p-6 text-center text-muted-foreground text-sm">
        <p>Select a conversation to view driver profile and active assignment.</p>
      </div>
    );
  }

  if (loading) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center p-6">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!driver) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center p-6 text-center text-muted-foreground text-sm">
        <p>Driver not found.</p>
      </div>
    );
  }

  const displayName = [driver.firstName, driver.lastName].filter(Boolean).join(" ") || "Driver";
  const initials = (driver.firstName?.charAt(0) ?? "") + (driver.lastName?.charAt(0) ?? "") || "?";

  return (
    <div className="flex flex-col h-full overflow-y-auto p-4">
      <div className="space-y-4">
        <div className="flex flex-col items-center text-center">
          <Avatar className="h-16 w-16">
            <AvatarImage src={driver.profileImage} alt={displayName} />
            <AvatarFallback className="text-lg">{initials}</AvatarFallback>
          </Avatar>
          <h3 className="font-semibold mt-2">{displayName}</h3>
          <p className="text-xs text-muted-foreground uppercase tracking-wide">Fleet Driver</p>
          <p className="text-xs text-muted-foreground">ID: {driver.id}</p>
        </div>

        {assignment && (
          <div className="rounded-lg border bg-card p-3 space-y-2">
            <h4 className="text-xs font-semibold uppercase text-muted-foreground">Active assignment</h4>
            <div className="flex items-center gap-2 text-sm">
              <Truck className="h-4 w-4 text-muted-foreground shrink-0" />
              <span>{assignment.truckPlate}</span>
              {assignment.truckModel && (
                <span className="text-muted-foreground">({assignment.truckModel})</span>
              )}
            </div>
            <p className="text-xs text-muted-foreground flex items-center gap-1">
              <MapPin className="h-3 w-3 shrink-0" />
              Truck ID: {assignment.truckId}
            </p>
          </div>
        )}

        {!assignment && (
          <div className="rounded-lg border border-dashed p-3 text-center text-muted-foreground text-xs">
            No active truck assignment
          </div>
        )}

        <div className="flex flex-col gap-2 pt-2">
          <Button variant="outline" size="sm" className="w-full gap-2" asChild>
            <Link href={`/app/drivers/view?id=${driver.id}`} prefetch={false}>
              <FileText className="h-4 w-4" />
              View Full Fleet Log
            </Link>
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="w-full gap-2"
            onClick={() => {
              if (onReportIncident && activeDriverId) {
                onReportIncident({
                  driverId: activeDriverId,
                  driverDocId: driver.id,
                  truckId: assignment?.truckId,
                  truckPlate: assignment?.truckPlate,
                });
              }
            }}
          >
            <AlertTriangle className="h-4 w-4" />
            Report Incident
          </Button>
        </div>
      </div>
    </div>
  );
}
