import { collection, getDocs, query, where, getCountFromServer } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Driver } from "@/validate/driverSchema";

export const taskService = {
  async fetchHubs() {
    const snapshot = await getDocs(collection(db, "hubs"));
    return snapshot.docs.map(doc => {
      const data = doc.data();
      return {
        'Hub Code': data.source_id ?? data.hubId ?? data.hubCode,
        'Hub Name': data.source_name_en ?? data.hubName,
        station_type: data.station_type ?? "",
        linkedCustomerId: data.linkedCustomerId ?? "",
        customerLinkKind: data.customerLinkKind ?? "",
        lat: data.latitude ?? data.lat,
        lng: data.longitude ?? data.lng,
        source: 'custom',
        id: doc.id
      };
    });
  },
  
  async fetchTrucks() {
    const snapshot = await getDocs(collection(db, 'trucks'));
    return snapshot.docs.map(doc => ({ id: doc.id, ...doc.data() }));
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
