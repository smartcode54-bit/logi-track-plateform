"use client";

import { db } from "@/firebase/client";
import { collection, addDoc, Timestamp } from "firebase/firestore";

export interface PartnerInterestData {
    companyName: string;
    contractNumber: string;
    contactName: string;
    email: string;
    serviceArea: string;
    fleetSize: string;
    vehicleType: string;
    status: 'new' | 'reviewed' | 'contacted';
    createdAt: Date;
}

export async function submitPartnerInterest(data: Omit<PartnerInterestData, 'createdAt' | 'status'>): Promise<string> {
    try {
        const docRef = await addDoc(collection(db, "partner-interest"), {
            ...data,
            status: 'new',
            createdAt: Timestamp.now(),
        });
        return docRef.id;
    } catch (error) {
        console.error("Error submitting partner interest:", error);
        throw error;
    }
}
