"use client";

import { useState } from "react";
import { format } from "date-fns";
import { CalendarIcon, Loader2, Check, ChevronsUpDown, Search } from "lucide-react";
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
    SelectGroup,
    SelectItem,
    SelectLabel,
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
import { useFirstMileTask } from "../hooks/useFirstMileTask";
import { Task as FirstMileTask } from "@/validate/taskSchema";
import DeliveryStopsEditor from "./DeliveryStopsEditor";

export interface FirstMileTaskDialogProps {
    mode: "create" | "edit";
    task?: Partial<FirstMileTask>;
    trigger?: React.ReactNode;
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    onSuccess?: () => void;
}

export default function FirstMileTaskDialog({ mode, task, trigger, open, onOpenChange, onSuccess }: FirstMileTaskDialogProps) {
    const [internalOpen, setInternalOpen] = useState(false);
    const isOpen = open !== undefined ? open : internalOpen;
    const setIsOpen = onOpenChange || setInternalOpen;
    const { t } = useLanguage();

    const {
        form,
        loading,
        hubOptions,
        socOptions,
        customerOptions,
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
    } = useFirstMileTask({ mode, task, isOpen, setIsOpen, onSuccess });

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
                                                            <span>{t("firstMile.task.pickDate")}</span>
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
                                        <FormLabel>{t("firstMile.task.time")}</FormLabel>
                                        <Select onValueChange={field.onChange} value={field.value ?? "15:00"}>
                                            <FormControl>
                                                <SelectTrigger>
                                                    <SelectValue placeholder={t("firstMile.task.selectTime")} />
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
                            {/* Source Hub - Searchable Dropdown */}
                            <FormField
                                control={form.control}
                                name="sourceHub"
                                render={({ field }) => {
                                    const filteredHubs = hubOptions.filter((hub) => {
                                        const val = hub['Hub Code'] ?? '';
                                        const name = hub['Hub Name'] ?? '';
                                        const nameTh = hub['Hub Name Th'] ?? '';
                                        const searchLower = hubSearch.toLowerCase();
                                        return (
                                            val.toString().toLowerCase().includes(searchLower) ||
                                            name.toString().toLowerCase().includes(searchLower) ||
                                            nameTh.toString().toLowerCase().includes(searchLower)
                                        );
                                    });

                                    return (
                                        <FormItem className="flex flex-col relative">
                                            <FormLabel>{t("firstMile.task.sourceHub")}</FormLabel>
                                            <FormControl>
                                                <Button
                                                    type="button"
                                                    variant="outline"
                                                    role="combobox"
                                                    className={cn(
                                                        "w-full justify-between h-auto min-h-10 py-2",
                                                        !field.value && "text-muted-foreground"
                                                    )}
                                                    onClick={() => {
                                                        setHubDropdownOpen(!hubDropdownOpen);
                                                        setHubSearch("");
                                                    }}
                                                >
                                                    <div className="flex w-full items-center gap-2 min-w-0">
                                                        <div className="flex-1 min-w-0 text-left">
                                                            {field.value ? (
                                                                (() => {
                                                                    const h = hubOptions.find(
                                                                        (hub) => (hub['Hub Code'] ?? '') === field.value
                                                                    );
                                                                    const val = h ? (h['Hub Code'] ?? '') : field.value;
                                                                    const primary = h
                                                                        ? String(
                                                                              h['Hub Name Th'] || h['Hub Name'] || val
                                                                          )
                                                                        : field.value;
                                                                    return (
                                                                        <span className="block truncate font-medium">
                                                                            {primary}
                                                                        </span>
                                                                    );
                                                                })()
                                                            ) : (
                                                                t("firstMile.task.selectHub")
                                                            )}
                                                        </div>
                                                        <ChevronsUpDown className="h-4 w-4 shrink-0 opacity-50" />
                                                    </div>
                                                </Button>
                                            </FormControl>
                                            {hubDropdownOpen && (
                                                <>
                                                    <div className="fixed inset-0 z-40" onClick={() => setHubDropdownOpen(false)} />
                                                    <div className="absolute top-[calc(100%+4px)] left-0 w-full z-50 rounded-md border bg-popover text-popover-foreground shadow-md">
                                                        <div className="flex items-center border-b px-3">
                                                            <Search className="mr-2 h-4 w-4 shrink-0 opacity-50" />
                                                            <input
                                                                className="flex h-10 w-full rounded-md bg-transparent py-3 text-sm outline-none placeholder:text-muted-foreground"
                                                                placeholder={t("firstMile.task.searchHub")}
                                                                value={hubSearch}
                                                                onChange={(e) => setHubSearch(e.target.value)}
                                                                autoFocus
                                                            />
                                                        </div>
                                                        <div className="max-h-[200px] overflow-y-auto p-1">
                                                            {filteredHubs.length === 0 ? (
                                                                <div className="py-4 text-center text-sm text-muted-foreground">
                                                                    {t("firstMile.task.noHub")}
                                                                </div>
                                                            ) : (
                                                                filteredHubs.slice(0, 100).map((hub, idx) => {
                                                                    const val = hub['Hub Code'] ?? '';
                                                                    const primary = String(
                                                                        hub['Hub Name Th'] || hub['Hub Name'] || val
                                                                    );
                                                                    if (!val) return null;
                                                                    return (
                                                                        <div
                                                                            key={`${val}-${idx}`}
                                                                            className="relative flex cursor-pointer select-none items-start rounded-sm px-2 py-1.5 text-sm hover:bg-accent hover:text-accent-foreground"
                                                                            onClick={() => {
                                                                                form.setValue("sourceHub", val as any);
                                                                                setHubDropdownOpen(false);
                                                                                setHubSearch("");
                                                                            }}
                                                                        >
                                                                            <Check
                                                                                className={cn(
                                                                                    "mr-2 h-4 w-4 mt-0.5 shrink-0",
                                                                                    val === field.value
                                                                                        ? "opacity-100"
                                                                                        : "opacity-0"
                                                                                )}
                                                                            />
                                                                            <div className="min-w-0 flex-1">
                                                                                <span className="block font-medium leading-tight">
                                                                                    {primary}
                                                                                </span>
                                                                            </div>
                                                                        </div>
                                                                    );
                                                                })
                                                            )}
                                                        </div>
                                                    </div>
                                                </>
                                            )}
                                            <FormMessage />
                                        </FormItem>
                                    );
                                }}
                            />

                            {/* Destination (SOC) */}
                            <FormField
                                control={form.control}
                                name="destination"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("firstMile.task.destination")}</FormLabel>
                                        <Select
                                            onValueChange={field.onChange}
                                            value={(() => {
                                                const v = field.value ?? "";
                                                if (!v) return "";
                                                if (socOptions.some((s) => s.source_id === v)) return v;
                                                const normalized = normalizeSocIdToKey(v);
                                                const matched = socOptions.find((s) => normalizeSocIdToKey(s.source_id) === normalized);
                                                return matched?.source_id ?? "";
                                            })()}
                                        >
                                            <FormControl>
                                                <SelectTrigger>
                                                    <SelectValue placeholder={t("firstMile.task.selectSOC")} />
                                                </SelectTrigger>
                                            </FormControl>
                                            <SelectContent className="z-[1005]" position="popper">
                                                {socOptions.length === 0 ? (
                                                    <SelectGroup>
                                                        <SelectLabel className="text-muted-foreground">
                                                            {t("firstMile.task.noSOC", "No SOC locations. Add pickup points with station type SOC.")}
                                                        </SelectLabel>
                                                    </SelectGroup>
                                                ) : (
                                                    socOptions.map((soc) => (
                                                        <SelectItem key={soc.source_id} value={soc.source_id}>
                                                            {soc.name || soc.source_id}
                                                        </SelectItem>
                                                    ))
                                                )}
                                            </SelectContent>
                                        </Select>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                        </div>

                        {/* Multi-Delivery Toggle */}
                        <FormField
                            control={form.control}
                            name="isMultiDelivery"
                            render={({ field }) => (
                                <FormItem className="flex items-center gap-2">
                                    <FormControl>
                                        <Checkbox checked={field.value} onCheckedChange={field.onChange} />
                                    </FormControl>
                                    <div className="flex-1">
                                        <FormLabel className="cursor-pointer">
                                            {t("firstMile.task.isMultiDelivery")}
                                        </FormLabel>
                                        <p className="text-xs text-muted-foreground">
                                            {t("firstMile.task.isMultiDeliveryHint")}
                                        </p>
                                    </div>
                                </FormItem>
                            )}
                        />

                        {/* Delivery Stops (show if multi-delivery enabled) */}
                        {form.watch("isMultiDelivery") && (
                            <FormItem>
                                <FormLabel>{t("firstMile.task.deliveryStops")}</FormLabel>
                                <p className="text-xs text-muted-foreground mb-3">
                                    {t("firstMile.task.deliveryStopsHint")}
                                </p>
                                <DeliveryStopsEditor
                                    socOptions={socOptions}
                                    customerOptions={customerOptions}
                                />
                            </FormItem>
                        )}

                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                            {/* Truck Type */}
                            <FormField
                                control={form.control}
                                name="truckType"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("firstMile.task.truckType")}</FormLabel>
                                        <Select onValueChange={field.onChange} value={field.value ?? "PICKUP"}>
                                            <FormControl>
                                                <SelectTrigger>
                                                    <SelectValue placeholder={t("firstMile.task.selectTruckType")} />
                                                </SelectTrigger>
                                            </FormControl>
                                            <SelectContent className="z-[1005]" position="popper">
                                                <SelectItem value="PICKUP">Pickup</SelectItem>
                                                <SelectItem value="4WJ">4WJ</SelectItem>
                                                <SelectItem value="6WH">6WH</SelectItem>
                                                <SelectItem value="10WH">10WH</SelectItem>
                                                <SelectItem value="18WH">18WH</SelectItem>
                                                <SelectItem value="VAN">Van</SelectItem>
                                            </SelectContent>
                                        </Select>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            {/* task ID */}
                            <FormField
                                control={form.control}
                                name="taskId"
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
                                                    if (val === "__none__" || !val) {
                                                        field.onChange("");
                                                        form.setValue("driverId", "");
                                                        form.setValue("driverPhone", "");
                                                        form.setValue("licensePlate", "");
                                                        return;
                                                    }
                                                    const selectedDriver = drivers.find(d => d.id === val);
                                                    if (selectedDriver) {
                                                        field.onChange(`${selectedDriver.firstName} ${selectedDriver.lastName}`);
                                                        form.setValue("driverId", selectedDriver.id);
                                                        form.setValue("driverPhone", selectedDriver.mobile || "");
                                                        if (selectedDriver.currentAssignment?.truckPlate) {
                                                            form.setValue("licensePlate", selectedDriver.currentAssignment.truckPlate);
                                                        }
                                                    } else {
                                                        field.onChange(val);
                                                    }
                                                }}
                                                value={drivers.find(d => `${d.firstName} ${d.lastName}` === field.value)?.id ?? "__none__"}
                                            >
                                                <FormControl>
                                                    <SelectTrigger>
                                                        <SelectValue placeholder={t("firstMile.task.selectDriver")}>
                                                            {field.value || t("firstMile.task.selectDriver")}
                                                        </SelectValue>
                                                    </SelectTrigger>
                                                </FormControl>
                                                <SelectContent className="z-[1005]" position="popper">
                                                    <SelectItem value="__none__">{t("firstMile.task.selectDriver")}</SelectItem>
                                                    {(() => {
                                                        const getMappedTruckType = (fmType: string) => {
                                                            if (fmType === "4WH" || fmType === "4WJ") return "4 Wheels Jumbo";
                                                            if (fmType === "6WH") return "6 Wheels";
                                                            if (fmType === "10WH") return "10 Wheels";
                                                            if (fmType === "18WH") return "18 Wheels";
                                                            if (fmType === "PICKUP") return "Pickup";
                                                            if (fmType === "VAN") return "Van";
                                                            return fmType;
                                                        };
                                                        const filtered = drivers.filter(driver => {
                                                            if (!watchedTruckType) return true;
                                                            const targetType = getMappedTruckType(watchedTruckType);
                                                            const assignedTruckId = driver.currentAssignment?.truckId;
                                                            if (!assignedTruckId) return false;
                                                            const truck = trucks.find(t => t.id === assignedTruckId);
                                                            if (!truck) return false;
                                                            if (targetType === "4 Wheels Jumbo") {
                                                                return truck.type === "4 Wheels" || truck.type === "4 Wheels Jumbo";
                                                            }
                                                            return truck.type === targetType;
                                                        });
                                                        const list = mode === "edit" && filtered.length === 0 ? drivers : filtered;
                                                        return list.map((driver) => {
                                                            const isActive = driver.id ? activeTaskDriverIds.has(driver.id) : false;
                                                            return (
                                                                <SelectItem key={driver.id} value={driver.id || "unknown"}>
                                                                    <span className="flex items-center gap-2">
                                                                        {driver.firstName} {driver.lastName}
                                                                        {isActive && (
                                                                            <span className="text-xs bg-orange-100 text-orange-700 px-1.5 py-0.5 rounded-full font-medium">
                                                                                {t("firstMile.task.driverOnRun")}
                                                                            </span>
                                                                        )}
                                                                    </span>
                                                                </SelectItem>
                                                            );
                                                        });
                                                    })()}
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
