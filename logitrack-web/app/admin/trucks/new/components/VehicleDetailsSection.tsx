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
                    {t("Details")}
                </CardTitle>
            </CardHeader>
            <CardContent className="space-y-6">
                <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                    <FormField
                        control={control}
                        name="brand"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("brand")} *</FormLabel>
                                <FormControl>
                                    <Input placeholder="e.g., Isuzu" {...field} />
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
                                <FormLabel>{t("model")} *</FormLabel>
                                <FormControl>
                                    <Input placeholder="e.g., ELF" {...field} />
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
                                <FormLabel>{t("year")}</FormLabel>
                                <Select onValueChange={field.onChange} defaultValue={field.value}>
                                    <FormControl>
                                        <SelectTrigger>
                                            <SelectValue placeholder="Select year" />
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
                                <FormLabel>{t("color")}</FormLabel>
                                <FormControl>
                                    <Input placeholder="e.g., White" {...field} />
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
                                <FormLabel>{t("truckType")} *</FormLabel>
                                <Select onValueChange={field.onChange} defaultValue={field.value}>
                                    <FormControl>
                                        <SelectTrigger>
                                            <SelectValue placeholder="Select truck type" />
                                        </SelectTrigger>
                                    </FormControl>
                                    <SelectContent>
                                        <SelectItem value="Pickup">Pickup</SelectItem>
                                        <SelectItem value="4 Wheels">4 Wheels</SelectItem>
                                        <SelectItem value="6 Wheels">6 Wheels</SelectItem>
                                        <SelectItem value="10 Wheels">10 Wheels</SelectItem>
                                        <SelectItem value="18 Wheels">18 Wheels</SelectItem>
                                        <SelectItem value="Van">Van</SelectItem>
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
                                <FormLabel>{t("seats")}</FormLabel>
                                <FormControl>
                                    <Input
                                        type="number"
                                        min="0"
                                        max="10"
                                        step="1"
                                        placeholder="e.g., 3"
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
