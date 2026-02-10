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
                    {t("Identification")}
                </CardTitle>
            </CardHeader>
            <CardContent className="space-y-6">
                <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                    <FormField
                        control={control}
                        name="licensePlate"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>{t("license Plate")} *</FormLabel>
                                <FormControl>
                                    <Input
                                        placeholder="e.g., กข1234"
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
                                <FormLabel>{t("province")} *</FormLabel>
                                <FormControl>
                                    <Combobox
                                        options={PROVINCES}
                                        value={field.value}
                                        onSelect={(value) => setValue("province", value, { shouldValidate: true })}
                                        placeholder="Select province"
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
                                <FormLabel>{t("VIN")} *</FormLabel>
                                <FormControl>
                                    <Input
                                        placeholder="e.g., 1HGBH41JXMN109186"
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
                                <FormLabel>{t("engineNumber")} *</FormLabel>
                                <FormControl>
                                    <Input
                                        placeholder="e.g., 4D56ABC123"
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
                                <FormLabel>{t("truck status")} *</FormLabel>
                                <Select onValueChange={field.onChange} value={field.value || ""}>
                                    <FormControl>
                                        <SelectTrigger>
                                            <SelectValue placeholder="Select truck status" />
                                        </SelectTrigger>
                                    </FormControl>
                                    <SelectContent>
                                        <SelectItem value="active">Active</SelectItem>
                                        <SelectItem value="inactive">Inactive</SelectItem>
                                        <SelectItem value="maintenance">Maintenance</SelectItem>
                                        <SelectItem value="insurance-claim">Insurance Claim</SelectItem>
                                        <SelectItem value="sold">Sold</SelectItem>
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
