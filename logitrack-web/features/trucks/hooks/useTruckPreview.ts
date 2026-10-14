import { useState } from "react";
import { useSearchParams } from "next/navigation";
import { useLanguage } from "@/context/language";
import { useAssignmentsOf, useTruck } from "../api/useTrucks";
import { fileUrl } from "../api/trucks";
import { useSubcontractorOptions } from "@/features/subcontractors/api/useSubcontractors";
import { carrierName } from "@/features/subcontractors/api/subcontractors";

/**
 * The truck page (T25): `['trucks','detail',id]` with the truck's assignments
 * (`['truckAssignments',{truckId}]`, fetched in parallel instead of after the truck) and the carrier
 * name from `['subcontractors','options']`. The page names itself with the plate through its `PageHeader` (UI3D);
 * without a truck it shows `RecordState` (not found, or a load error with a retry).
 */
export function useTruckPreview() {
    const searchParams = useSearchParams();
    const truckId = searchParams.get("id") ?? "";
    const { t, language } = useLanguage();

    const truckQuery = useTruck(truckId);
    const truck = truckQuery.data ?? null;
    const assignments = useAssignmentsOf({ truckId });
    const { options: subcontractors } = useSubcontractorOptions();

    const [isViewerOpen, setIsViewerOpen] = useState(false);
    const [viewerIndex, setViewerIndex] = useState(0);

    // No id, no truck (not found), or the read failed: the page shows RecordState instead of the truck.
    const missing = !truckId || truckQuery.isError || (truckQuery.isSuccess && !truck);

    const documentTax = truck ? fileUrl(truck.files, "documentTax") : "";
    const insuranceDocuments = truck ? (truck.files.insuranceDocument ?? []).map((f) => f.url ?? "").filter(Boolean) : [];
    const partnerName =
        truck && truck.ownershipType === "subcontractor"
            ? carrierName(subcontractors.find((s) => s.id === truck.tenantId)) || truck.tenantId
            : "";

    const viewableFiles = truck
        ? [
              ...(fileUrl(truck.files, "imageFrontRight") ? [{ url: fileUrl(truck.files, "imageFrontRight"), type: "image" as const, label: t("trucks.detail.view.frontRight") }] : []),
              ...(fileUrl(truck.files, "imageFrontLeft") ? [{ url: fileUrl(truck.files, "imageFrontLeft"), type: "image" as const, label: t("trucks.detail.view.frontLeft") }] : []),
              ...(fileUrl(truck.files, "imageBackRight") ? [{ url: fileUrl(truck.files, "imageBackRight"), type: "image" as const, label: t("trucks.detail.view.backRight") }] : []),
              ...(fileUrl(truck.files, "imageBackLeft") ? [{ url: fileUrl(truck.files, "imageBackLeft"), type: "image" as const, label: t("trucks.detail.view.backLeft") }] : []),
              ...(documentTax ? [{ url: documentTax, type: "pdf" as const, label: t("trucks.detail.taxDoc") }] : []),
              ...(fileUrl(truck.files, "documentRegister") ? [{ url: fileUrl(truck.files, "documentRegister"), type: "pdf" as const, label: t("trucks.detail.view.registrationDoc") }] : []),
              ...(truck.files.legacyImage ?? []).filter((f) => f.url).map((f, i) => ({ url: f.url!, type: "image" as const, label: `${t("trucks.detail.view.legacyImage")} ${i + 1}` })),
              ...insuranceDocuments.map((url, i) => ({ url, type: "pdf" as const, label: `${t("trucks.detail.insuranceDoc")} ${i + 1}` })),
          ]
        : [];

    const handleFileClick = (url: string) => {
        const index = viewableFiles.findIndex((f) => f.url === url);
        if (index !== -1) {
            setViewerIndex(index);
            setIsViewerOpen(true);
        }
    };

    return {
        truck,
        assignmentHistory: assignments.assignments,
        isLoading: truckQuery.isPending && Boolean(truckId),
        missing,
        loadError: truckQuery.error,
        retry: () => void truckQuery.refetch(),
        retrying: truckQuery.isFetching,
        isViewerOpen,
        setIsViewerOpen,
        viewerIndex,
        setViewerIndex,
        subcontractors,
        partnerName,
        documentTax,
        insuranceDocuments,
        viewableFiles,
        handleFileClick,
        formatDate: (date: Date | string | null | undefined) => {
            if (!date) return "-";
            return new Date(date).toLocaleDateString(language === "th" ? "th-TH" : "en-US", {
                year: "numeric",
                month: "long",
                day: "numeric",
            });
        },
        t,
        language,
    };
}
