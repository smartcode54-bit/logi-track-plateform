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
import { Zap } from "lucide-react";

export function EngineInformationSection() {
    const { control } = useFormContext();
    const { t } = useLanguage();

    return (
        <Card>
            <CardHeader>
                <CardTitle className="flex items-center gap-2">
                    <Zap className="h-5 w-5 text-primary" />
                    {t("trucks.section.engineInfo")}
                </CardTitle>
            </CardHeader>
            <CardContent className="space-y-6">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                    <FormField
                        control={control}
                        name="fuelType"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.fuelType")} *</FormLabel>
                                <Select onValueChange={field.onChange} defaultValue={field.value}>
                                    <FormControl>
                                        <SelectTrigger>
                                            <SelectValue placeholder={t("trucks.section.selectFuel")} />
                                        </SelectTrigger>
                                    </FormControl>
                                    <SelectContent>
                                        <SelectItem value="Diesel">{t("trucks.fuel.diesel")}</SelectItem>
                                        <SelectItem value="Gasoline">{t("trucks.fuel.gasoline")}</SelectItem>
                                        <SelectItem value="NGV">{t("trucks.fuel.ngv")}</SelectItem>
                                        <SelectItem value="Electric">{t("trucks.fuel.electric")}</SelectItem>
                                        <SelectItem value="Hybrid">{t("trucks.fuel.hybrid")}</SelectItem>
                                    </SelectContent>
                                </Select>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="engineCapacity"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.engineCap")}</FormLabel>
                                <FormControl>
                                    <Input
                                        type="number"
                                        min="0"
                                        step="1"
                                        placeholder={t("trucks.placeholder.cc")}
                                        value={field.value ?? ""}
                                        onChange={(e) => {
                                            const value = e.target.value;
                                            // Only allow positive numbers or empty string
                                            if (value === "" || (!isNaN(Number(value)) && Number(value) >= 0)) {
                                                field.onChange(value === "" ? undefined : Number(value));
                                            }
                                        }}
                                        onBlur={field.onBlur}
                                    />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="fuelCapacity"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.fuelCap")}</FormLabel>
                                <FormControl>
                                    <Input
                                        type="number"
                                        min="0"
                                        step="1"
                                        placeholder={t("trucks.placeholder.fuelCapacity")}
                                        value={field.value ?? ""}
                                        onChange={(e) => {
                                            const value = e.target.value;
                                            // Only allow positive numbers or empty string
                                            if (value === "" || (!isNaN(Number(value)) && Number(value) >= 0)) {
                                                field.onChange(value === "" ? undefined : Number(value));
                                            }
                                        }}
                                        onBlur={field.onBlur}
                                    />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="maxLoadWeight"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.maxLoad")}</FormLabel>
                                <FormControl>
                                    <Input
                                        type="number"
                                        min="0"
                                        step="1"
                                        placeholder={t("trucks.placeholder.weight")}
                                        value={field.value ?? ""}
                                        onChange={(e) => {
                                            const value = e.target.value;
                                            // Only allow positive numbers or empty string
                                            if (value === "" || (!isNaN(Number(value)) && Number(value) >= 0)) {
                                                field.onChange(value === "" ? undefined : Number(value));
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
