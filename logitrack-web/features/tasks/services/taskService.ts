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

  async fetchActiveDriverIds() {
    const activeQuery = query(
      collection(db, COLLECTIONS.TASKS),
      where("status", "in", ["Pending", "Assigned", "Checked in", "In-Transit"])
    );
    const snapshot = await getDocs(activeQuery);
    const busyDrivers = new Set<string>();
    snapshot.forEach((doc) => {
      const data = doc.data();
      if (data.driverId) busyDrivers.add(data.driverId);
    });
    return busyDrivers;
  },

  async fetchDeliveredDrivers(driverIds: string[]) {
    if (driverIds.length === 0) return [];
    const tripQuery = query(
      collection(db, COLLECTIONS.TRIP_RECORDS),
      where("driverId", "in", driverIds),
      where("status", "==", "delivered")
    );
    const snapshot = await getDocs(tripQuery);
    const delivered = [] as string[];
    snapshot.forEach((doc) => {
       const data = doc.data();
       if (data.driverId) delivered.push(data.driverId);
    });
    return delivered;
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
