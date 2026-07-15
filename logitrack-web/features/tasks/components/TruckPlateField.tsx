"use client";

import { useMemo, useState } from "react";
import { Check, ChevronsUpDown, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { FormControl, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { cn } from "@/lib/utils";
import { useLanguage } from "@/context/language";
import { taskTruckTypeFromTruckDoc, truckMatchesTaskTruckType } from "@/lib/truckType";
import type { TaskTruck } from "../services/taskService";
import type { TaskTruckType } from "@/validate/taskSchema";

export interface TruckPlateFieldProps {
    trucks: TaskTruck[];
    /** Currently selected truck type — filters the list. */
    truckType?: TaskTruckType;
    /** Selected trucks/{id}. */
    value?: string;
    /** Denormalized plate, shown when the truck doc is missing (e.g. a legacy task). */
    licensePlate?: string;
    onSelect: (truck: TaskTruck) => void;
}

/**
 * Picks the vehicle for a task from the trucks fleet — the per-task truck, not the driver's binding.
 * Selecting a plate is what sets tasks.truckId; the caller re-derives truckType from the truck doc
 * so the two can never disagree (billing reads tasks.truckType to select the rate card).
 */
export function TruckPlateField({ trucks, truckType, value, licensePlate, onSelect }: TruckPlateFieldProps) {
    const { t } = useLanguage();
    const [open, setOpen] = useState(false);
    const [search, setSearch] = useState("");

    const selected = useMemo(() => trucks.find((truck) => truck.id === value), [trucks, value]);

    const matching = useMemo(() => {
        const term = search.trim().toLowerCase();
        return trucks
            .filter((truck) => truckMatchesTaskTruckType(truck.type, truckType))
            .filter((truck) => {
                if (!term) return true;
                const haystack = `${truck.licensePlate ?? ""} ${truck.model ?? ""} ${truck.type ?? ""}`.toLowerCase();
                return haystack.includes(term);
            });
    }, [trucks, truckType, search]);

    // A legacy task can carry a plate with no truckId, or a plate whose truck was deleted.
    const orphanPlate = !selected && !!licensePlate;

    return (
        <FormItem className="flex flex-col relative">
            <FormLabel>{t("firstMile.task.licensePlate")}</FormLabel>
            <FormControl>
                <Button
                    type="button"
                    variant="outline"
                    role="combobox"
                    className={cn(
                        "w-full justify-between h-auto min-h-10 py-2",
                        !selected && !orphanPlate && "text-muted-foreground"
                    )}
                    onClick={() => {
                        setOpen(!open);
                        setSearch("");
                    }}
                >
                    <div className="flex w-full items-center gap-2 min-w-0">
                        <div className="flex-1 min-w-0 text-left">
                            {selected ? (
                                <span className="block truncate font-medium">
                                    {selected.licensePlate}
                                    <span className="ml-2 text-xs font-normal text-muted-foreground">
                                        {taskTruckTypeFromTruckDoc(selected.type) ?? selected.type}
                                    </span>
                                </span>
                            ) : orphanPlate ? (
                                <span className="block truncate font-medium">
                                    {licensePlate}
                                    <span className="ml-2 text-xs font-normal text-amber-600">
                                        {t("firstMile.task.truckNotInFleet")}
                                    </span>
                                </span>
                            ) : (
                                t("firstMile.task.selectTruck")
                            )}
                        </div>
                        <ChevronsUpDown className="h-4 w-4 shrink-0 opacity-50" />
                    </div>
                </Button>
            </FormControl>
            {open && (
                <>
                    <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
                    <div className="absolute top-[calc(100%+4px)] left-0 w-full z-50 rounded-md border bg-popover text-popover-foreground shadow-md">
                        <div className="flex items-center border-b px-3">
                            <Search className="mr-2 h-4 w-4 shrink-0 opacity-50" />
                            <input
                                className="flex h-10 w-full rounded-md bg-transparent py-3 text-sm outline-none placeholder:text-muted-foreground"
                                placeholder={t("firstMile.task.searchTruck")}
                                value={search}
                                onChange={(e) => setSearch(e.target.value)}
                                autoFocus
                            />
                        </div>
                        <div className="max-h-[200px] overflow-y-auto p-1">
                            {matching.length === 0 ? (
                                <div className="py-4 text-center text-sm text-muted-foreground">
                                    {t("firstMile.task.noTruck")}
                                </div>
                            ) : (
                                matching.slice(0, 100).map((truck) => (
                                    <div
                                        key={truck.id}
                                        className="relative flex cursor-pointer select-none items-start rounded-sm px-2 py-1.5 text-sm hover:bg-accent hover:text-accent-foreground"
                                        onClick={() => {
                                            onSelect(truck);
                                            setOpen(false);
                                            setSearch("");
                                        }}
                                    >
                                        <Check
                                            className={cn(
                                                "mr-2 h-4 w-4 mt-0.5 shrink-0",
                                                truck.id === value ? "opacity-100" : "opacity-0"
                                            )}
                                        />
                                        <div className="min-w-0 flex-1">
                                            <span className="block font-medium leading-tight">
                                                {truck.licensePlate || truck.id}
                                            </span>
                                            <span className="block text-xs text-muted-foreground leading-tight">
                                                {[taskTruckTypeFromTruckDoc(truck.type) ?? truck.type, truck.model]
                                                    .filter(Boolean)
                                                    .join(" · ")}
                                            </span>
                                        </div>
                                    </div>
                                ))
                            )}
                        </div>
                    </div>
                </>
            )}
            <FormMessage />
        </FormItem>
    );
}

export default TruckPlateField;
