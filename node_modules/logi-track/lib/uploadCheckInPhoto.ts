import { ref, uploadBytes, getDownloadURL } from "firebase/storage";
import { storage } from "@/firebase/client";

/**
 * Upload check-in photo to Storage (checkin/{taskId}/{timestamp}.jpg)
 * Returns download URL for updating task.checkInPhotoUrl
 */
export async function uploadCheckInPhoto(taskId: string, file: File): Promise<string> {
    const path = `checkin/${taskId}/${Date.now()}.jpg`;
    const storageRef = ref(storage, path);
    await uploadBytes(storageRef, file, { contentType: "image/jpeg" });
    return getDownloadURL(storageRef);
}
