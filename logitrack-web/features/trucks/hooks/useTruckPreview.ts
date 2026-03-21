import { useState, useEffect } from "react";
import { useSearchParams } from "next/navigation";
import { useBreadcrumb } from "@/context/breadcrumb";
import { useLanguage } from "@/context/language";
import { getTruckByIdClient, TruckData } from "../services/truckService";
import { getTruckAssignmentHistory, AssignmentData } from "@/app/admin/truck-assignment/actions.client";
import { getSubcontractors } from "@/app/admin/subcontractors/actions.client";

export function useTruckPreview() {
    const searchParams = useSearchParams();
    const truckId = searchParams.get('id');
    const { setCustomLastItem } = useBreadcrumb();
    const { t, language } = useLanguage();

    const [truck, setTruck] = useState<TruckData | null>(null);
    const [assignmentHistory, setAssignmentHistory] = useState<AssignmentData[]>([]);
    const [isLoading, setIsLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const [isViewerOpen, setIsViewerOpen] = useState(false);
    const [viewerIndex, setViewerIndex] = useState(0);
    const [subcontractors, setSubcontractors] = useState<any[]>([]);

    useEffect(() => {
        const fetchTruck = async () => {
            if (!truckId) {
                setError(t("trucks.detail.noId"));
                setIsLoading(false);
                return;
            }
            try {
                setIsLoading(true);
                setError(null);
                const data = await getTruckByIdClient(truckId);
                if (!data) {
                    setError(t("trucks.detail.notFound"));
                    return;
                }
                setTruck(data);
                setCustomLastItem(`Truck ${data.licensePlate}`);

                const history = await getTruckAssignmentHistory(truckId);
                setAssignmentHistory(history);
            } catch (err) {
                console.error("Error fetching truck:", err);
                setError(err instanceof Error ? err.message : t("trucks.detail.error"));
            } finally {
                setIsLoading(false);
            }
        };

        fetchTruck();

        return () => {
            setCustomLastItem(null);
        };
    }, [truckId, setCustomLastItem, t]);

    useEffect(() => {
        getSubcontractors().then(setSubcontractors);
    }, []);

    const viewableFiles = truck ? [
        ...(truck.imageFrontRight ? [{ url: truck.imageFrontRight, type: "image" as const, label: t("trucks.detail.view.frontRight") }] : []),
        ...(truck.imageFrontLeft ? [{ url: truck.imageFrontLeft, type: "image" as const, label: t("trucks.detail.view.frontLeft") }] : []),
        ...(truck.imageBackRight ? [{ url: truck.imageBackRight, type: "image" as const, label: t("trucks.detail.view.backRight") }] : []),
        ...(truck.imageBackLeft ? [{ url: truck.imageBackLeft, type: "image" as const, label: t("trucks.detail.view.backLeft") }] : []),
        ...(truck.documentTax ? [{ url: truck.documentTax, type: "pdf" as const, label: t("trucks.detail.taxDoc") }] : []),
        ...(truck.documentRegister ? [{ url: truck.documentRegister, type: "pdf" as const, label: t("trucks.detail.view.registrationDoc") }] : []),
        ...(truck.images || []).map((img, i) => ({ url: img, type: "image" as const, label: `${t("trucks.detail.view.legacyImage")} ${i + 1}` })),
        ...(truck.insuranceDocuments || []).map((doc, i) => ({ url: doc, type: "pdf" as const, label: `${t("trucks.detail.insuranceDoc")} ${i + 1}` })),
    ] : [];

    const handleFileClick = (url: string) => {
        const index = viewableFiles.findIndex(f => f.url === url);
        if (index !== -1) {
            setViewerIndex(index);
            setIsViewerOpen(true);
        }
    };

    return {
        truck,
        assignmentHistory,
        isLoading,
        error,
        isViewerOpen,
        setIsViewerOpen,
        viewerIndex,
        setViewerIndex,
        subcontractors,
        viewableFiles,
        handleFileClick,
        formatDate: (date: Date | string | null | undefined) => {
            if (!date) return "-";
            return new Date(date).toLocaleDateString(language === 'th' ? "th-TH" : "en-US", {
                year: "numeric",
                month: "long",
                day: "numeric",
            });
        },
        t,
        language
    };
}
