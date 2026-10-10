import { collection, getDocs, query, where, getCountFromServer } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Driver } from "@/validate/driverSchema";
import { fetchHubsCached } from "@/features/hubs/api/hubs";
import { selectHubRows, type HubRow } from "@/features/hubs/api/selectors";

/** The subset of a trucks/{id} doc the task dialogs need to pick a vehicle for a job. */
export interface TaskTruck {
  id: string;
  licensePlate?: string;
  /** Full-word type from the truck master ("6 Wheels"). Map with lib/truckType.ts, never compare raw. */
  type?: string;
  model?: string;
  truckStatus?: string;
  ownershipType?: "own" | "subcontractor";
  subcontractorId?: string;
}

export const taskService = {
  /**
   * Hub rows for the task dialogs and import dialogs, from the tab's `['hubs']` cache (TW4): one
   * read of the collection per stale time for every caller (features/hubs/api).
   */
  async fetchHubs(): Promise<HubRow[]> {
    return selectHubRows(await fetchHubsCached());
  },

  async fetchTrucks(): Promise<TaskTruck[]> {
    const snapshot = await getDocs(collection(db, COLLECTIONS.TRUCKS));
    return snapshot.docs.map(doc => ({ id: doc.id, ...doc.data() } as TaskTruck));
  },

  async fetchDrivers() {
    const snapshot = await getDocs(collection(db, 'drivers'));
    return snapshot.docs.map(doc => ({ id: doc.id, ...doc.data() } as Driver));
  },

  /** Drivers who are on an active run (not queued Pending/Assigned). Allows multiple planned tasks per driver. */
  async fetchActiveDriverIds() {
    const activeQuery = query(
      collection(db, COLLECTIONS.TASKS),
      where("status", "in", ["Checked in", "In-Transit"])
    );
    const snapshot = await getDocs(activeQuery);
    const busyDrivers = new Set<string>();
    snapshot.forEach((doc) => {
      const data = doc.data();
      if (data.driverId) busyDrivers.add(data.driverId);
    });
    return busyDrivers;
  },

  /** Next runOrder for new tasks assigned to this driver (max existing + 1). */
  async getNextRunOrderForDriver(driverId: string) {
    if (!driverId) return 1;
    const q = query(collection(db, COLLECTIONS.TASKS), where("driverId", "==", driverId));
    const snapshot = await getDocs(q);
    let max = 0;
    snapshot.forEach((d) => {
      const ro = d.data().runOrder;
      if (typeof ro === "number" && Number.isFinite(ro) && ro > max) max = ro;
    });
    return max + 1;
  },

  async countTasksForDay(startDate: Date, endDate: Date) {
    const q = query(
      collection(db, COLLECTIONS.TASKS),
      where("date", ">=", startDate),
      where("date", "<=", endDate)
    );
    const snapshot = await getCountFromServer(q);
    return snapshot.data().count;
  }
};
