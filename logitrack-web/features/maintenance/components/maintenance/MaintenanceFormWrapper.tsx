"use client";

import { useEffect, useState } from "react";
import { MaintenanceForm } from "./MaintenanceForm";
import { saveMaintenanceRecord, getTruckChoices } from "@/features/maintenance/api/maintenance";
import { useAuth } from "@/context/auth";
import { uploadTruckFile } from "@/features/trucks/services/truckService";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { ArrowLeft } from "lucide-react";

interface MaintenanceFormWrapperProps {
    onSuccess: () => void;
    onCancel: () => void;
}

export function MaintenanceFormWrapper({ onSuccess, onCancel }: MaintenanceFormWrapperProps) {
    const auth = useAuth();
    const currentUser = auth?.currentUser;

    const [truckId, setTruckId] = useState<string>("");
    const [trucksList, setTrucksList] = useState<any[]>([]);
    const [isSubmitting, setIsSubmitting] = useState(false);

    const [type, setType] = useState<"PM" | "CM">("PM");
    const [status, setStatus] = useState<"in_progress" | "completed" | "cancelled">("in_progress");
    const [serviceType, setServiceType] = useState<string>("");
    const [customServiceType, setCustomServiceType] = useState<string>(""); 
    const [startDate, setStartDate] = useState<string>(() => {
        const now = new Date();
        const offset = now.getTimezoneOffset() * 60000;
        const localISOTime = (new Date(now.getTime() - offset)).toISOString().slice(0, 16);
        return localISOTime;
    });
    const [endDate, setEndDate] = useState<string>("");
    const [pickupAppointment, setPickupAppointment] = useState<string>("");
    const [paymentMethod, setPaymentMethod] = useState<string>("cash");
    const [costLabor, setCostLabor] = useState<string>("");
    const [costParts, setCostParts] = useState<string>("");
    const [currentMileage, setCurrentMileage] = useState<string>("");
    const [nextServiceMileage, setNextServiceMileage] = useState<string>("");
    const [provider, setProvider] = useState<string>("");
    const [notes, setNotes] = useState<string>("");
    const [selectedFile, setSelectedFile] = useState<File | null>(null);

    useEffect(() => {
        async function loadTrucks() {
            const data = await getTruckChoices();
            setTrucksList(data);
        }
        loadTrucks();
    }, []);

    const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        if (e.target.files && e.target.files[0]) {
            setSelectedFile(e.target.files[0]);
        }
    };

    const handleSave = async (e: React.FormEvent) => {
        e.preventDefault();
        if (!currentUser || !truckId) {
            alert("Please select a truck");
            return;
        }

        setIsSubmitting(true);
        try {
            let imageUrl = "";
            if (selectedFile) {
                const path = `trucks/documents/maintenance/${truckId}/${Date.now()}_${selectedFile.name}`;
                imageUrl = await uploadTruckFile(selectedFile, path);
            }

            const labor = parseFloat(costLabor) || 0;
            const parts = parseFloat(costParts) || 0;
            const finalServiceType = type === "PM" ? serviceType : customServiceType;

            const payload: any = {
                truckId,
                type,
                serviceType: finalServiceType,
                startDate,
                pickupAppointment: pickupAppointment || undefined,
                endDate: status === 'completed' ? endDate : undefined,
                status,
                costLabor: labor > 0 ? labor : undefined,
                costParts: parts > 0 ? parts : undefined,
                totalCost: (labor + parts) > 0 ? (labor + parts) : undefined,
                provider,
                currentMileage: parseFloat(currentMileage) || undefined,
                nextServiceMileage: parseFloat(nextServiceMileage) || undefined,
                paymentMethod,
                images: imageUrl ? [imageUrl] : [],
                notes
            };

            await saveMaintenanceRecord(payload, currentUser.uid);
            onSuccess();
        } catch (error) {
            console.error("Error saving maintenance record from overview:", error);
        } finally {
            setIsSubmitting(false);
        }
    };

    return (
        <div className="space-y-4">
            <Button variant="ghost" onClick={onCancel} className="flex items-center gap-2">
                <ArrowLeft className="h-4 w-4" /> Back to List
            </Button>
            <MaintenanceForm
                selectedRecordId={null}
                type={type}
                setType={setType}
                serviceType={serviceType}
                setServiceType={setServiceType}
                customServiceType={customServiceType}
                setCustomServiceType={setCustomServiceType}
                provider={provider}
                setProvider={setProvider}
                paymentMethod={paymentMethod}
                setPaymentMethod={setPaymentMethod}
                status={status}
                setStatus={setStatus}
                startDate={startDate}
                setStartDate={setStartDate}
                pickupAppointment={pickupAppointment}
                setPickupAppointment={setPickupAppointment}
                endDate={endDate}
                setEndDate={setEndDate}
                costLabor={costLabor}
                setCostLabor={setCostLabor}
                costParts={costParts}
                setCostParts={setCostParts}
                currentMileage={currentMileage}
                setCurrentMileage={setCurrentMileage}
                nextServiceMileage={nextServiceMileage}
                setNextServiceMileage={setNextServiceMileage}
                selectedFile={selectedFile}
                handleFileChange={handleFileChange}
                notes={notes}
                setNotes={setNotes}
                isSubmitting={isSubmitting}
                handleSave={handleSave}
                setView={() => onCancel()}
                truckId={truckId}
                setTruckId={setTruckId}
                trucksList={trucksList}
            />
        </div>
    );
}
