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
import { queryOptions } from "@tanstack/react-query";
import { db, storage } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { stripUndefined } from "@/lib/firestoreWrite";
import type { Company, CompanyFormValues } from "@/validate/companySchema";

export type CompanyWithId = Company & { id: string };

/**
 * Read the OWNER company (the logistics operator's own company profile) from Firestore: the P0 source
 * of `['companies',{owner:true}]`. Null if no owner company document exists yet.
 */
export async function fetchOwnerCompanyFromFirestore(): Promise<CompanyWithId | null> {
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
 * `['companies',{owner:true}]` (developer-spec.md §10.7, Appendix E §E.4 row "owner company"): the
 * invoice header shared by Billing Document, the Shopee report and the company profile page, 10 min
 * stale. Go source `GET /v1/companies/owner` in P1.
 */
export const ownerCompanyQueryOptions = queryOptions({
  queryKey: queryKeys.companies.owner(),
  queryFn: fetchOwnerCompanyFromFirestore,
  ...QUERY_POLICY.masterData,
  refetchOnWindowFocus: true,
});

/**
 * The owner company from the tab's cache (one read per stale time for the three pages). An edit form
 * passes `{ fresh: true }` so it never starts from a copy older than the stored profile; that read
 * also refreshes the cache for the other pages.
 */
export function getOwnerCompany(options: { fresh?: boolean } = {}): Promise<CompanyWithId | null> {
  return getQueryClient().fetchQuery(options.fresh ? { ...ownerCompanyQueryOptions, staleTime: 0 } : ownerCompanyQueryOptions);
}

/** After a company write: the owner company and any company list refetch. */
export function invalidateCompanies(): Promise<void> {
  return getQueryClient().invalidateQueries({ queryKey: queryKeys.companies.all() });
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
    ...stripUndefined(data as Record<string, unknown>),
    createdAt: serverTimestamp(),
    updatedAt: serverTimestamp(),
  });
  await invalidateCompanies();
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
    ...stripUndefined(data as Record<string, unknown>),
    updatedAt: serverTimestamp(),
  });
  await invalidateCompanies();
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
