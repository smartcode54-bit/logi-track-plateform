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
import { hubSchema, Hub } from "@/validate/hubSchema";
import { collection, addDoc } from "firebase/firestore";
import { db } from "@/firebase/client";
import SimpleMap from "@/components/map/SimpleMap";

interface HubDialogProps {
    trigger?: React.ReactNode;
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    onSuccess?: () => void;
}

export function HubDialog({ trigger, open, onOpenChange, onSuccess }: HubDialogProps) {
    const [internalOpen, setInternalOpen] = useState(false);
    const [loading, setLoading] = useState(false);

    // Controlled open state
    const isOpen = open !== undefined ? open : internalOpen;
    const setIsOpen = onOpenChange || setInternalOpen;

    const form = useForm<Hub>({
        resolver: zodResolver(hubSchema as any),
        defaultValues: {
            hubId: "",
            hubName: "",
            hubTHName: "",
            source: "custom",
            lat: 13.7563, // Default BKK
            lng: 100.5018
        },
    });

    // Reset form when dialog opens
    useEffect(() => {
        if (isOpen) {
            form.reset({
                hubId: "",
                hubName: "",
                hubTHName: "",
                lat: 13.7563,
                lng: 100.5018,
                source: "custom",
            });
        }
    }, [isOpen, form]);

    const onSubmit = async (values: Hub) => {
        setLoading(true);
        try {
            await addDoc(collection(db, "hubs"), {
                ...values,
                hubCode: values.hubId, // Backwards compat or if code relies on it
                createdAt: new Date(),
                updatedAt: new Date(),
            });
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
                    <DialogTitle>Add New Source</DialogTitle>
                    <DialogDescription>
                        Register a new source location with coordinates.
                    </DialogDescription>
                </DialogHeader>

                <Form {...form}>
                    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
                        <div className="grid grid-cols-2 gap-4">
                            <FormField
                                control={form.control}
                                name="hubId"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>Source ID</FormLabel>
                                        <FormControl>
                                            <Input placeholder="e.g. SPX01" {...field} />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            <FormField
                                control={form.control}
                                name="hubName"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>SPX Name</FormLabel>
                                        <FormControl>
                                            <Input placeholder="e.g. Bangkok Hub" {...field} />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                        </div>

                        <FormField
                            control={form.control}
                            name="hubTHName"
                            render={({ field }) => (
                                <FormItem>
                                    <FormLabel>Source Name (Thai)</FormLabel>
                                    <FormControl>
                                        <Input placeholder="e.g. ศูนย์คัดแยกกรุงเทพ" {...field} />
                                    </FormControl>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />

                        <div className="space-y-2">
                            <FormLabel>Location</FormLabel>
                            <div className="border rounded-md p-1">
                                <SimpleMap
                                    value={form.watch("lat") && form.watch("lng") ? { lat: form.watch("lat")!, lng: form.watch("lng")! } : undefined}
                                    onChange={(pos) => {
                                        form.setValue("lat", pos.lat);
                                        form.setValue("lng", pos.lng);
                                    }}
                                />
                            </div>
                            <div className="flex gap-4 text-xs text-muted-foreground">
                                <span>Lat: {form.watch("lat")?.toFixed(6)}</span>
                                <span>Lng: {form.watch("lng")?.toFixed(6)}</span>
                            </div>
                        </div>

                        <DialogFooter>
                            <Button type="button" variant="outline" onClick={() => setIsOpen(false)}>Cancel</Button>
                            <Button type="submit" disabled={loading}>
                                {loading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                Save Source
                            </Button>
                        </DialogFooter>
                    </form>
                </Form>
            </DialogContent>
        </Dialog>
    );
}
