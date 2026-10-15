import { useState } from "react";
import { useSearchParams } from "next/navigation";
import { useLanguage } from "@/context/language";
import { recordUnavailable } from "@/components/record-state";
import { useTruck } from "../api/useTrucks";
import { truckFileType } from "../utils/uploads";
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
    const { options: subcontractors } = useSubcontractorOptions();

    const [isViewerOpen, setIsViewerOpen] = useState(false);
    const [viewerIndex, setViewerIndex] = useState(0);

    // No id, no truck (not found), or the read failed: the page shows RecordState instead of the truck.
    // A failed background refetch keeps the loaded truck (recordUnavailable); only a missing one is "missing".
    const missing = !truckId || recordUnavailable(truckQuery);

    const documentTax = truck ? fileUrl(truck.files, "documentTax") : "";
    const insuranceDocuments = truck ? (truck.files.insuranceDocument ?? []).map((f) => f.url ?? "").filter(Boolean) : [];
    const partnerName =
        truck && truck.ownershipType === "subcontractor"
            ? carrierName(subcontractors.find((s) => s.id === truck.tenantId)) || truck.tenantId
            : "";

    const viewableFiles = truck
        ? [
              ...(fileUrl(truck.files, "imageFrontRight") ? [{ url: fileUrl(truck.files, "imageFrontRight"), isPhoto: true, type: "image" as const, label: t("trucks.detail.view.frontRight") }] : []),
              ...(fileUrl(truck.files, "imageFrontLeft") ? [{ url: fileUrl(truck.files, "imageFrontLeft"), isPhoto: true, type: "image" as const, label: t("trucks.detail.view.frontLeft") }] : []),
              ...(fileUrl(truck.files, "imageBackRight") ? [{ url: fileUrl(truck.files, "imageBackRight"), isPhoto: true, type: "image" as const, label: t("trucks.detail.view.backRight") }] : []),
              ...(fileUrl(truck.files, "imageBackLeft") ? [{ url: fileUrl(truck.files, "imageBackLeft"), isPhoto: true, type: "image" as const, label: t("trucks.detail.view.backLeft") }] : []),
              ...(documentTax ? [{ url: documentTax, isPhoto: false, type: truckFileType(documentTax), label: t("trucks.detail.taxDoc") }] : []),
              ...(fileUrl(truck.files, "documentRegister") ? [{ url: fileUrl(truck.files, "documentRegister"), isPhoto: false, type: truckFileType(fileUrl(truck.files, "documentRegister")), label: t("trucks.detail.view.registrationDoc") }] : []),
              ...(truck.files.legacyImage ?? []).filter((f) => f.url).map((f, i) => ({ url: f.url!, isPhoto: true, type: "image" as const, label: `${t("trucks.detail.view.legacyImage")} ${i + 1}` })),
              ...insuranceDocuments.map((url, i) => ({ url, isPhoto: false, type: truckFileType(url), label: `${t("trucks.detail.insuranceDoc")} ${i + 1}` })),
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
