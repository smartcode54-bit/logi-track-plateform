import { useState, useEffect } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { format } from "date-fns";
import { collection, addDoc, updateDoc, doc, deleteField } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import { toast } from "sonner";
import { db, functions } from "@/firebase/client";
import { useLanguage } from "@/context/language";
import { COLLECTIONS } from "@/lib/collections";
import { uploadCheckInPhoto } from "@/lib/uploadCheckInPhoto";
import { taskSchema as firstMileTaskSchema, Task as FirstMileTask, normalizeSocIdToKey } from "@/validate/taskSchema";
import { hubSourceIdHasSpxSuffix } from "@/validate/hubSchema";
import {
    isLinkedToCustomer,
    linkCustomerIdForPlace,
    placeOwnerIds,
    placesOfCustomer,
    samePlaceCode,
} from "@/lib/customerPlaces";
import { Driver } from "@/validate/driverSchema";
import { selectableDrivers } from "@/lib/driverStatus";
import { matchDriverOptionId } from "@/lib/driverName";
import { taskService, TaskTruck } from "../services/taskService";
import { getCustomers, CustomerData } from "@/features/customers/api/customers";

export function useLineHaulTask({
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
    const { t } = useLanguage();
    const [hubs, setHubs] = useState<Record<string, any>[]>([]);
    const [hubOptions, setHubOptions] = useState<Record<string, any>[]>([]);
    const [socOptions, setSocOptions] = useState<{ source_id: string; name: string; linkedCustomerId?: string }[]>([]);
    const [trucks, setTrucks] = useState<TaskTruck[]>([]);
    const [drivers, setDrivers] = useState<Driver[]>([]);
    const [loading, setLoading] = useState(false);
    const [hubDropdownOpen, setHubDropdownOpen] = useState(false);
    const [hubSearch, setHubSearch] = useState("");
    const [socDropdownOpen, setSocDropdownOpen] = useState(false);
    const [socSearch, setSocSearch] = useState("");
    const [activeTaskDriverIds, setActiveTaskDriverIds] = useState<Set<string>>(new Set());
    const [newCheckInPhotoFile, setNewCheckInPhotoFile] = useState<File | null>(null);
    const [customersById, setCustomersById] = useState<Map<string, CustomerData>>(new Map());

    const form = useForm<FirstMileTask>({
        resolver: zodResolver(firstMileTaskSchema as any),
        defaultValues: {
            date: new Date(),
            time: "15:00",
            sourceHub: "",
            destination: "",
            billingCustomerId: "",
            jobCategory: "PRIMARY" as const,
            truckType: "4W",
            truckId: "",
            taskId: "",
            driverId: "",
            driverName: "",
            driverPhone: "",
            licensePlate: "",
            status: "Pending" as const,
            taskType: "LINE_HAUL" as const,
            isMultiDelivery: false,
            deliveryStops: undefined,
            helperDriverIds: [],
            actualPickupAt: undefined,
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

                const seen = new Set<string>();
                const socList = hubList
                    .filter((h: any) => {
                        const st = String(h.station_type ?? "").trim().toUpperCase();
                        const code = String(h["Hub Code"] ?? "").trim();
                        return st.startsWith("SOC") && !/^\d/.test(code);
                    })
                    .map((h: any) => ({
                        source_id: (h["Hub Code"] ?? "").toString(),
                        name: (h["Hub Name Th"] ?? h["Hub Name"] ?? h["Hub Code"] ?? "").toString(),
                        linkedCustomerId: String(h.linkedCustomerId ?? "").trim() || undefined,
                    }))
                    .filter((s: any) => {
                        if (!s.source_id || seen.has(s.source_id)) return false;
                        seen.add(s.source_id);
                        return true;
                    });
                setSocOptions(socList);

                const truckList = await taskService.fetchTrucks();
                setTrucks(truckList);

                const driverList = await taskService.fetchDrivers();
                setDrivers(driverList);
                const customers = await getCustomers();
                const customerMap = new Map<string, CustomerData>();
                customers.forEach((customer) => customerMap.set(customer.id, customer));
                setCustomersById(customerMap);
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
                    // Prefill billing customer from the task's own value, else the hub link (ADR 0027).
                    billingCustomerId:
                        task.billingCustomerId || task.sourceHubLinkedCustomerId || task.destinationLinkedCustomerId || "",
                    jobCategory: (task.jobCategory as FirstMileTask["jobCategory"]) || "PRIMARY",
                    status: (task.status as FirstMileTask["status"]) || "Pending",
                    isMultiDelivery: task.isMultiDelivery || false,
                    deliveryStops: task.deliveryStops || undefined,
                    // Coerce a stored Firestore Timestamp → Date for the datetime-local control.
                    actualPickupAt: task.actualPickupAt
                        ? (task.actualPickupAt instanceof Date
                            ? task.actualPickupAt
                            : (typeof (task.actualPickupAt as any).toDate === "function"
                                ? (task.actualPickupAt as any).toDate()
                                : new Date(task.actualPickupAt as any)))
                        : undefined,
                } as FirstMileTask);
            } else {
                // Create mode. When a `task` seed is supplied (Duplicate flow), copy its route /
                // customer / vehicle so the admin only changes driver/truck; otherwise blank.
                // `task` is undefined for a normal "Add", so this stays identical to before.
                form.reset({
                    date: new Date(),
                    time: task?.time || "15:00",
                    sourceHub: task?.sourceHub || "",
                    destination: (task?.destination as string) || "",
                    billingCustomerId:
                        task?.billingCustomerId || task?.sourceHubLinkedCustomerId || task?.destinationLinkedCustomerId || "",
                    jobCategory: (task?.jobCategory as FirstMileTask["jobCategory"]) || "PRIMARY",
                    truckType: (task?.truckType as FirstMileTask["truckType"]) || "4W",
                    truckId: task?.truckId || "",
                    taskId: "",
                    driverId: task?.driverId || "",
                    driverName: task?.driverName || "",
                    driverPhone: task?.driverPhone || "",
                    licensePlate: task?.licensePlate || "",
                    status: "Pending",
                    taskType: "LINE_HAUL",
                    isMultiDelivery: task?.isMultiDelivery || false,
                    deliveryStops: task?.deliveryStops || undefined,
                    // A duplicate / new job starts with no actual pickup time — the admin sets it fresh.
                    actualPickupAt: undefined,
                });
            }
        }
    }, [isOpen, mode, task, form]);

    const watchedDate = form.watch("date");
    const watchedDestination = form.watch("destination");
    const watchedTruckType = form.watch("truckType");
    const watchedBillingCustomerId = form.watch("billingCustomerId");

    // Customer first (one job = one customer): source SOCs, destination hubs and delivery stops are
    // listed only from places linked to the chosen billing customer. This replaces the old flow that
    // defaulted the first SOC and back-filled the customer from whichever hub was picked.
    // A customer that borrows another's places (customers.usePlacesOfCustomerId) also sees those.
    const placeOwnersOf = (customerId: string | undefined) =>
        placeOwnerIds(customerId, (id) => customersById.get(id));
    const customerSocOptions = placesOfCustomer(socOptions, placeOwnersOf(watchedBillingCustomerId));
    const customerHubOptions = placesOfCustomer(hubOptions, placeOwnersOf(watchedBillingCustomerId));
    // Stops are the customer's destination hubs — never another customer's and never SOCs.
    const stopDestinationOptions = (() => {
        const seen = new Set<string>();
        const options: { code: string; name: string }[] = [];
        for (const hub of customerHubOptions) {
            const code = String(hub["Hub Code"] ?? "").trim();
            if (!code || seen.has(code)) continue;
            seen.add(code);
            options.push({ code, name: String(hub["Hub Name Th"] || hub["Hub Name"] || code) });
        }
        return options;
    })();

    /** Pick the customer, then drop route picks that belong to someone else; auto-pick a lone option. */
    const selectCustomer = (customerId: string) => {
        form.setValue("billingCustomerId", customerId, { shouldDirty: true, shouldValidate: true });
        const owners = placeOwnersOf(customerId);
        const socCodes = placesOfCustomer(socOptions, owners).map((s) => s.source_id);
        const hubCodes = placesOfCustomer(hubOptions, owners).map((h) => String(h["Hub Code"] ?? ""));
        const keepOrSingle = (current: string, codes: string[]) =>
            codes.some((code) => samePlaceCode(code, current)) ? current : codes.length === 1 ? codes[0] : "";
        form.setValue("sourceHub", keepOrSingle(form.getValues("sourceHub") ?? "", socCodes));
        form.setValue("destination", keepOrSingle(form.getValues("destination") ?? "", hubCodes));
        const stops = form.getValues("deliveryStops");
        if (stops?.length) {
            form.setValue(
                "deliveryStops",
                stops.map((stop) =>
                    hubCodes.some((code) => samePlaceCode(code, stop.destination)) ? stop : { ...stop, destination: "" }
                )
            );
            form.clearErrors("deliveryStops");
        }
    };

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
                    const newId = `LH-${dateStr}-${runningNumber}`;

                    form.setValue("taskId", newId);
                } catch (err) {
                    console.error("Error generating ID:", err);
                }
            }
        };

        const timer = setTimeout(generateId, 500);
        return () => clearTimeout(timer);
    }, [watchedDate, watchedDestination, mode, form]);

    const buildHubLinkFields = (values: FirstMileTask, useDeleteFieldForMissing: boolean) => {
        const sourceHub = hubs.find((h) => String(h["Hub Code"] ?? "").trim() === String(values.sourceHub ?? "").trim());
        const destinationHub = hubs.find((h) => String(h["Hub Code"] ?? "").trim() === String(values.destination ?? "").trim());
        // Stamp the job's customer on places it may use (its own or borrowed) — see linkCustomerIdForPlace.
        const owners = placeOwnersOf(values.billingCustomerId);
        const sourceCustomerId = linkCustomerIdForPlace(sourceHub, owners);
        const destinationCustomerId = linkCustomerIdForPlace(destinationHub, owners);
        const sourceCustomer = sourceCustomerId ? customersById.get(sourceCustomerId) : undefined;
        const destinationCustomer = destinationCustomerId ? customersById.get(destinationCustomerId) : undefined;
        const missing = useDeleteFieldForMissing ? deleteField() : undefined;

        const sourceCode =
            sourceHub &&
            hubSourceIdHasSpxSuffix(String(sourceHub["Hub Code"] ?? ""))
                ? "SPX"
                : sourceCustomer?.code;
        const destCode =
            destinationHub &&
            hubSourceIdHasSpxSuffix(String(destinationHub["Hub Code"] ?? ""))
                ? "SPX"
                : destinationCustomer?.code;

        return {
            sourceHubLinkedCustomerId: sourceCustomerId || missing,
            sourceHubLinkedCustomerName: sourceCustomer?.name || missing,
            sourceHubLinkedCustomerCode: sourceCode || missing,
            sourceHubCustomerLinkKind: sourceHub?.customerLinkKind || missing,
            destinationLinkedCustomerId: destinationCustomerId || missing,
            destinationLinkedCustomerName: destinationCustomer?.name || missing,
            destinationLinkedCustomerCode: destCode || missing,
            destinationCustomerLinkKind: destinationHub?.customerLinkKind || missing,
        };
    };

    const findHub = (code: string | undefined) =>
        hubs.find((h) => String(h["Hub Code"] ?? "").trim() === String(code ?? "").trim());

    // The admin no longer picks a customer per stop; the stop's link comes from its hub doc, stamped
    // with the job's customer when the hub is one it may use — customer-scoped task lists match on it.
    const buildStopLinkFields = (destination: string, jobCustomerId: string | undefined) => {
        const hub = findHub(destination);
        const customerId = linkCustomerIdForPlace(hub, placeOwnersOf(jobCustomerId));
        const customer = customerId ? customersById.get(customerId) : undefined;
        const code = hub && hubSourceIdHasSpxSuffix(String(hub["Hub Code"] ?? "")) ? "SPX" : customer?.code;
        return {
            destinationLinkedCustomerId: customerId || undefined,
            destinationLinkedCustomerName: customer?.name,
            destinationLinkedCustomerCode: code,
            destinationCustomerLinkKind: hub?.customerLinkKind || undefined,
        };
    };

    const onSubmit = async (values: FirstMileTask) => {
        // Required billing customer at assign (ADR 0027) — fail loud, block save.
        if (!values.billingCustomerId) {
            form.setError("billingCustomerId", { type: "manual", message: "กรุณาเลือกลูกค้า / Customer is required" });
            return;
        }
        // Every stop must be one of the chosen customer's hubs — catches a customer switched after the
        // stops were picked, and legacy tasks whose stops were SOCs.
        if (values.isMultiDelivery && values.deliveryStops) {
            const foreignStops = values.deliveryStops.filter(
                (stop) => !isLinkedToCustomer(findHub(stop.destination), placeOwnersOf(values.billingCustomerId))
            );
            if (foreignStops.length > 0) {
                const message = t("lineHaul.task.stopsWrongCustomer", {
                    stops: foreignStops.map((stop) => stop.index).join(", "),
                });
                form.setError("deliveryStops", { type: "manual", message });
                toast.error(message);
                return;
            }
        }
        const chosenCustomer = customersById.get(values.billingCustomerId);
        const billingCustomerFields: Record<string, unknown> = {
            billingCustomerId: values.billingCustomerId,
            billingCustomerName: chosenCustomer?.name,
            billingCustomerCode: chosenCustomer?.code,
        };
        // The plan has no time field (ADR 0028). Carry the time from the actual pickup so the stored
        // task.time (schema-required, shown in lists) stays meaningful; default 00:00 when unset.
        values.time = values.actualPickupAt ? format(new Date(values.actualPickupAt), "HH:mm") : (values.time || "00:00");
        setLoading(true);
        try {
            if (values.isMultiDelivery && values.deliveryStops && values.deliveryStops.length >= 2) {
                // Use Cloud Function for multi-delivery tasks — same payload shape as First Mile.
                // The callable reads `id` (not `taskId`) to update, requires `destination`, and
                // computes runOrder itself.
                const callable = httpsCallable<any, { id: string }>(functions, "createOrUpdateTask");

                const payload = {
                    id: mode === "edit" ? task?.id : undefined,
                    date: values.date ? format(values.date, "yyyy-MM-dd") : new Date().toISOString(),
                    dateStr: values.date ? format(values.date, "ddMMyyyy") : undefined,
                    time: values.time,
                    taskId: values.taskId || undefined,
                    sourceHub: values.sourceHub,
                    destination: values.destination,
                    jobCategory: values.jobCategory,
                    truckType: values.truckType,
                    truckId: values.truckId || undefined,
                    taskType: values.taskType,
                    driverId: values.driverId || undefined,
                    driverName: values.driverName || undefined,
                    driverPhone: values.driverPhone || undefined,
                    licensePlate: values.licensePlate || undefined,
                    status: values.status,
                    helperDriverIds: values.helperDriverIds ?? [],
                    isMultiDelivery: true,
                    deliveryStops: values.deliveryStops.map((stop) => ({
                        index: stop.index,
                        destination: stop.destination,
                        ...buildStopLinkFields(stop.destination, values.billingCustomerId),
                    })),
                    ...buildHubLinkFields(values, false),
                    ...billingCustomerFields,
                    actualPickupAt: values.actualPickupAt ? new Date(values.actualPickupAt).toISOString() : undefined,
                };

                const result = await callable(payload);
                try {
                    const notify = httpsCallable<{ taskId: string; oldDriverId?: string; newDriverId?: string; status?: string; sourceHub?: string; destination?: string; date?: string; time?: string; taskType: string }, { ok: boolean }>(functions, "notifyTaskUpdate");
                    await notify({
                        taskId: result.data.id,
                        taskType: values.taskType,
                        oldDriverId: mode === "edit" ? task?.driverId : undefined,
                        newDriverId: values.driverId || undefined,
                        status: values.status,
                        sourceHub: values.sourceHub,
                        destination: values.deliveryStops?.[0]?.destination,
                        date: values.date ? format(values.date, "yyyy-MM-dd") : undefined,
                        time: values.time,
                    });
                } catch (fcmErr) {
                    console.warn("FCM notify:", fcmErr);
                }
            } else if (mode === "create") {
                const dateStr = values.date ? format(values.date, "ddMMyyyy") : "";
                const runOrder = values.driverId
                    ? await taskService.getNextRunOrderForDriver(values.driverId)
                    : undefined;
                const hubLinkFields = buildHubLinkFields(values, false);
                const rawCreate = {
                    ...values,
                    ...hubLinkFields,
                    ...billingCustomerFields,
                    dateStr,
                    ...(runOrder != null ? { runOrder } : {}),
                    createdAt: new Date(),
                    updatedAt: new Date(),
                };
                const createPayload = Object.fromEntries(
                    Object.entries(rawCreate).filter(([, v]) => v !== undefined)
                );
                const ref = await addDoc(collection(db, COLLECTIONS.TASKS), createPayload as FirstMileTask);
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
                    ...buildHubLinkFields(values, true),
                    ...billingCustomerFields,
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

    // Driver/helper pickers list active drivers only, plus whoever this task already holds so an
    // edit never blanks out a driver made inactive since (lib/driverStatus.ts).
    const watchedDriverId = form.watch("driverId");
    const driverChoices = selectableDrivers(drivers, [
        watchedDriverId,
        matchDriverOptionId(drivers, watchedDriverId, form.watch("driverName")),
        ...(form.watch("helperDriverIds") ?? []),
    ]);

    const customerOptions = Array.from(customersById.values());

    return {
        form,
        loading,
        hubs,
        hubOptions,
        socOptions,
        trucks,
        drivers,
        driverChoices,
        hubDropdownOpen,
        setHubDropdownOpen,
        hubSearch,
        setHubSearch,
        socDropdownOpen,
        setSocDropdownOpen,
        socSearch,
        setSocSearch,
        newCheckInPhotoFile,
        setNewCheckInPhotoFile,
        activeTaskDriverIds,
        watchedTruckType,
        onSubmit,
        normalizeSocIdToKey,
        customerOptions,
        customerSocOptions,
        customerHubOptions,
        selectCustomer,
        stopDestinationOptions,
    };
}
