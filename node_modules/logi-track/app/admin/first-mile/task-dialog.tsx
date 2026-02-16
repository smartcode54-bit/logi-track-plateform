"use client";

import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";

import { format } from "date-fns";
import { CalendarIcon, Loader2, Check, ChevronsUpDown } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    DialogTrigger,
} from "@/components/ui/dialog";
import {
    Form,
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Calendar } from "@/components/ui/calendar";
import {
    Popover,
    PopoverContent,
    PopoverTrigger,
} from "@/components/ui/popover";
import {
    Command,
    CommandEmpty,
    CommandGroup,
    CommandInput,
    CommandItem,
    CommandList,
} from "@/components/ui/command";

import { cn } from "@/lib/utils";
import { firstMileTaskSchema, FirstMileTask, SOC_KEYS, SOC_DESTINATIONS } from "@/validate/firstMileTaskSchema";
import { Driver } from "@/validate/driverSchema";
import { collection, addDoc, doc, updateDoc, getDocs, query, where, getCountFromServer } from "firebase/firestore";
import { db } from "@/firebase/client";
import { useLanguage } from "@/context/language";

interface ItemDialogProps {
    mode: "create" | "edit";
    task?: Partial<FirstMileTask>;
    trigger?: React.ReactNode;
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    onSuccess?: () => void;
}

