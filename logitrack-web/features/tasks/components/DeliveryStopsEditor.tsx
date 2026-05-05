"use client";

import { useState } from "react";
import { Plus, Trash2, GripVertical } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage,
} from "@/components/ui/form";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { useLanguage } from "@/context/language";
import { useFormContext } from "react-hook-form";
import type { DeliveryStop } from "@/lib/../shared-docs/schemas/taskSchema";

interface DeliveryStopsEditorProps {
    socOptions: Array<{ source_id: string; name: string }>;
    customerOptions: Array<{ id: string; name: string; code: string }>;
}

export default function DeliveryStopsEditor({
    socOptions,
    customerOptions,
}: DeliveryStopsEditorProps) {
    const { t } = useLanguage();
    const form = useFormContext();
    const stops = form.watch("deliveryStops") as DeliveryStop[] | undefined;
    const [draggedIndex, setDraggedIndex] = useState<number | null>(null);

    const addStop = () => {
        const currentStops = stops || [];
        const newIndex = currentStops.length + 1;
        const newStop: DeliveryStop = {
            index: newIndex,
            destination: "",
            status: "pending",
        };
        form.setValue("deliveryStops", [...currentStops, newStop]);
    };

    const removeStop = (index: number) => {
        const currentStops = stops || [];
        const filtered = currentStops
            .filter((_, i) => i !== index)
            .map((stop, i) => ({ ...stop, index: i + 1 }));
        form.setValue("deliveryStops", filtered);
    };

    const updateStop = (index: number, field: keyof DeliveryStop, value: unknown) => {
        const currentStops = [...(stops || [])];
        currentStops[index] = { ...currentStops[index], [field]: value };
        form.setValue("deliveryStops", currentStops);
    };

    const moveStop = (fromIndex: number, toIndex: number) => {
        const currentStops = [...(stops || [])];
        const [movedStop] = currentStops.splice(fromIndex, 1);
        currentStops.splice(toIndex, 0, movedStop);
        const reindexed = currentStops.map((stop, i) => ({ ...stop, index: i + 1 }));
        form.setValue("deliveryStops", reindexed);
        setDraggedIndex(null);
    };

    if (!stops || stops.length === 0) {
        return (
            <div className="border border-dashed rounded-md p-4 text-center">
                <p className="text-sm text-muted-foreground mb-2">
                    {t("firstMile.task.noStops")}
                </p>
                <Button type="button" variant="outline" size="sm" onClick={addStop}>
                    <Plus className="h-4 w-4 mr-2" />
                    {t("firstMile.task.addStop")}
                </Button>
            </div>
        );
    }

    return (
        <div className="space-y-3">
            {stops.map((stop, idx) => (
                <div
                    key={idx}
                    className={`border rounded-md p-3 space-y-3 transition-opacity ${
                        draggedIndex === idx ? "opacity-50" : ""
                    }`}
                    draggable
                    onDragStart={() => setDraggedIndex(idx)}
                    onDragOver={(e) => e.preventDefault()}
                    onDrop={() => {
                        if (draggedIndex !== null && draggedIndex !== idx) {
                            moveStop(draggedIndex, idx);
                        }
                    }}
                >
                    <div className="flex items-center gap-2">
                        <GripVertical className="h-4 w-4 text-muted-foreground cursor-move" />
                        <span className="font-semibold text-sm">
                            {t("firstMile.task.stopIndex", { index: idx + 1 })}
                        </span>
                        {idx === 0 && (
                            <span className="text-xs bg-blue-100 text-blue-700 px-2 py-1 rounded">
                                {t("firstMile.task.primaryDestination")}
                            </span>
                        )}
                        <div className="flex-1" />
                        <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={() => removeStop(idx)}
                            className="text-red-600 hover:text-red-700"
                        >
                            <Trash2 className="h-4 w-4" />
                            {t("firstMile.task.removeStop")}
                        </Button>
                    </div>

                    <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                        {/* Destination SOC/Hub */}
                        <div>
                            <FormLabel className="text-xs">
                                {t("firstMile.task.stopDestination")}
                            </FormLabel>
                            <Select
                                value={stop.destination || ""}
                                onValueChange={(value) => updateStop(idx, "destination", value)}
                            >
                                <FormControl>
                                    <SelectTrigger className="h-9">
                                        <SelectValue placeholder={t("firstMile.task.selectSOC")} />
                                    </SelectTrigger>
                                </FormControl>
                                <SelectContent className="z-[1005]" position="popper">
                                    {socOptions.map((soc) => (
                                        <SelectItem key={soc.source_id} value={soc.source_id}>
                                            {soc.name}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        {/* Customer/Partner Link */}
                        <div>
                            <FormLabel className="text-xs">
                                {t("firstMile.task.stopCustomer")}
                            </FormLabel>
                            <Select
                                value={stop.destinationLinkedCustomerId || ""}
                                onValueChange={(customerId) => {
                                    const customer = customerOptions.find((c) => c.id === customerId);
                                    if (customer) {
                                        updateStop(idx, "destinationLinkedCustomerId", customerId);
                                        updateStop(idx, "destinationLinkedCustomerName", customer.name);
                                        updateStop(idx, "destinationLinkedCustomerCode", customer.code);
                                    }
                                }}
                            >
                                <FormControl>
                                    <SelectTrigger className="h-9">
                                        <SelectValue placeholder={t("firstMile.task.stopCustomer")} />
                                    </SelectTrigger>
                                </FormControl>
                                <SelectContent className="z-[1005]" position="popper">
                                    {customerOptions.map((customer) => (
                                        <SelectItem key={customer.id} value={customer.id}>
                                            {customer.name} ({customer.code})
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    </div>
                </div>
            ))}

            <Button type="button" variant="outline" size="sm" onClick={addStop} className="w-full">
                <Plus className="h-4 w-4 mr-2" />
                {t("firstMile.task.addStop")}
            </Button>
        </div>
    );
}
