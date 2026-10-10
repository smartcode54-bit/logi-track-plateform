"use client";

import Link from "next/link";
import { ArrowLeftIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useForgotPassword } from "../hooks/useForgotPassword";

export default function ForgotPasswordForm() {
  const { email, setEmail, loading, sent, error, handleSubmit, t } = useForgotPassword();

  return (
    <Card className="w-full max-w-md">
      <CardHeader>
        <CardTitle>{t("auth.forgot.title")}</CardTitle>
        <CardDescription>{t("auth.forgot.subtitle")}</CardDescription>
      </CardHeader>
      <CardContent>
        {sent ? (
          <p role="status" className="text-sm text-green-600">
            {t("auth.forgot.sent")}
          </p>
        ) : (
          <form onSubmit={handleSubmit}>
            <div className="flex flex-col gap-6">
              <div className="grid gap-2">
                <Label htmlFor="email">{t("auth.email")}</Label>
                <Input
                  id="email"
                  type="email"
                  autoComplete="email"
                  placeholder={t("auth.emailPlaceholder")}
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                />
              </div>
              {error ? (
                <p role="alert" className="text-sm text-destructive">
                  {error}
                </p>
              ) : null}
              <Button type="submit" className="w-full" disabled={loading || !email.trim()}>
                {loading ? t("auth.forgot.sending") : t("auth.forgot.submit")}
              </Button>
            </div>
          </form>
        )}
      </CardContent>
      <CardAction>
        <div className="flex justify-center">
          <Button variant="link" asChild>
            <Link className="text-sm hover:underline flex items-center gap-1" href="/login">
              <ArrowLeftIcon className="w-4 h-4" /> {t("auth.backToLogin")}
            </Link>
          </Button>
        </div>
      </CardAction>
    </Card>
  );
}
