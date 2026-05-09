"use client";

import Link from "next/link";
import Navigation from "@/components/navigation";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { HelpCircle, Mail, MessageCircle, FileText, ArrowLeft } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/context/auth";

export default function SupportPage() {
  const auth = useAuth();
  const isAdmin = auth?.customClaims?.admin === true;

  return (
    <div className="min-h-screen bg-background flex flex-col">
      <Navigation />
      <main className="flex-1 container max-w-3xl mx-auto px-4 py-12">
        <div className="mb-8">
          <button
            onClick={() => window.history.back()}
            className="inline-flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground transition-colors cursor-pointer"
          >
            <ArrowLeft className="h-4 w-4" />
            Go Back
          </button>
        </div>

        <div className="flex items-center gap-3 mb-2">
          <div className="h-12 w-12 rounded-lg bg-primary/10 flex items-center justify-center">
            <HelpCircle className="h-6 w-6 text-primary" />
          </div>
          <div>
            <h1 className="text-2xl font-bold tracking-tight">Support Center</h1>
            <p className="text-muted-foreground text-sm">
              Get help and contact the LogisticsPro team
            </p>
          </div>
        </div>

        <div className="grid gap-4 mt-8">
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <Mail className="h-4 w-4" />
                Email support
              </CardTitle>
              <CardDescription>
                Send your questions or issues to our support team. We typically respond within 1–2 business days.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <a
                href="https://mail.google.com/mail/?view=cm&fs=1&to=smartcode54@gmail.com&su=แจ้งปัญหาระบบ Logitrack web/mobile"
                target="_blank"
                rel="noopener noreferrer"
                className="text-primary hover:underline font-medium"
              >
                smartcode54@gmail.com
              </a>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <MessageCircle className="h-4 w-4" />
                In-app chat (Admin)
              </CardTitle>
              <CardDescription>
                Use the Chat section in the admin panel to communicate with drivers and manage conversations.
              </CardDescription>
            </CardHeader>
            <CardContent>
              {isAdmin ? (
                <Button asChild variant="outline" size="sm">
                  <Link href="/app/chat" prefetch={false}>
                    Open Admin Chat
                  </Link>
                </Button>
              ) : (
                <p className="text-sm text-muted-foreground">
                  Sign in as an administrator to access the chat.
                </p>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <FileText className="h-4 w-4" />
                Documentation & resources
              </CardTitle>
              <CardDescription>
                Guides and references are available inside the admin dashboard. Check the help tips on each page.
              </CardDescription>
            </CardHeader>
          </Card>
        </div>

        <p className="mt-8 text-center text-sm text-muted-foreground">
          LogisticsPro · Logi Track Platform
        </p>
      </main>
    </div>
  );
}
