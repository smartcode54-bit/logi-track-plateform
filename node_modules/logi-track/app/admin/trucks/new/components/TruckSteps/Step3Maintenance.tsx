"use client";

import { useFormContext } from "react-hook-form";
import { TruckFormValues } from "@/validate/truckSchema";
import {
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage,
} from "@/components/ui/form";
import { DatePicker } from "@/components/ui/date-picker";
import { format } from "date-fns";
import { Input } from "@/components/ui/input";
import { EngineInformationSection } from "../EngineCapacitySection";

export function Step3Maintenance() {
    const form = useFormContext<TruckFormValues>();
    const ownershipType = form.watch("ownershipType");

    return (
        <div className="space-y-6">
            <div className="bg-card border rounded-lg p-6 animate-in fade-in slide-in-from-bottom-4 duration-500">
                <h3 className="text-lg font-medium mb-1">Step 3: Engine Specifications</h3>
                <p className="text-sm text-muted-foreground mb-6">Details regarding engine specifications and capacity.</p>

                {/* Engine Details */}
                {ownershipType === 'own' ? (
                    <EngineInformationSection />
                ) : (
                    <div className="p-4 bg-muted/50 rounded-md text-sm text-muted-foreground">
                        Detailed engine information is optional for subcontractor trucks.
                    </div>
                )}
            </div>

            {/* Maintenance Book Section */}
            <div className="bg-card border rounded-lg p-6 animate-in fade-in slide-in-from-bottom-5 duration-500 delay-100">
                <h3 className="text-lg font-medium mb-4">Maintenance Book</h3>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                    <FormField
                        control={form.control}
                        name="lastServiceDate"
                        render={({ field }) => (
                            <FormItem className="flex flex-col">
                                <FormLabel>Last Service Date</FormLabel>
                                <DatePicker
                                    value={field.value ? new Date(field.value) : undefined}
                                    onChange={(date) => field.onChange(date ? format(date, "yyyy-MM-dd") : "")}
                                    fromYear={new Date().getFullYear() - 5}
                                    toYear={new Date().getFullYear()}
                                />
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={form.control}
                        name="nextServiceDate"
                        render={({ field }) => (
                            <FormItem className="flex flex-col">
                                <FormLabel>Next Service Date</FormLabel>
                                <DatePicker
                                    value={field.value ? new Date(field.value) : undefined}
                                    onChange={(date) => field.onChange(date ? format(date, "yyyy-MM-dd") : "")}
                                    fromDate={new Date()}
                                    toYear={new Date().getFullYear() + 5}
                                />
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={form.control}
                        name="nextServiceMileage"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>Next Service Mileage (km)</FormLabel>
                                <FormControl>
                                    <Input
                                        type="number"
                                        placeholder="e.g., 50000"
                                        {...field}
                                        onChange={(e) => {
                                            const val = e.target.value === "" ? undefined : Number(e.target.value);
                                            field.onChange(val);
                                        }}
                                        value={field.value ?? ""}
                                    />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                </div>
            </div>
        </div>
    );
}
