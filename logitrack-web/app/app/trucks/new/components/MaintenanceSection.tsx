import {
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { DatePicker } from "@/components/ui/date-picker";
import { format } from "date-fns";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useFormContext } from "react-hook-form";
import { useLanguage } from "@/context/language";
import { Wrench } from "lucide-react";

export function MaintenanceSection() {
    const { control } = useFormContext();
    const { t } = useLanguage();

    return (
        <Card>
            <CardHeader>
                <CardTitle className="flex items-center gap-2">
                    <Wrench className="h-5 w-5 text-primary" />
                    {t("trucks.maintenance.book")}
                </CardTitle>
            </CardHeader>
            <CardContent className="space-y-6">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                    <FormField
                        control={control}
                        name="lastServiceDate"
                        render={({ field }) => (
                            <FormItem className="flex flex-col">
                                <FormLabel>{t("trucks.maintenance.lastDate")}</FormLabel>
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
                        control={control}
                        name="nextServiceDate"
                        render={({ field }) => (
                            <FormItem className="flex flex-col">
                                <FormLabel>{t("trucks.maintenance.nextDate")}</FormLabel>
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
                        control={control}
                        name="nextServiceMileage"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.maintenance.nextMileage")}</FormLabel>
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
                    <FormField
                        control={control}
                        name="maintenanceResponsible"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.maintenance.responsible")}</FormLabel>
                                <FormControl>
                                    <Input placeholder="e.g. Driver Name" {...field} value={field.value ?? ""} />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                </div>
            </CardContent>
        </Card>
    );
}
