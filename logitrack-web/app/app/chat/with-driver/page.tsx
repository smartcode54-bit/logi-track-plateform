"use client";

import { useEffect, Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import {
  collection,
  query,
  where,
  getDocs,
  addDoc,
  serverTimestamp,
} from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useAuth } from "@/context/auth";
import { Loader2 } from "lucide-react";

function ChatWithDriverContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const authId = searchParams.get("authId") ?? "";
  const auth = useAuth();
  const currentUser = auth?.currentUser ?? null;

  useEffect(() => {
    if (!authId || !currentUser?.uid) {
      if (!authId) router.replace("/app/chat");
      return;
    }

    (async () => {
      const chatsRef = collection(db, COLLECTIONS.CHATS);
      const q = query(chatsRef, where("driverId", "==", authId));
      const snap = await getDocs(q);
      let chatId: string;

      if (!snap.empty) {
        chatId = snap.docs[0].id;
      } else {
        let driverDisplayName = "Driver";
        const driversSnap = await getDocs(
          query(collection(db, COLLECTIONS.DRIVERS), where("authId", "==", authId))
        );
        if (!driversSnap.empty) {
          const d = driversSnap.docs[0].data();
          driverDisplayName = [d.firstName, d.lastName].filter(Boolean).join(" ") || driverDisplayName;
        }
        const newChat = await addDoc(chatsRef, {
          driverId: authId,
          driverDisplayName,
          status: "open",
          assignedAdminId: null,
          assignedAt: null,
          closedAt: null,
          priority: "normal",
          lastReadByAdmin: {},
          lastMessage: "",
          lastMessageAt: serverTimestamp(),
          lastMessageBy: "",
          createdAt: serverTimestamp(),
          updatedAt: serverTimestamp(),
        });
        chatId = newChat.id;
      }

      router.replace(`/app/chat/room?chatId=${chatId}`);
    })();
  }, [authId, currentUser?.uid, router]);

  return (
    <div className="flex items-center justify-center min-h-[50vh] gap-2">
      <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      <span className="text-muted-foreground">Opening chat...</span>
    </div>
  );
}

export default function ChatWithDriverPage() {
  return (
    <Suspense fallback={<div className="flex items-center justify-center min-h-[50vh]"><Loader2 className="h-6 w-6 animate-spin text-muted-foreground" /></div>}>
      <ChatWithDriverContent />
    </Suspense>
  );
}
