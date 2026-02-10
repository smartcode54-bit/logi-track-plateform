import { NextResponse } from 'next/server';
import { db } from "@/firebase/client";
import { collection, getDocs } from "firebase/firestore";

export const dynamic = 'force-dynamic';

export async function GET() {
    try {
        let hubs: any[] = [];
        try {
            const querySnapshot = await getDocs(collection(db, "hubs"));
            hubs = querySnapshot.docs.map(doc => {
                const data = doc.data();
                return {
                    'Hub Code': data.hubId || data.hubCode, // Prefer hubId, fallback to code
                    'Hub Name': data.hubName,
                    'Hub Name TH': data.hubTHName,
                    lat: data.lat,
                    lng: data.lng,
                    source: 'custom',
                    id: doc.id
                };
            });
        } catch (dbError) {
            console.error("Firestore fetch error:", dbError);
            return NextResponse.json({ error: 'Failed to fetch hubs from database' }, { status: 500 });
        }

        return NextResponse.json({ hubs });
    } catch (error) {
        console.error('Error reading hub data:', error);
        return NextResponse.json({ error: 'Internal Server Error' }, { status: 500 });
    }
}
