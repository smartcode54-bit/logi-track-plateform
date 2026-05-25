"use client";

import {
  collection,
  doc,
  getDoc,
  getDocs,
  addDoc,
  updateDoc,
  query,
  where,
  orderBy,
  serverTimestamp,
  limit,
} from "firebase/firestore";
import { ref, uploadBytes, getDownloadURL } from "firebase/storage";
import { db, storage } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import type { Company, CompanyFormValues } from "@/validate/companySchema";

export type CompanyWithId = Company & { id: string };

/**
 * Fetch the OWNER company (the logistics operator's own company profile).
 * Returns null if no owner company document exists yet.
 */
export async function getOwnerCompany(): Promise<CompanyWithId | null> {
  const q = query(
    collection(db, COLLECTIONS.COMPANIES),
    where("companyType", "==", "owner"),
    limit(1)
  );
  const snap = await getDocs(q);
  if (snap.empty) return null;
  const d = snap.docs[0];
  return { id: d.id, ...(d.data() as Company) };
}

/**
 * Fetch a single company by its Firestore document ID.
 */
export async function getCompanyById(id: string): Promise<CompanyWithId | null> {
  const snap = await getDoc(doc(db, COLLECTIONS.COMPANIES, id));
  if (!snap.exists()) return null;
  return { id: snap.id, ...(snap.data() as Company) };
}

/**
 * Fetch all companies (admin view).
 */
export async function getCompanies(): Promise<CompanyWithId[]> {
  const q = query(
    collection(db, COLLECTIONS.COMPANIES),
    orderBy("companyType", "asc"),
    orderBy("nameTh", "asc")
  );
  const snap = await getDocs(q);
  return snap.docs.map((d) => ({ id: d.id, ...(d.data() as Company) }));
}

/**
 * Create a new company document.
 * Returns the new document ID.
 */
export async function createCompany(
  data: CompanyFormValues
): Promise<string> {
  const docRef = await addDoc(collection(db, COLLECTIONS.COMPANIES), {
    ...data,
    createdAt: serverTimestamp(),
    updatedAt: serverTimestamp(),
  });
  return docRef.id;
}

/**
 * Update an existing company document (partial update).
 */
export async function updateCompany(
  id: string,
  data: Partial<CompanyFormValues>
): Promise<void> {
  await updateDoc(doc(db, COLLECTIONS.COMPANIES, id), {
    ...data,
    updatedAt: serverTimestamp(),
  });
}

/**
 * Upload a company asset (logo / stamp / signature) to Firebase Storage.
 * Storage path: companies/{companyId}/{type}.{ext}
 * Returns the public download URL.
 */
export async function uploadCompanyAsset(
  companyId: string,
  type: "logo" | "stamp" | "signature",
  file: File
): Promise<string> {
  const ext = file.name.split(".").pop() ?? "png";
  const storageRef = ref(storage, `companies/${companyId}/${type}.${ext}`);
  await uploadBytes(storageRef, file);
  return await getDownloadURL(storageRef);
}
