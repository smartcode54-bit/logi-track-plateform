import { ref, uploadBytes, getDownloadURL } from "firebase/storage";
import { storage } from "@/firebase/client";

/**
 * Upload trip photo to Storage (trip_records/{tripId}/{photoType}.jpg)
 * Returns download URL for updating trip_record.photos
 */
export async function uploadTripPhoto(
    tripId: string,
    photoType: string,
    file: File
): Promise<string> {
    const path = `trip_records/${tripId}/${photoType}.jpg`;
    const storageRef = ref(storage, path);
    await uploadBytes(storageRef, file, { contentType: "image/jpeg" });
    return getDownloadURL(storageRef);
}
