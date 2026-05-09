"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { MessageCircle, Loader2 } from "lucide-react";
import { BroadcastComposer } from "./components/BroadcastComposer";

function ChatPageContent() {
  const searchParams = useSearchParams();
  const view = searchParams?.get("view");

  if (view === "broadcast") {
    return <BroadcastComposer />;
  }

  return (
    <div className="flex flex-col items-center justify-center flex-1 p-8 text-center">
      <MessageCircle className="h-16 w-16 text-muted-foreground/50 mb-4" />
      <h2 className="text-lg font-semibold text-foreground mb-1">Command Center</h2>
      <p className="text-muted-foreground text-sm max-w-sm">
        Select a conversation from the list to view messages, or use the driver panel on the right when a chat is selected.
      </p>
    </div>
  );
}

export default function AdminChatListPage() {
  return (
    <Suspense
      fallback={
        <div className="flex flex-1 items-center justify-center">
          <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
        </div>
      }
    >
      <ChatPageContent />
    </Suspense>
  );
}
