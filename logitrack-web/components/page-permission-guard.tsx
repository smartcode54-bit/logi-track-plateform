"use client"

import { usePermission } from "@/hooks/usePermission"
import type { CapabilityId } from "@/lib/capabilities"
import { Loader2, ShieldX } from "lucide-react"

export function PagePermissionGuard({
  capability,
  children,
}: {
  capability: CapabilityId
  children: React.ReactNode
}) {
  const { hasPermission, loading } = usePermission(capability)

  if (loading) {
    return (
      <div className="flex h-[50vh] items-center justify-center">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    )
  }

  if (!hasPermission) {
    return (
      <div className="flex h-[50vh] flex-col items-center justify-center gap-4 text-center">
        <ShieldX className="h-12 w-12 text-destructive/60" />
        <div className="space-y-2">
          <p className="text-lg font-semibold text-foreground">ไม่มีสิทธิ์เข้าถึงหน้านี้</p>
          <p className="text-sm text-muted-foreground">คุณไม่มีสิทธิ์ในการเข้าถึงหน้าที่ขอมา</p>
        </div>
      </div>
    )
  }

  return <>{children}</>
}
