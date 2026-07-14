import {
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
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useFormContext } from "react-hook-form";
import { useLanguage } from "@/context/language";
import { Truck } from "lucide-react";

export function VehicleDetailsSection() {
    const { control } = useFormContext();
    const { t } = useLanguage();

    const currentYear = new Date().getFullYear();
    const years = Array.from({ length: 11 }, (_, i) => (currentYear - i).toString());

    return (
        <Card>
            <CardHeader>
                <CardTitle className="flex items-center gap-2">
                    <Truck className="h-5 w-5 text-primary" />
                    {t("trucks.section.details")}
                </CardTitle>
            </CardHeader>
            <CardContent className="space-y-6">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                    <FormField
                        control={control}
                        name="brand"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.brand")} *</FormLabel>
                                <FormControl>
                                    <Input placeholder={t("trucks.placeholder.brand")} {...field} />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="model"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.model")} *</FormLabel>
                                <FormControl>
                                    <Input placeholder={t("trucks.placeholder.model")} {...field} />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="year"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.year")}</FormLabel>
                                <Select onValueChange={field.onChange} defaultValue={field.value}>
                                    <FormControl>
                                        <SelectTrigger>
                                            <SelectValue placeholder={t("trucks.placeholder.selectYear")} />
                                        </SelectTrigger>
                                    </FormControl>
                                    <SelectContent>
                                        {years.map((year) => (
                                            <SelectItem key={year} value={year}>
                                                {year}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="color"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.color")}</FormLabel>
                                <FormControl>
                                    <Input placeholder={t("trucks.placeholder.color")} {...field} />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="type"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.truckType")} *</FormLabel>
                                <Select onValueChange={field.onChange} defaultValue={field.value}>
                                    <FormControl>
                                        <SelectTrigger>
                                            <SelectValue placeholder={t("trucks.placeholder.selectType")} />
                                        </SelectTrigger>
                                    </FormControl>
                                    <SelectContent>
                                        {/* Stored as "Pickup" on purpose — the truck master keeps full
                                            names and lib/truckType.ts maps it to the 4W task class.
                                            Only the label follows the 4W vocabulary. */}
                                        <SelectItem value="Pickup">{t("trucks.type.pickup")}</SelectItem>
                                        <SelectItem value="4 Wheels Jumbo">{t("trucks.type.4wheels")}</SelectItem>
                                        <SelectItem value="6 Wheels">{t("trucks.type.6wheels")}</SelectItem>
                                        <SelectItem value="10 Wheels">{t("trucks.type.10wheels")}</SelectItem>
                                        <SelectItem value="18 Wheels">{t("trucks.type.18wheels")}</SelectItem>
                                        <SelectItem value="Van">{t("trucks.type.van")}</SelectItem>
                                    </SelectContent>
                                </Select>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="seats"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.seats")}</FormLabel>
                                <FormControl>
                                    <Input
                                        type="number"
                                        min="0"
                                        max="10"
                                        step="1"
                                        placeholder={t("trucks.placeholder.seats")}
                                        value={field.value ?? ""}
                                        onChange={(e) => {
                                            const value = e.target.value;
                                            // Only allow positive numbers 0-10 or empty string
                                            if (value === "" || (!isNaN(Number(value)) && Number(value) >= 0 && Number(value) <= 10)) {
                                                field.onChange(value);
                                            }
                                        }}
                                        onBlur={field.onBlur}
                                    />
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
