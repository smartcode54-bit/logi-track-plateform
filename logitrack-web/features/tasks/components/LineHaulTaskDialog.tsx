"use client";

import { useState } from "react";
import { format } from "date-fns";
import { CalendarIcon, Loader2, Check, ChevronsUpDown, Search } from "lucide-react";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
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
import { cn } from "@/lib/utils";
import { useLanguage } from "@/context/language";
import { useLineHaulTask } from "../hooks/useLineHaulTask";
import DeliveryStopsEditor from "./DeliveryStopsEditor";
import { HelperDriverField } from "./HelperDriverField";
import { TruckPlateField } from "./TruckPlateField";
import { taskTruckTypeFromTruckDoc } from "@/lib/truckType";
import { driverDisplayName, matchDriverOptionId } from "@/lib/driverName";
import { Task as FirstMileTask, TASK_TRUCK_TYPE_ENUM } from "@/validate/taskSchema";

export interface LineHaulTaskDialogProps {
    mode: "create" | "edit";
    task?: Partial<FirstMileTask>;
    trigger?: React.ReactNode;
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    onSuccess?: () => void;
}

export default function LineHaulTaskDialog({ mode, task, trigger, open, onOpenChange, onSuccess }: LineHaulTaskDialogProps) {
    const [internalOpen, setInternalOpen] = useState(false);
    const isOpen = open !== undefined ? open : internalOpen;
    const setIsOpen = onOpenChange || setInternalOpen;
    const { t } = useLanguage();

    const {
        form,
        loading,
        hubOptions,
        socOptions,
        trucks,
        drivers,
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
        customerOptions
    } = useLineHaulTask({ mode, task, isOpen, setIsOpen, onSuccess });

    return (
        <Dialog open={isOpen} onOpenChange={setIsOpen}>
            {trigger && <DialogTrigger asChild>{trigger}</DialogTrigger>}
            <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
                <DialogHeader>
                    <DialogTitle>{mode === "create" ? "Create Line Haul Task" : "Edit Line Haul Task"}</DialogTitle>
                    <DialogDescription>
                        {mode === "create" ? "Add a new line haul task assignment." : "Update an existing line haul task assignment."}
                    </DialogDescription>
                </DialogHeader>

                <Form {...form}>
                    <form onSubmit={form.handleSubmit(onSubmit, createInvalidHandler(form, t))} className="space-y-4">
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                            {/* Date Field */}
                            <FormField
                                control={form.control}
                                name="date"
                                render={({ field }) => (
                                    <FormItem className="flex flex-col">
                                        <FormLabel>Date</FormLabel>
                                        <Popover modal={true}>
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
                                                            <span>Pick a date</span>
                                                        )}
                                                        <CalendarIcon className="ml-auto h-4 w-4 opacity-50" />
                                                    </Button>
                                                </FormControl>
                                            </PopoverTrigger>
                                            <PopoverContent className="w-auto p-0 z-[1005]" align="start">
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
                                        <FormLabel>Time</FormLabel>
                                        <Select onValueChange={field.onChange} value={field.value ?? "15:00"}>
                                            <FormControl>
                                                <SelectTrigger>
                                                    <SelectValue placeholder="Select Time" />
                                                </SelectTrigger>
                                            </FormControl>
                                            <SelectContent className="max-h-[200px] z-[1005]" position="popper">
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
                            {/* Source Field (SOC) */}
                            <FormField
                                control={form.control}
                                name="sourceHub"
                                render={({ field }) => {
                                    const resolvedValue = (() => {
                                        const v = field.value ?? "";
                                        if (!v) return "";
                                        if (socOptions.some((s) => s.source_id === v)) return v;
                                        const normalized = normalizeSocIdToKey(v);
                                        return socOptions.find((s) => normalizeSocIdToKey(s.source_id) === normalized)?.source_id ?? "";
                                    })();
                                    const selectedSoc = socOptions.find((s) => s.source_id === resolvedValue);
                                    const filteredSocs = socOptions.filter((s) => {
                                        const q = socSearch.toLowerCase();
                                        return s.source_id.toLowerCase().includes(q) || s.name.toLowerCase().includes(q);
                                    });
                                    return (
                                        <FormItem className="flex flex-col">
                                            <FormLabel>Source (SOC)</FormLabel>
                                            <Popover open={socDropdownOpen} onOpenChange={(o) => { setSocDropdownOpen(o); if (o) setSocSearch(""); }} modal={true}>
                                                <PopoverTrigger asChild>
                                                    <FormControl>
                                                        <Button
                                                            type="button"
                                                            variant="outline"
                                                            role="combobox"
                                                            className={cn("w-full justify-between h-auto min-h-10 py-2", !resolvedValue && "text-muted-foreground")}
                                                        >
                                                            <span className="block truncate text-left flex-1 min-w-0">
                                                                {selectedSoc ? selectedSoc.name || selectedSoc.source_id : "Select Source (SOC)"}
                                                            </span>
                                                            <ChevronsUpDown className="h-4 w-4 shrink-0 opacity-50 ml-2" />
                                                        </Button>
                                                    </FormControl>
                                                </PopoverTrigger>
                                                <PopoverContent className="w-[--radix-popover-trigger-width] p-0 z-[1010]" align="start">
                                                    <div className="flex items-center border-b px-3">
                                                        <Search className="mr-2 h-4 w-4 shrink-0 opacity-50" />
                                                        <input
                                                            className="flex h-10 w-full rounded-md bg-transparent py-3 text-sm outline-none placeholder:text-muted-foreground"
                                                            placeholder="Search SOC..."
                                                            value={socSearch}
                                                            onChange={(e) => setSocSearch(e.target.value)}
                                                            autoFocus
                                                        />
                                                    </div>
                                                    <div className="max-h-[220px] overflow-y-auto p-1">
                                                        {filteredSocs.length === 0 ? (
                                                            <div className="py-4 text-center text-sm text-muted-foreground">No SOCs found.</div>
                                                        ) : (
                                                            filteredSocs.map((soc) => (
                                                                <div
                                                                    key={soc.source_id}
                                                                    onClick={() => { field.onChange(soc.source_id); setSocDropdownOpen(false); }}
                                                                    className={cn(
                                                                        "relative flex cursor-default select-none items-center rounded-sm px-2 py-1.5 text-sm outline-none hover:bg-accent hover:text-accent-foreground",
                                                                        resolvedValue === soc.source_id && "bg-accent"
                                                                    )}
                                                                >
                                                                    <Check className={cn("mr-2 h-4 w-4 shrink-0", resolvedValue === soc.source_id ? "opacity-100" : "opacity-0")} />
                                                                    <span>{soc.name || soc.source_id}</span>
                                                                </div>
                                                            ))
                                                        )}
                                                    </div>
                                                </PopoverContent>
                                            </Popover>
                                            <FormMessage />
                                        </FormItem>
                                    );
                                }}
                            />

                            {/* Destination Field (Hub) */}
                            <FormField
                                control={form.control}
                                name="destination"
                                render={({ field }) => {
                                    const filteredHubs = hubOptions.filter((hub) => {
                                        const val = String(hub['Hub Code'] ?? '');
                                        const name = String(hub['Hub Name'] ?? '');
                                        const nameTh = String(hub['Hub Name Th'] ?? '');
                                        const q = hubSearch.toLowerCase();
                                        return val.toLowerCase().includes(q) || name.toLowerCase().includes(q) || nameTh.toLowerCase().includes(q);
                                    });
                                    const selectedHub = hubOptions.find((h) => (h['Hub Code'] ?? '') === field.value);
                                    const selectedLabel = selectedHub
                                        ? String(selectedHub['Hub Name Th'] || selectedHub['Hub Name'] || selectedHub['Hub Code'] || '')
                                        : field.value || '';

                                    return (
                                        <FormItem className="flex flex-col">
                                            <FormLabel>Destination (Hub)</FormLabel>
                                            <Popover open={hubDropdownOpen} onOpenChange={(o) => { setHubDropdownOpen(o); if (o) setHubSearch(""); }} modal={true}>
                                                <PopoverTrigger asChild>
                                                    <FormControl>
                                                        <Button
                                                            type="button"
                                                            variant="outline"
                                                            role="combobox"
                                                            className={cn("w-full justify-between h-auto min-h-10 py-2", !field.value && "text-muted-foreground")}
                                                        >
                                                            <span className="block truncate text-left flex-1 min-w-0">
                                                                {selectedLabel || "Select Destination Hub"}
                                                            </span>
                                                            <ChevronsUpDown className="h-4 w-4 shrink-0 opacity-50 ml-2" />
                                                        </Button>
                                                    </FormControl>
                                                </PopoverTrigger>
                                                <PopoverContent className="w-[--radix-popover-trigger-width] p-0 z-[1010]" align="start">
                                                    <div className="flex items-center border-b px-3">
                                                        <Search className="mr-2 h-4 w-4 shrink-0 opacity-50" />
                                                        <input
                                                            className="flex h-10 w-full rounded-md bg-transparent py-3 text-sm outline-none placeholder:text-muted-foreground"
                                                            placeholder="Search Hub..."
                                                            value={hubSearch}
                                                            onChange={(e) => setHubSearch(e.target.value)}
                                                            autoFocus
                                                        />
                                                    </div>
                                                    <div className="max-h-[220px] overflow-y-auto p-1">
                                                        {filteredHubs.length === 0 ? (
                                                            <div className="py-4 text-center text-sm text-muted-foreground">No hubs found.</div>
                                                        ) : (
                                                            filteredHubs.slice(0, 100).map((hub, idx) => {
                                                                const val = String(hub['Hub Code'] ?? '');
                                                                const primary = String(hub['Hub Name Th'] || hub['Hub Name'] || val);
                                                                return (
                                                                    <div
                                                                        key={val || idx}
                                                                        onClick={() => {
                                                                            if (!val) return;
                                                                            form.setValue("destination", val as any, { shouldValidate: true });
                                                                            setHubDropdownOpen(false);
                                                                        }}
                                                                        className={cn(
                                                                            "relative flex cursor-default select-none items-center rounded-sm px-2 py-1.5 text-sm outline-none hover:bg-accent hover:text-accent-foreground",
                                                                            field.value === val && "bg-accent"
                                                                        )}
                                                                    >
                                                                        <Check className={cn("mr-2 h-4 w-4 shrink-0", field.value === val ? "opacity-100" : "opacity-0")} />
                                                                        <span className="block leading-tight">{primary}</span>
                                                                    </div>
                                                                );
                                                            })
                                                        )}
                                                    </div>
                                                </PopoverContent>
                                            </Popover>
                                            <FormMessage />
                                        </FormItem>
                                    );
                                }}
                            />
                        </div>

                        {/* Job Category: หลัก/เสริม */}
                        <FormField
                            control={form.control}
                            name="jobCategory"
                            render={({ field }) => (
                                <FormItem>
                                    <FormLabel>{t("firstMile.task.jobCategory.label", "Job Category")}</FormLabel>
                                    <Select
                                        onValueChange={field.onChange}
                                        value={field.value ?? "PRIMARY"}
                                    >
                                        <FormControl>
                                            <SelectTrigger>
                                                <SelectValue />
                                            </SelectTrigger>
                                        </FormControl>
                                        <SelectContent className="z-[1005]" position="popper">
                                            <SelectItem value="PRIMARY">{t("firstMile.task.jobCategory.primary", "Primary")}</SelectItem>
                                            <SelectItem value="SUPPLEMENTARY">{t("firstMile.task.jobCategory.supplementary", "Supplementary")}</SelectItem>
                                        </SelectContent>
                                    </Select>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />

                        {/* Multi-Delivery Toggle */}
                        <FormField
                            control={form.control}
                            name="isMultiDelivery"
                            render={({ field }) => (
                                <FormItem className="flex items-center gap-3 rounded-lg border p-4">
                                    <FormControl>
                                        <Checkbox
                                            checked={field.value ?? false}
                                            onCheckedChange={field.onChange}
                                        />
                                    </FormControl>
                                    <div className="flex-1">
                                        <FormLabel className="cursor-pointer font-medium">{t("firstMile.multiDelivery.isMultiDelivery", "Multi-Delivery Task")}</FormLabel>
                                        <p className="text-sm text-muted-foreground mt-1">{t("firstMile.multiDelivery.description", "One pickup point, multiple delivery stops")}</p>
                                    </div>
                                </FormItem>
                            )}
                        />

                        {form.watch("isMultiDelivery") && (
                            <DeliveryStopsEditor
                                socOptions={socOptions}
                                customerOptions={customerOptions}
                            />
                        )}

                        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                            {/* Truck Type — narrows the plate list below. */}
                            <FormField
                                control={form.control}
                                name="truckType"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>Truck Type</FormLabel>
                                        <Select
                                            onValueChange={(val) => {
                                                field.onChange(val);
                                                // Drop a plate that no longer matches the type, so the two never disagree.
                                                const picked = trucks.find((truck) => truck.id === form.getValues("truckId"));
                                                if (picked && taskTruckTypeFromTruckDoc(picked.type) !== val) {
                                                    form.setValue("truckId", "");
                                                    form.setValue("licensePlate", "");
                                                }
                                            }}
                                            value={field.value ?? "4W"}
                                        >
                                            <FormControl>
                                                <SelectTrigger>
                                                    <SelectValue placeholder="Select Truck Type" />
                                                </SelectTrigger>
                                            </FormControl>
                                            <SelectContent className="z-[1005]" position="popper">
                                                {TASK_TRUCK_TYPE_ENUM.map((truckType) => (
                                                    <SelectItem key={truckType} value={truckType}>{truckType}</SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            {/* License plate — the actual vehicle for this job, picked from the fleet. */}
                            <FormField
                                control={form.control}
                                name="truckId"
                                render={({ field }) => (
                                    <TruckPlateField
                                        trucks={trucks}
                                        truckType={watchedTruckType}
                                        value={field.value}
                                        licensePlate={form.watch("licensePlate")}
                                        onSelect={(truck) => {
                                            field.onChange(truck.id);
                                            form.setValue("licensePlate", truck.licensePlate ?? "");
                                            // Re-derive the class from the truck doc — billing reads truckType.
                                            const derived = taskTruckTypeFromTruckDoc(truck.type);
                                            if (derived) form.setValue("truckType", derived);
                                        }}
                                    />
                                )}
                            />

                            {/* task ID */}
                            <FormField
                                control={form.control}
                                name="taskId"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>Task ID</FormLabel>
                                        <FormControl>
                                            <Input placeholder="Auto-generated" {...field} readOnly className="bg-muted" />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                        </div>

                        <div className="border-t pt-2 mt-2">
                            <h3 className="text-sm font-medium mb-3">Driver Info</h3>
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                                <FormField
                                    control={form.control}
                                    name="driverName"
                                    render={({ field }) => (
                                        <FormItem>
                                            <FormLabel>Driver Name</FormLabel>
                                            <Select
                                                onValueChange={(val) => {
                                                    if (val === "__none__" || !val) {
                                                        field.onChange("");
                                                        form.setValue("driverId", "");
                                                        form.setValue("driverPhone", "");
                                                        return;
                                                    }
                                                    const selectedDriver = drivers.find(d => d.id === val);
                                                    if (selectedDriver) {
                                                        // Store the THAI name — see the same note in FirstMileTaskDialog:
                                                        // `tasks.driverName` is billing's last-resort fallback.
                                                        field.onChange(driverDisplayName(selectedDriver, selectedDriver.id));
                                                        form.setValue("driverId", selectedDriver.id);
                                                        form.setValue("driverPhone", selectedDriver.mobile || "");
                                                        // The driver's home truck is a DEFAULT only — it never overrides a
                                                        // plate already picked for this job, and never filters this list.
                                                        const home = selectedDriver.currentAssignment;
                                                        if (home?.truckId && !form.getValues("truckId")) {
                                                            const homeTruck = trucks.find((truck) => truck.id === home.truckId);
                                                            const derived = taskTruckTypeFromTruckDoc(homeTruck?.type);
                                                            form.setValue("truckId", home.truckId);
                                                            form.setValue("licensePlate", home.truckPlate ?? homeTruck?.licensePlate ?? "");
                                                            if (derived) form.setValue("truckType", derived);
                                                        }
                                                    } else {
                                                        field.onChange(val);
                                                    }
                                                }}
                                                value={matchDriverOptionId(drivers, form.watch("driverId"), field.value) ?? "__none__"}
                                            >
                                                <FormControl>
                                                    <SelectTrigger>
                                                        <SelectValue placeholder="Select Driver">
                                                            {field.value || "Select Driver"}
                                                        </SelectValue>
                                                    </SelectTrigger>
                                                </FormControl>
                                                <SelectContent className="z-[1005]" position="popper">
                                                    <SelectItem value="__none__">Select Driver</SelectItem>
                                                    {drivers.map((driver) => {
                                                        const isActive = driver.id ? activeTaskDriverIds.has(driver.id) : false;
                                                        return (
                                                            <SelectItem key={driver.id} value={driver.id || "unknown"}>
                                                                <span className="flex items-center gap-2">
                                                                    {driverDisplayName(driver, driver.id)}
                                                                    {isActive && (
                                                                        <span className="text-xs bg-orange-100 text-orange-700 px-1.5 py-0.5 rounded-full font-medium">
                                                                            {t("firstMile.task.driverOnRun")}
                                                                        </span>
                                                                    )}
                                                                </span>
                                                            </SelectItem>
                                                        );
                                                    })}
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
                                            <FormLabel>Phone</FormLabel>
                                            <FormControl>
                                                <Input placeholder="09xxxxxxx" {...field} readOnly className="bg-muted" />
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                            </div>

                            {/* Helper (training / assisting) — at most one, stores Auth UID. See ADR-0001. */}
                            <div className="mt-4">
                                <FormField
                                    control={form.control}
                                    name="helperDriverIds"
                                    render={({ field }) => (
                                        <FormItem>
                                            <FormControl>
                                                <HelperDriverField
                                                    drivers={drivers}
                                                    value={field.value as string[] | undefined}
                                                    onChange={field.onChange}
                                                    excludeDriverId={form.watch("driverId") || undefined}
                                                    label={t("task.helper.label", "Helper (training / assisting)")}
                                                    placeholder={t("task.helper.select", "Select helper")}
                                                    noneLabel={t("task.helper.none", "No helper")}
                                                    searchPlaceholder={t("task.helper.search", "Search driver")}
                                                />
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                            </div>
                        </div>

                        {mode === "edit" && task && (
                            <div className="border-t pt-4 space-y-3">
                                <h3 className="text-sm font-medium">{t("firstMile.task.checkInPhoto", "Check-in Photo")}</h3>
                                <div className="flex flex-wrap items-start gap-4">
                                    {task.checkInPhotoUrl && (
                                        <div className="space-y-1">
                                            <a href={task.checkInPhotoUrl} target="_blank" rel="noopener noreferrer" className="text-primary text-sm underline block">View current photo</a>
                                            <img src={task.checkInPhotoUrl} alt="Check-in" className="max-h-32 rounded border object-cover" />
                                        </div>
                                    )}
                                    <div className="flex flex-col gap-2">
                                        <label className="text-sm text-muted-foreground">{t("firstMile.task.replacePhoto", "Replace photo (when work was recorded incorrectly)")}</label>
                                        <input
                                            type="file"
                                            accept="image/*"
                                            className="text-sm"
                                            onChange={(e) => setNewCheckInPhotoFile(e.target.files?.[0] ?? null)}
                                        />
                                        {newCheckInPhotoFile && (
                                            <span className="text-xs text-muted-foreground">{newCheckInPhotoFile.name}</span>
                                        )}
                                    </div>
                                </div>
                            </div>
                        )}

                        <DialogFooter>
                            <Button type="button" variant="outline" onClick={() => setIsOpen(false)}>Cancel</Button>
                            <Button type="submit" disabled={loading}>
                                {loading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                {mode === "create" ? "Create Assignment" : "Save Changes"}
                            </Button>
                        </DialogFooter>
                    </form>
                </Form>
            </DialogContent>
        </Dialog>
    );
}
