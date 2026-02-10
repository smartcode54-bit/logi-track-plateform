# Firebase Data Guide: Saving and Editing

This guide details how to perform **Create** and **Update** operations using your project's specific Firebase architecture.

## 1. Architecture Overview (Recap)
In your project:
1.  **Connection**: `firebase/client.ts` initializes the app and exports `db`.
2.  **Actions**: Specific logic (saving/updating) lives in dedicated `action.client.ts` files near their components.

---

## 2. How to Save Data (Create)

To create a new document, we use `addDoc` (for auto-generated IDs) or `setDoc` (for custom IDs).

### Pattern
1.  Import `db` from `@/firebase/client`.
2.  Define your function (e.g., `saveNewItem`).
3.  Use `collection()` to choose where to save.
4.  Use `addDoc()` to save the data.

### Example Code (Generic)
```typescript
import { db } from "@/firebase/client";
import { collection, addDoc, serverTimestamp } from "firebase/firestore";

export const saveItem = async (data: any, userId: string) => {
    // 1. Reference the collection
    const collectionRef = collection(db, "items"); 

    // 2. Add the document
    await addDoc(collectionRef, {
        ...data,
        createdBy: userId,
        createdAt: serverTimestamp(), // Always track creation
    });
};
```

### Real Project Example
*Reference: `app/admin/trucks/new/action.client.ts`*

Your `saveNewTruckToFirestoreClient` function effectively demonstrates this:
```typescript
const trucksRef = collection(db, "trucks");
const docRef = await addDoc(trucksRef, {
    ...data,
    createdBy: userId,
    createdAt: serverTimestamp(),
});
```

---

## 3. How to Edit Data (Update)

To edit an existing document, we use `updateDoc`. This only changes the fields you specify and leaves others alone.

### Pattern
1.  Import `db` from `@/firebase/client`.
2.  Define your function (e.g., `updateItem`) accepting the `id`.
3.  Use `doc()` to reference the *specific* document by ID.
4.  Use `updateDoc()` to apply changes.

### Example Code (Generic)
```typescript
import { db } from "@/firebase/client";
import { doc, updateDoc, serverTimestamp } from "firebase/firestore";

export const updateItem = async (id: string, updates: any, userId: string) => {
    // 1. Reference the specific document
    const docRef = doc(db, "items", id); 

    // 2. Update only provided fields
    await updateDoc(docRef, {
        ...updates,
        updatedBy: userId,         // Track who updated it
        updatedAt: serverTimestamp() // Track when it was updated
    });
};
```

### Real Project Example
*Reference: `app/admin/trucks/new/action.client.ts`*

Your `updateTruckInFirestoreClient` function effectively demonstrates this:
```typescript
export const updateTruckInFirestoreClient = async (truckId: string, data: any, userId: string) => {
    // 1. Reference specific truck
    const truckRef = doc(db, "trucks", truckId);
    
    // 2. Update it
    await updateDoc(truckRef, {
        ...data,
        updatedAt: serverTimestamp(),
        updatedBy: userId,
    });
};
```

---

## 4. How to Upload Images (Storage)

Images are stored in **Firebase Storage** (a file bucket), separate from Firestore (the database). The database only stores the *link* (URL) to the image.

### Pattern
1.  Import `storage` from `@/firebase/client`.
2.  Get the file from an input (`<input type="file" />`).
3.  Create a unique filename (e.g., using `Date.now()`).
4.  Create a storage reference (`ref()`).
5.  Upload the file (`uploadBytes()`).
6.  Get the public URL (`getDownloadURL()`).
7.  Save that URL to Firestore (as part of your data object).

### Example Code (Client-Side)
```typescript
import { storage } from "@/firebase/client";
import { ref, uploadBytes, getDownloadURL } from "firebase/storage";

const uploadImage = async (file: File) => {
    // 1. Create unique path
    const filename = `${Date.now()}_${file.name}`;
    const storageRef = ref(storage, `trucks/${filename}`);

    // 2. Upload
    const snapshot = await uploadBytes(storageRef, file);

    // 3. Get URL
    const url = await getDownloadURL(snapshot.ref);
    
    return url; // Save this string to Firestore!
};
```

### Real Project Example
*Reference: `app/admin/trucks/new/components/PhotosSection.tsx`*

In your project, we upload images **immediately** when the user selects them, so they can see a preview. The URLs are then added to the form state (`images` array) and saved to Firestore when the user clicks "Save".

---

## 4. Key Differences

| Feature | Create (`addDoc`) | Update (`updateDoc`) |
| :--- | :--- | :--- |
| **Reference** | `collection(db, "name")` | `doc(db, "name", "id")` |
| **Input** | Requires full data object | Requires ID + partial data |
| **TimestampField** | `createdAt` & `updatedAt` | `updatedAt` only |
| **Danger** | Creates duplicates if run twice | Fails if ID doesn't exist |

## 5. Security Note
Remember that even though this is "client-side" code, it is secure because of **Firestore Security Rules**. 
*   Ensure your rules (in `firestore.rules` or Firebase Console) allow `create` and `update` operations only for authenticated users (which you are checking in your component).

---

## 6. How It Works (Database vs Collection)

You may wonder how the code "knows" where to save data without downloading a schema.

### 1. The Default Database (Configured)
The codebase connects to the specific database because of the **Project ID** in your configuration.
*   **Where:** `firebase/client.ts`
*   **How:** `initializeApp` uses `NEXT_PUBLIC_FIREBASE_PROJECT_ID` to find the project, and `getFirestore(app)` connects to that project's **default** database.

### 2. The "Trucks" Collection (Declared)
The codebase doesn't "know" about the collection ahead of time. **Your code tells Firebase** where to go each time you run a command.

*   **Code**: `const trucksRef = collection(db, "trucks");`
*   **Logic**:
    *   This line tells Firebase: *"I want to target a collection named 'trucks'."*
    *   If "trucks" **exists**, Firebase uses it.
    *   If "trucks" **does NOT exist**, Firebase generally does nothing until you write data (`addDoc`/`setDoc`), at which point it **instantly creates** the collection for you.
