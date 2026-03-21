import { useState, useEffect } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { format } from "date-fns";
import { collection, addDoc, updateDoc, doc } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import { db, functions } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { uploadCheckInPhoto } from "@/lib/uploadCheckInPhoto";
import { taskSchema as firstMileTaskSchema, Task as FirstMileTask, normalizeSocIdToKey } from "@/validate/taskSchema";
import { Driver } from "@/validate/driverSchema";
import { taskService } from "../services/taskService";

export function useFirstMileTask({
    mode,
    task,
    isOpen,
    setIsOpen,
    onSuccess
} : {
    mode: "create" | "edit";
    task?: Partial<FirstMileTask>;
    isOpen: boolean;
    setIsOpen: (open: boolean) => void;
    onSuccess?: () => void;
}) {
    const [hubs, setHubs] = useState<Record<string, any>[]>([]);
    const [hubOptions, setHubOptions] = useState<Record<string, any>[]>([]);
    const [socOptions, setSocOptions] = useState<{ source_id: string; name: string }[]>([]);
    const [trucks, setTrucks] = useState<any[]>([]);
    const [drivers, setDrivers] = useState<Driver[]>([]);
    const [loading, setLoading] = useState(false);
    const [hubDropdownOpen, setHubDropdownOpen] = useState(false);
    const [hubSearch, setHubSearch] = useState("");
    const [activeTaskDriverIds, setActiveTaskDriverIds] = useState<Set<string>>(new Set());
    const [newCheckInPhotoFile, setNewCheckInPhotoFile] = useState<File | null>(null);

    const form = useForm<FirstMileTask>({
        resolver: zodResolver(firstMileTaskSchema as any),
        defaultValues: {
            date: new Date(),
            time: "15:00",
            sourceHub: "",
            destination: "",
            truckType: "PICKUP",
            taskId: "",
            driverId: "",
            driverName: "",
            driverPhone: "",
            licensePlate: "",
            status: "Pending" as const,
            taskType: "FIRST_MILE" as const,
        },
    });

    useEffect(() => {
        const fetchData = async () => {
            try {
                const hubList = await taskService.fetchHubs();
                setHubs(hubList);

                const isHub = (st: string) => {
                    const v = String(st ?? "").trim().toUpperCase();
                    if (v === "SOC" || v === "RETURN_CENTER" || v.startsWith("SOC")) return false;
                    return true;
                };
                setHubOptions(hubList.filter((h: any) => isHub(h.station_type ?? "")));

                const socList = hubList
                    .filter((h: any) => {
                        const st = String(h.station_type ?? "").trim().toUpperCase();
                        const code = String(h["Hub Code"] ?? "").trim();
                        return st.startsWith("SOC") && !/^\d/.test(code);
                    })
                    .map((h: any) => ({
                        source_id: (h["Hub Code"] ?? "").toString(),
                        name: (h["Hub Name"] ?? h["Hub Code"] ?? "").toString(),
                    }))
                    .filter((s: any) => s.source_id.length > 0);
                setSocOptions(socList);

                const truckList = await taskService.fetchTrucks();
                setTrucks(truckList);

                const driverList = await taskService.fetchDrivers();
                setDrivers(driverList);
            } catch (err) {
                console.error("Failed to fetch data", err);
            }
        };
        fetchData();
    }, []);

    useEffect(() => {
        if (!isOpen) return;
        const fetchActiveDrivers = async () => {
             try {
                 const busyDrivers = await taskService.fetchActiveDriverIds();
                 if (busyDrivers.size > 0) {
                      const driverArr = Array.from(busyDrivers);
                      for (let i = 0; i < driverArr.length; i += 30) {
                          const batch = driverArr.slice(i, i + 30);
                          const delivered = await taskService.fetchDeliveredDrivers(batch);
                          delivered.forEach((id) => busyDrivers.delete(id));
                      }
                 }
                 setActiveTaskDriverIds(busyDrivers);
             } catch (err) {
                 console.error("Failed to fetch active tasks", err);
             }
        };
        fetchActiveDrivers();
    }, [isOpen]);

    useEffect(() => {
        if (isOpen) {
            setNewCheckInPhotoFile(null);
            if (mode === "edit" && task) {
                const d = task.date ? (task.date instanceof Date ? task.date : new Date(task.date)) : new Date();
                form.reset({
                    ...task,
                    date: d,
                    time: task.time || "",
                    sourceHub: task.sourceHub || "",
                    destination: (task.destination as string) || "",
                    status: (task.status as FirstMileTask["status"]) || "Pending"
                } as FirstMileTask);
            } else {
                form.reset({
                    date: new Date(),
                    time: "15:00",
                    sourceHub: "",
                    destination: "",
                    truckType: "PICKUP",
                    taskId: "",
                    driverName: "",
                    driverPhone: "",
                    licensePlate: "",
                    status: "Pending",
                    taskType: "FIRST_MILE"
                });
            }
        }
    }, [isOpen, mode, task, form]);

    useEffect(() => {
        if (isOpen && mode === "create" && socOptions.length > 0 && !form.getValues("destination")) {
            form.setValue("destination", socOptions[0].source_id);
        }
    }, [isOpen, mode, socOptions, form]);

    const watchedDate = form.watch("date");
    const watchedDestination = form.watch("destination");
    const watchedTruckType = form.watch("truckType");

    useEffect(() => {
        const generateId = async () => {
            if (mode === "create" && watchedDate && watchedDestination) {
                try {
                    const startOfDay = new Date(watchedDate);
                    startOfDay.setHours(0, 0, 0, 0);

                    const endOfDay = new Date(watchedDate);
                    endOfDay.setHours(23, 59, 59, 999);

                    const count = await taskService.countTasksForDay(startOfDay, endOfDay);
                    const runningNumber = (count + 1).toString().padStart(3, '0');
                    const dateStr = format(watchedDate, "ddMMyyyy");
                    const newId = `FM-${dateStr}-${runningNumber}`;

                    form.setValue("taskId", newId);
                } catch (err) {
                    console.error("Error generating ID:", err);
                }
            }
        };

        const timer = setTimeout(generateId, 500);
        return () => clearTimeout(timer);
    }, [watchedDate, watchedDestination, mode, form]);

    const onSubmit = async (values: FirstMileTask) => {
        setLoading(true);
        try {
            if (mode === "create") {
                const dateStr = values.date ? format(values.date, "ddMMyyyy") : "";
                const ref = await addDoc(collection(db, COLLECTIONS.TASKS), {
                    ...values,
                    dateStr,
                    createdAt: new Date(),
                    updatedAt: new Date(),
                });
                try {
                    const notify = httpsCallable<{ taskId: string; oldDriverId?: string; newDriverId?: string; status?: string; sourceHub?: string; destination?: string; date?: string; time?: string, taskType: string }, { ok: boolean }>(functions, "notifyTaskUpdate");
                    await notify({
                        taskId: ref.id,
                        taskType: values.taskType,
                        newDriverId: values.driverId || undefined,
                        status: values.status,
                        sourceHub: values.sourceHub,
                        destination: values.destination,
                        date: values.date ? format(values.date, "yyyy-MM-dd") : undefined,
                        time: values.time,
                    });
                } catch (fcmErr) {
                    console.warn("FCM notify after create:", fcmErr);
                }
            } else if (mode === "edit" && task?.id) {
                const payload: Record<string, unknown> = {
                    ...values,
                    updatedAt: new Date(),
                };
                if (task.status === "Cancelled" && values.driverId) {
                    payload.status = "Assigned";
                }
                if (newCheckInPhotoFile) {
                    const photoUrl = await uploadCheckInPhoto(task.id, newCheckInPhotoFile);
                    payload.checkInPhotoUrl = photoUrl;
                }
                const clean = Object.fromEntries(
                    Object.entries(payload).filter(([, v]) => v !== undefined)
                );
                const dateStr = values.date ? format(values.date, "ddMMyyyy") : "";
                await updateDoc(doc(db, COLLECTIONS.TASKS, task.id), {
                    ...clean as any,
                    dateStr,
                });
                try {
                    const notify = httpsCallable<{ taskId: string; oldDriverId?: string; newDriverId?: string; status?: string; sourceHub?: string; destination?: string; date?: string; time?: string, taskType: string }, { ok: boolean }>(functions, "notifyTaskUpdate");
                    await notify({
                        taskId: task.id,
                        taskType: values.taskType,
                        oldDriverId: task.driverId || undefined,
                        newDriverId: values.driverId || undefined,
                        status: (clean.status as string) ?? values.status,
                        sourceHub: values.sourceHub,
                        destination: values.destination,
                        date: values.date ? format(values.date, "yyyy-MM-dd") : undefined,
                        time: values.time,
                    });
                } catch (fcmErr) {
                    console.warn("FCM notify after update:", fcmErr);
                }
            }
            form.reset();
            setIsOpen(false);
            if (onSuccess) onSuccess();
        } catch (error) {
            console.error("Error saving task:", error);
        } finally {
            setLoading(false);
        }
    };

    return {
        form,
        loading,
        hubs,
        hubOptions,
        socOptions,
        trucks,
        drivers,
        hubDropdownOpen,
        setHubDropdownOpen,
        hubSearch,
        setHubSearch,
        newCheckInPhotoFile,
        setNewCheckInPhotoFile,
        activeTaskDriverIds,
        watchedTruckType,
        onSubmit,
        normalizeSocIdToKey
    };
}
