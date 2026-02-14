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
import { PROVINCES } from "@/lib/provinces";
import { Combobox } from "@/components/ui/combobox";
import { Fingerprint } from "lucide-react";

export function IdentificationSection() {
    const { control, setValue } = useFormContext();
    const { t } = useLanguage();

    return (
        <Card>
            <CardHeader>
                <CardTitle className="flex items-center gap-2">
                    <Fingerprint className="h-5 w-5 text-primary" />
                    {t("trucks.section.identification")}
                </CardTitle>
            </CardHeader>
            <CardContent className="space-y-6">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                    <FormField
                        control={control}
                        name="licensePlate"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.licensePlate")} *</FormLabel>
                                <FormControl>
                                    <Input
                                        placeholder={t("trucks.placeholder.licensePlate")}
                                        maxLength={7}
                                        {...field}
                                        onChange={(e) => {
                                            // Remove English characters (a-z, A-Z) to enforce Thai characters
                                            const value = e.target.value.replace(/[a-zA-Z]/g, '');
                                            field.onChange(value);
                                        }}
                                    />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="province"
                        render={({ field }) => (
                            <FormItem className="flex flex-col">
                                <FormLabel>{t("trucks.section.province")} *</FormLabel>
                                <FormControl>
                                    <Combobox
                                        options={PROVINCES}
                                        value={field.value}
                                        onSelect={(value) => setValue("province", value, { shouldValidate: true })}
                                        placeholder={t("trucks.section.province")}
                                        searchPlaceholder="Search province..."
                                        className={!field.value ? "text-muted-foreground" : ""}
                                    />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />

                    <FormField
                        control={control}
                        name="vin"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.vin")} *</FormLabel>
                                <FormControl>
                                    <Input
                                        placeholder={t("trucks.placeholder.vin")}
                                        maxLength={17}
                                        {...field}
                                        onChange={(e) => {
                                            // Remove Thai characters
                                            const value = e.target.value.toUpperCase().replace(/[\u0E00-\u0E7F]/g, "");
                                            field.onChange(value);
                                        }}
                                    />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="engineNumber"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.engineNumber")} *</FormLabel>
                                <FormControl>
                                    <Input
                                        placeholder={t("trucks.placeholder.engineNumber")}
                                        maxLength={10}
                                        {...field}
                                        onChange={(e) => {
                                            // Remove Thai characters
                                            const value = e.target.value.toUpperCase().replace(/[\u0E00-\u0E7F]/g, "");
                                            field.onChange(value);
                                        }}
                                    />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="truckStatus"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("trucks.section.truckStatus")} *</FormLabel>
                                <Select onValueChange={field.onChange} value={field.value || ""}>
                                    <FormControl>
                                        <SelectTrigger>
                                            <SelectValue placeholder={t("trucks.section.truckStatus")} />
                                        </SelectTrigger>
                                    </FormControl>
                                    <SelectContent>
                                        <SelectItem value="active">{t("trucks.detail.status.active")}</SelectItem>
                                        <SelectItem value="inactive">{t("trucks.detail.status.inactive")}</SelectItem>
                                        <SelectItem value="maintenance">{t("trucks.detail.status.maintenance")}</SelectItem>
                                        <SelectItem value="insurance-claim">{t("trucks.detail.status.insuranceClaim")}</SelectItem>
                                        <SelectItem value="sold">{t("trucks.detail.status.sold")}</SelectItem>
                                    </SelectContent>
                                </Select>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                </div>
            </CardContent>
        </Card>
    );
}
