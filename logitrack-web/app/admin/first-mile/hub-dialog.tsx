"use client";

import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { Loader2, Plus, MapPin } from "lucide-react";
import { Button } from "@/components/ui/button";
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
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { hubSchema, Hub, STATION_TYPE_ENUM } from "@/validate/hubSchema";
import { collection, addDoc, doc, updateDoc, query, where, getDocs } from "firebase/firestore";
import { db } from "@/firebase/client";
import SimpleMap from "@/components/map/SimpleMap";
import { useLanguage } from "@/context/language";
import { toast } from "sonner";

interface HubDialogProps {
    trigger?: React.ReactNode;
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    onSuccess?: () => void;
    /** When set, dialog opens in edit mode with these values and updates this document */
    defaultValues?: Partial<Hub>;
    documentId?: string;
}

export function HubDialog({ trigger, open, onOpenChange, onSuccess, defaultValues, documentId }: HubDialogProps) {
    const [internalOpen, setInternalOpen] = useState(false);
    const [loading, setLoading] = useState(false);
    const { t } = useLanguage();

    // Controlled open state
    const isOpen = open !== undefined ? open : internalOpen;
    const setIsOpen = onOpenChange || setInternalOpen;

    const form = useForm<Hub>({
        resolver: zodResolver(hubSchema as any),
        defaultValues: {
            source_id: "",
            source_name_en: "",
            latitude: 13.7563, // Default BKK
            longitude: 100.5018,
            station_type: "HUB",
        },
    });

    const isEditMode = Boolean(documentId && defaultValues);

    // Reset form when dialog opens (add vs edit)
    useEffect(() => {
        if (isOpen) {
            if (isEditMode && defaultValues) {
                form.reset({
                    source_id: defaultValues.source_id ?? "",
                    source_name_en: defaultValues.source_name_en ?? "",
                    latitude: defaultValues.latitude ?? 13.7563,
                    longitude: defaultValues.longitude ?? 100.5018,
                    station_type: defaultValues.station_type ?? "HUB",
                });
            } else {
                form.reset({
                    source_id: "",
                    source_name_en: "",
                    latitude: 13.7563,
                    longitude: 100.5018,
                    station_type: "HUB",
                });
            }
        }
    }, [isOpen, isEditMode, defaultValues, form]);

    const onSubmit = async (values: Hub) => {
        setLoading(true);
        try {
            const sourceId = String(values.source_id ?? "").trim();
            if (!sourceId) return;

            // เช็ครหัสซ้ำ (ยกเว้นเอกสารที่กำลังแก้ไข)
            const q = query(
                collection(db, "hubs"),
                where("source_id", "==", sourceId)
            );
            const snap = await getDocs(q);
            const existing = snap.docs.filter((d) => d.id !== documentId);
            if (existing.length > 0) {
                toast.error(t("firstMile.hub.duplicateCode"));
                setLoading(false);
                return;
            }

            const payload = {
                ...values,
                updatedAt: new Date(),
            };
            if (documentId) {
                await updateDoc(doc(db, "hubs", documentId), payload);
            } else {
                await addDoc(collection(db, "hubs"), {
                    ...payload,
                    createdAt: new Date(),
                });
            }
            setIsOpen(false);
            if (onSuccess) onSuccess();
        } catch (error) {
            console.error("Error saving source:", error);
        } finally {
            setLoading(false);
        }
    };

    return (
        <Dialog open={isOpen} onOpenChange={setIsOpen}>
            {trigger && <DialogTrigger asChild>{trigger}</DialogTrigger>}
            <DialogContent className="sm:max-w-[600px] max-h-[90vh] overflow-y-auto">
                <DialogHeader>
                    <DialogTitle>{isEditMode ? t("firstMile.hub.titleEdit") : t("firstMile.hub.title")}</DialogTitle>
                    <DialogDescription>
                        {isEditMode ? t("firstMile.hub.descriptionEdit") : t("firstMile.hub.description")}
                    </DialogDescription>
                </DialogHeader>

                <Form {...form}>
                    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
                        <div className="grid grid-cols-2 gap-4">
                            <FormField
                                control={form.control}
                                name="source_id"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("firstMile.hub.sourceId")}</FormLabel>
                                        <FormControl>
                                            <Input placeholder={t("firstMile.hub.sourceIdPlaceholder")} {...field} />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            <FormField
                                control={form.control}
                                name="source_name_en"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("firstMile.hub.spxName")}</FormLabel>
                                        <FormControl>
                                            <Input placeholder={t("firstMile.hub.spxNamePlaceholder")} {...field} />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                        </div>

                        <FormField
                            control={form.control}
                            name="station_type"
                            render={({ field }) => (
                                <FormItem>
                                    <FormLabel>{t("firstMile.hub.stationType")}</FormLabel>
                                    <Select onValueChange={field.onChange} value={field.value}>
                                        <FormControl>
                                            <SelectTrigger>
                                                <SelectValue placeholder={t("firstMile.hub.stationTypePlaceholder")} />
                                            </SelectTrigger>
                                        </FormControl>
                                        <SelectContent>
                                            {STATION_TYPE_ENUM.map((type) => (
                                                <SelectItem key={type} value={type}>
                                                    {t(`firstMile.hub.stationType.${type}`)}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />

                        <div className="space-y-2">
                            <FormLabel>{t("firstMile.hub.location")}</FormLabel>
                            <div className="border rounded-md p-1">
                                <SimpleMap
                                    value={form.watch("latitude") != null && form.watch("longitude") != null ? { lat: form.watch("latitude")!, lng: form.watch("longitude")! } : undefined}
                                    onChange={(pos) => {
                                        form.setValue("latitude", pos.lat);
                                        form.setValue("longitude", pos.lng);
                                    }}
                                />
                            </div>
                            <div className="flex gap-4 text-xs text-muted-foreground">
                                <span>Lat: {form.watch("latitude")?.toFixed(6)}</span>
                                <span>Lng: {form.watch("longitude")?.toFixed(6)}</span>
                            </div>
                        </div>

                        <DialogFooter>
                            <Button type="button" variant="outline" onClick={() => setIsOpen(false)}>{t("firstMile.hub.cancel")}</Button>
                            <Button type="submit" disabled={loading}>
                                {loading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                {t("firstMile.hub.save")}
                            </Button>
                        </DialogFooter>
                    </form>
                </Form>
            </DialogContent>
        </Dialog>
    );
}