export function FirstMileTaskDialog({ mode, task, trigger, open, onOpenChange, onSuccess }: ItemDialogProps) {
    const [internalOpen, setInternalOpen] = useState(false);
    const [hubs, setHubs] = useState<Record<string, any>[]>([]);
    const [drivers, setDrivers] = useState<Driver[]>([]);
    const [loading, setLoading] = useState(false);
    const { t } = useLanguage();

    // Controlled open state
    const isOpen = open !== undefined ? open : internalOpen;
    const setIsOpen = onOpenChange || setInternalOpen;

    // Safe default values
    const form = useForm<FirstMileTask>({
        resolver: zodResolver(firstMileTaskSchema as any),
        defaultValues: {
            date: new Date(),
            time: "15:00",
            sourceHub: "",
            destination: undefined as any, // Force selection
            truckType: undefined as any, // Force selection
            FirstMileTaskId: "",
            driverId: "",
            driverName: "",
            driverPhone: "",
            licensePlate: "",
            status: "Pending" as const
        },
    });

    // Load Hubs and Drivers
    useEffect(() => {
        const fetchData = async () => {
            try {
                // Fetch Hubs directly from Firestore
                const hubSnapshot = await getDocs(collection(db, "hubs"));
                const hubList = hubSnapshot.docs.map(doc => {
                    const data = doc.data();
                    return {
                        'Hub Code': data.hubId || data.hubCode,
                        'Hub Name': data.hubName,
                        'Hub Name TH': data.hubTHName,
                        lat: data.lat,
                        lng: data.lng,
                        source: 'custom',
                        id: doc.id
                    };
                });
                setHubs(hubList);

                // Fetch Drivers
                const driverSnapshot = await getDocs(collection(db, 'drivers'));
                const driverList = driverSnapshot.docs.map(doc => ({ id: doc.id, ...doc.data() } as Driver));
                setDrivers(driverList);
            } catch (err) {
                console.error("Failed to fetch data", err);
            }
        };
        fetchData();
    }, []);

    // Reset/Set values on open or task change
    useEffect(() => {
        if (isOpen) {
            if (mode === "edit" && task) {
                const d = task.date ? (task.date instanceof Date ? task.date : new Date(task.date)) : new Date();
                form.reset({
                    ...task,
                    date: d,
                    // Ensure fields are present
                    time: task.time || "",
                    sourceHub: task.sourceHub || "",
                    destination: (task.destination as FirstMileTask["destination"]) || "SOC-E",
                    status: (task.status as FirstMileTask["status"]) || "Pending"
                } as FirstMileTask);
            } else {
                form.reset({
                    date: new Date(),
                    time: "15:00",
                    sourceHub: "",
                    destination: undefined as any, // Force selection
                    truckType: undefined as any, // Force selection
                    FirstMileTaskId: "",
                    driverName: "",
                    driverPhone: "",
                    licensePlate: "",
                    status: "Pending"
                });
            }
        }
    }, [isOpen, mode, task, form]);

    const onSubmit = async (values: FirstMileTask) => {
        setLoading(true);
        try {
            if (mode === "create") {
                await addDoc(collection(db, "first_mile_tasks"), {
                    ...values,
                    createdAt: new Date(),
                    updatedAt: new Date(),
                });
            } else if (mode === "edit" && task?.id) {
                await updateDoc(doc(db, "first_mile_tasks", task.id), {
                    ...values,
                    updatedAt: new Date(),
                });
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

    // Auto-generate ID on Date or Destination Change (Create Mode)
    const watchedDate = form.watch("date");
    const watchedDestination = form.watch("destination");

    useEffect(() => {
        const generateId = async () => {
            if (mode === "create" && watchedDate && watchedDestination) {
                try {
                    const startOfDay = new Date(watchedDate);
                    startOfDay.setHours(0, 0, 0, 0);

                    const endOfDay = new Date(watchedDate);
                    endOfDay.setHours(23, 59, 59, 999);

                    // Running number based on Date (daily count)
                    const qByDate = query(
                        collection(db, "first_mile_tasks"),
                        where("date", ">=", startOfDay),
                        where("date", "<=", endOfDay)
                    );

                    const snapshot = await getCountFromServer(qByDate);
                    const count = snapshot.data().count;
                    const runningNumber = (count + 1).toString().padStart(3, '0');
                    const dateStr = format(watchedDate, "ddMMyyyy");

                    // Format: FM-[Date]-[Destination]-[RunningNumber]
                    // Example: FM-05022026-SOC-E-001
                    const newId = `FM-${dateStr}-${watchedDestination}-${runningNumber}`;

                    form.setValue("FirstMileTaskId", newId);
                } catch (err) {
                    console.error("Error generating ID:", err);
                }
            }
        };

        const timer = setTimeout(generateId, 500);
        return () => clearTimeout(timer);
    }, [watchedDate, watchedDestination, mode, form]);

    return (
        <Dialog open={isOpen} onOpenChange={setIsOpen}>
            {trigger && <DialogTrigger asChild>{trigger}</DialogTrigger>}
            <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
                <DialogHeader>
                    <DialogTitle>{mode === "create" ? t("firstMile.task.createTitle") : t("firstMile.task.editTitle")}</DialogTitle>
                    <DialogDescription>
                        {mode === "create" ? t("firstMile.task.createDesc") : t("firstMile.task.editDesc")}
                    </DialogDescription>
                </DialogHeader>

                <Form {...form}>
                    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                            {/* Date Field */}
                            <FormField
                                control={form.control}
                                name="date"
                                render={({ field }) => (
                                    <FormItem className="flex flex-col">
                                        <FormLabel>{t("firstMile.task.date")}</FormLabel>
                                        <Popover>
                                            <PopoverTrigger asChild>
                                                <FormControl>
                                                    <Button
                                                        variant={"outline"}
                                                        className={cn(
                                                            "w-full pl-3 text-left font-normal",
                                                            !field.value && "text-muted-foreground"
                                                        )}
                                                    >
                                                        {field.value ? (
                                                            format(field.value, "dd/MM/yyyy")
                                                        ) : (
                                                            <span>{t("firstMile.task.pickDate")}</span>
                                                        )}
                                                        <CalendarIcon className="ml-auto h-4 w-4 opacity-50" />
                                                    </Button>
                                                </FormControl>
                                            </PopoverTrigger>
                                            <PopoverContent className="w-auto p-0" align="start">
                                                <Calendar
                                                    mode="single"
                                                    selected={field.value}
                                                    onSelect={field.onChange}
                                                    initialFocus
                                                />
                                            </PopoverContent>
                                        </Popover>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            {/* Time Field */}
                            <FormField
                                control={form.control}
                                name="time"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("firstMile.task.time")}</FormLabel>
                                        <Select onValueChange={field.onChange} value={field.value}>
                                            <FormControl>
                                                <SelectTrigger>
                                                    <SelectValue placeholder={t("firstMile.task.selectTime")} />
                                                </SelectTrigger>
                                            </FormControl>
                                            <SelectContent className="h-[200px]" position="popper">
                                                {Array.from({ length: 48 }).map((_, i) => {
                                                    const hour = Math.floor(i / 2).toString().padStart(2, '0');
                                                    const minute = (i % 2 === 0 ? '00' : '30');
                                                    const time = `${hour}:${minute}`;
                                                    return (
                                                        <SelectItem key={time} value={time}>
                                                            {time}
                                                        </SelectItem>
                                                    );
                                                })}
                                            </SelectContent>
                                        </Select>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                        </div>

                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                            {/* Source Hub - Combobox */}
                            <FormField
                                control={form.control}
                                name="sourceHub"
                                render={({ field }) => (
                                    <FormItem className="flex flex-col">
                                        <FormLabel>{t("firstMile.task.sourceHub")}</FormLabel>
                                        <Popover>
                                            <PopoverTrigger asChild>
                                                <FormControl>
                                                    <Button
                                                        variant="outline"
                                                        role="combobox"
                                                        className={cn(
                                                            "w-full justify-between",
                                                            !field.value && "text-muted-foreground"
                                                        )}
                                                    >
                                                        {field.value
                                                            ? (() => {
                                                                const h = hubs.find(
                                                                    (hub) => (hub['Hub Code'] || hub['Code']) === field.value
                                                                );
                                                                const val = h ? (h['Hub Code'] || h['Code']) : field.value;
                                                                const name = h ? (h['Hub Name'] || h['station_name_en'] || val) : "";
                                                                return `${val} - ${name}`;
                                                            })()
                                                            : t("firstMile.task.selectHub")}
                                                        <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
                                                    </Button>
                                                </FormControl>
                                            </PopoverTrigger>
                                            <PopoverContent className="w-[300px] p-0">
                                                <Command>
                                                    <CommandInput placeholder={t("firstMile.task.searchHub")} />
                                                    <CommandList>
                                                        <CommandEmpty>{t("firstMile.task.noHub")}</CommandEmpty>
                                                        <CommandGroup>
                                                            {/* Slice to prevent performance issues if list is huge */}
                                                            {hubs.slice(0, 100).map((hub, idx) => {
                                                                const val = hub['Hub Code'] || hub['Code'];
                                                                const name = hub['Hub Name'] || hub['station_name_en'] || val;
                                                                if (!val) return null;
                                                                return (
                                                                    <CommandItem
                                                                        value={`${val} ${name}`}
                                                                        key={`${val}-${idx}`}
                                                                        onSelect={() => {
                                                                            form.setValue("sourceHub", val);
                                                                        }}
                                                                    >
                                                                        <Check
                                                                            className={cn(
                                                                                "mr-2 h-4 w-4",
                                                                                val === field.value
                                                                                    ? "opacity-100"
                                                                                    : "opacity-0"
                                                                            )}
                                                                        />
                                                                        {val} - {name}
                                                                    </CommandItem>
                                                                );
                                                            })}
                                                        </CommandGroup>
                                                    </CommandList>
                                                </Command>
                                            </PopoverContent>
                                        </Popover>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            {/* Destination */}
                            <FormField
                                control={form.control}
                                name="destination"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("firstMile.task.destination")}</FormLabel>
                                        <Select onValueChange={field.onChange} defaultValue={field.value} value={field.value}>
                                            <FormControl>
                                                <SelectTrigger>
                                                    <SelectValue placeholder={t("firstMile.task.selectSOC")} />
                                                </SelectTrigger>
                                            </FormControl>
                                            <SelectContent>
                                                {SOC_KEYS.map((key) => (
                                                    <SelectItem key={key} value={key}>
                                                        {SOC_DESTINATIONS[key]}
                                                    </SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                        </div>

                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                            {/* Truck Type */}
                            <FormField
                                control={form.control}
                                name="truckType"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("firstMile.task.truckType")}</FormLabel>
                                        <Select onValueChange={field.onChange} defaultValue={field.value} value={field.value}>
                                            <FormControl>
                                                <SelectTrigger>
                                                    <SelectValue placeholder={t("firstMile.task.selectTruckType")} />
                                                </SelectTrigger>
                                            </FormControl>
                                            <SelectContent>
                                                <SelectItem value="4WH">4WH</SelectItem>
                                                <SelectItem value="4WJ">4WJ</SelectItem>
                                                <SelectItem value="6WH">6WH</SelectItem>
                                                <SelectItem value="10WH">10WH</SelectItem>
                                            </SelectContent>
                                        </Select>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            {/* first mile task ID */}
                            <FormField
                                control={form.control}
                                name="FirstMileTaskId"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("firstMile.task.taskId")}</FormLabel>
                                        <FormControl>
                                            <Input placeholder={t("firstMile.task.autoGenerated")} {...field} readOnly className="bg-muted" />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                        </div>

                        <div className="border-t pt-2 mt-2">
                            <h3 className="text-sm font-medium mb-3">{t("firstMile.task.driverInfo")}</h3>
                            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                                <FormField
                                    control={form.control}
                                    name="driverName"
                                    render={({ field }) => (
                                        <FormItem>
                                            <FormLabel>{t("firstMile.task.driverName")}</FormLabel>
                                            <Select
                                                onValueChange={(val) => {
                                                    const selectedDriver = drivers.find(d => d.id === val);
                                                    if (selectedDriver) {
                                                        field.onChange(`${selectedDriver.firstName} ${selectedDriver.lastName}`);
                                                        form.setValue("driverId", selectedDriver.id);
                                                        form.setValue("driverPhone", selectedDriver.mobile || "");
                                                        // Auto-fill plate if available from current assignment
                                                        if (selectedDriver.currentAssignment?.truckPlate) {
                                                            form.setValue("licensePlate", selectedDriver.currentAssignment.truckPlate);
                                                        }
                                                    } else {
                                                        field.onChange(val);
                                                    }
                                                }}
                                                value={drivers.find(d => `${d.firstName} ${d.lastName}` === field.value)?.id || ""}
                                            >
                                                <FormControl>
                                                    <SelectTrigger>
                                                        <SelectValue placeholder={t("firstMile.task.selectDriver")}>
                                                            {field.value || t("firstMile.task.selectDriver")}
                                                        </SelectValue>
                                                    </SelectTrigger>
                                                </FormControl>
                                                <SelectContent>
                                                    {drivers.map((driver) => (
                                                        <SelectItem key={driver.id} value={driver.id || "unknown"}>
                                                            {driver.firstName} {driver.lastName}
                                                        </SelectItem>
                                                    ))}
                                                </SelectContent>
                                            </Select>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                                <FormField
                                    control={form.control}
                                    name="driverPhone"
                                    render={({ field }) => (
                                        <FormItem>
                                            <FormLabel>{t("firstMile.task.phone")}</FormLabel>
                                            <FormControl>
                                                <Input placeholder="09xxxxxxx" {...field} readOnly className="bg-muted" />
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                                <FormField
                                    control={form.control}
                                    name="licensePlate"
                                    render={({ field }) => (
                                        <FormItem>
                                            <FormLabel>{t("firstMile.task.licensePlate")}</FormLabel>
                                            <FormControl>
                                                <Input placeholder="e.g. 1กก-1234" {...field} readOnly className="bg-muted" />
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                            </div>
                        </div>

                        <DialogFooter>
                            <Button type="button" variant="outline" onClick={() => setIsOpen(false)}>{t("firstMile.task.cancel")}</Button>
                            <Button type="submit" disabled={loading}>
                                {loading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                {mode === "create" ? t("firstMile.task.create") : t("firstMile.task.save")}
                            </Button>
                        </DialogFooter>
                    </form>
                </Form>
            </DialogContent>
        </Dialog>
    );
}
