"use client"

import { useEffect, useMemo, useState } from "react"
import {
    collection,
    doc,
    getDocs,
    limit,
    onSnapshot,
    query,
    serverTimestamp,
    Timestamp,
    where,
    writeBatch,
} from "firebase/firestore"
import { ref as storageRef, uploadBytes, getDownloadURL } from "firebase/storage"
import { db, storage } from "@/firebase/client"
import { COLLECTIONS } from "@/lib/collections"
import { useLanguage } from "@/context/language"
import { format } from "date-fns"
import { Loader2, Upload, X } from "lucide-react"

import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select"

interface DriverOption {
    id: string
    authId?: string
    firstName?: string
    lastName?: string
}

interface TaskOption {
    id: string
    sourceHub?: string
    destination?: string
    date?: Date | null
    status?: string
    checkInAt?: Date | null
}

interface TripOption {
    id: string
    origin?: string
    destination?: string
    status?: string
    createdAt?: Date | null
    taskId?: string
}

interface Props {
    open: boolean
    onOpenChange: (open: boolean) => void
    onSaved?: () => void
}

function toDate(val: unknown): Date | null {
    if (!val) return null
    if (typeof val === "object" && val !== null && "toDate" in val && typeof (val as { toDate: unknown }).toDate === "function") {
        return (val as { toDate: () => Date }).toDate()
    }
    if (val instanceof Date) return val
    if (typeof val === "number" || typeof val === "string") return new Date(val)
    return null
}

function toLocalInputValue(d: Date | null): string {
    if (!d) return ""
    const pad = (n: number) => String(n).padStart(2, "0")
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const NONE_VALUE = "__none__"

export function StandbyBackfillDialog({ open, onOpenChange, onSaved }: Props) {
    const { t } = useLanguage()

    // Form state
    const [drivers, setDrivers] = useState<DriverOption[]>([])
    const [driverId, setDriverId] = useState<string>("")
    const [tasks, setTasks] = useState<TaskOption[]>([])
    const [taskId, setTaskId] = useState<string>(NONE_VALUE)
    const [trips, setTrips] = useState<TripOption[]>([])
    const [tripId, setTripId] = useState<string>(NONE_VALUE)
    const [origin, setOrigin] = useState<string>("")
    const [destination, setDestination] = useState<string>("")
    const [startedAtStr, setStartedAtStr] = useState<string>("")
    const [endedAtStr, setEndedAtStr] = useState<string>("")
    const [note, setNote] = useState<string>("")
    const [worksheetFile, setWorksheetFile] = useState<File | null>(null)
    const [siteFile, setSiteFile] = useState<File | null>(null)

    const [loadingTasks, setLoadingTasks] = useState(false)
    const [loadingTrips, setLoadingTrips] = useState(false)
    const [saving, setSaving] = useState(false)
    const [error, setError] = useState<string | null>(null)

    // Load drivers list
    useEffect(() => {
        if (!open) return
        const q = query(collection(db, COLLECTIONS.DRIVERS), limit(500))
        const unsub = onSnapshot(q, (snap) => {
            const list: DriverOption[] = snap.docs.map((d) => {
                const data = d.data()
                return {
                    id: d.id,
                    authId: data.authId,
                    firstName: data.firstName,
                    lastName: data.lastName,
                }
            })
            list.sort((a, b) =>
                `${a.firstName ?? ""} ${a.lastName ?? ""}`.localeCompare(`${b.firstName ?? ""} ${b.lastName ?? ""}`)
            )
            setDrivers(list)
        })
        return () => unsub()
    }, [open])

    // Reset form on close
    useEffect(() => {
        if (!open) {
            setDriverId("")
            setTaskId(NONE_VALUE)
            setTripId(NONE_VALUE)
            setOrigin("")
            setDestination("")
            setStartedAtStr("")
            setEndedAtStr("")
            setNote("")
            setWorksheetFile(null)
            setSiteFile(null)
            setError(null)
            setTasks([])
            setTrips([])
        }
    }, [open])

    // Load tasks for selected driver. tasks have driverId == authId on mobile/web writes.
    useEffect(() => {
        if (!open || !driverId) {
            setTasks([])
            return
        }
        const driver = drivers.find((d) => d.id === driverId)
        const driverAuthId = driver?.authId
        const candidateIds = [driverId, driverAuthId].filter(Boolean) as string[]
        if (candidateIds.length === 0) {
            setTasks([])
            return
        }
        setLoadingTasks(true)
        const q = query(
            collection(db, COLLECTIONS.TASKS),
            where("driverId", "in", candidateIds),
            limit(50)
        )
        getDocs(q)
            .then((snap) => {
                const list: TaskOption[] = snap.docs.map((d) => {
                    const data = d.data()
                    return {
                        id: d.id,
                        sourceHub: data.sourceHub,
                        destination: data.destination,
                        date: toDate(data.date) ?? toDate(data.createdAt),
                        status: data.status,
                        checkInAt: toDate(data.checkInAt),
                    }
                })
                list.sort((a, b) => (b.date?.getTime() ?? 0) - (a.date?.getTime() ?? 0))
                setTasks(list)
            })
            .catch((err) => {
                console.error("Error loading tasks:", err)
                setTasks([])
            })
            .finally(() => setLoadingTasks(false))
    }, [open, driverId, drivers])

    // Load trips for selected driver. trip_records.driverId stores authId.
    useEffect(() => {
        if (!open || !driverId) {
            setTrips([])
            return
        }
        const driver = drivers.find((d) => d.id === driverId)
        const driverAuthId = driver?.authId
        const candidateIds = [driverId, driverAuthId].filter(Boolean) as string[]
        if (candidateIds.length === 0) {
            setTrips([])
            return
        }
        setLoadingTrips(true)
        const q = query(
            collection(db, COLLECTIONS.TRIP_RECORDS),
            where("driverId", "in", candidateIds),
            limit(50)
        )
        getDocs(q)
            .then((snap) => {
                const list: TripOption[] = snap.docs.map((d) => {
                    const data = d.data()
                    return {
                        id: d.id,
                        origin: data.origin,
                        destination: data.destination,
                        status: data.status,
                        createdAt: toDate(data.createdAt),
                        taskId: data.taskId,
                    }
                })
                list.sort((a, b) => (b.createdAt?.getTime() ?? 0) - (a.createdAt?.getTime() ?? 0))
                setTrips(list)
            })
            .catch((err) => {
                console.error("Error loading trips:", err)
                setTrips([])
            })
            .finally(() => setLoadingTrips(false))
    }, [open, driverId, drivers])

    // Auto-fill origin/destination/check-in/trip when picking a task
    useEffect(() => {
        if (taskId === NONE_VALUE) return
        const t = tasks.find((x) => x.id === taskId)
        if (!t) return
        if (!origin && t.sourceHub) setOrigin(t.sourceHub)
        if (!destination && t.destination) setDestination(t.destination)
        if (!startedAtStr && t.checkInAt) {
            setStartedAtStr(toLocalInputValue(t.checkInAt))
        }
        // Auto-link the matching trip_record (trip.taskId === selected task.id)
        if (tripId === NONE_VALUE) {
            const matchedTrip = trips.find((tr) => tr.taskId === taskId)
            if (matchedTrip) setTripId(matchedTrip.id)
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [taskId, trips])

    // Auto-fill from trip
    useEffect(() => {
        if (tripId === NONE_VALUE) return
        const tr = trips.find((x) => x.id === tripId)
        if (!tr) return
        if (!origin && tr.origin) setOrigin(tr.origin)
        if (!destination && tr.destination) setDestination(tr.destination)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [tripId])

    const driverNameMap = useMemo(() => {
        const map: Record<string, string> = {}
        drivers.forEach((d) => {
            map[d.id] = `${d.firstName ?? ""} ${d.lastName ?? ""}`.trim() || d.id.slice(0, 8)
        })
        return map
    }, [drivers])

    async function uploadPhoto(recordId: string, photoType: string, file: File): Promise<string> {
        const sref = storageRef(storage, `standby_records/${recordId}/${photoType}.jpg`)
        await uploadBytes(sref, file, { contentType: file.type || "image/jpeg" })
        return getDownloadURL(sref)
    }

    async function handleSubmit() {
        setError(null)
        if (!driverId || !origin.trim() || !destination.trim() || !startedAtStr || !endedAtStr) {
            setError(t("standbyRecords.create.errorRequired", "Driver, origin, destination, check-in and end time are required"))
            return
        }
        const startedAt = new Date(startedAtStr)
        const endedAt = new Date(endedAtStr)
        if (Number.isNaN(startedAt.getTime()) || Number.isNaN(endedAt.getTime())) {
            setError(t("standbyRecords.create.errorRequired", "Driver, origin, destination, check-in and end time are required"))
            return
        }
        if (endedAt.getTime() <= startedAt.getTime()) {
            setError(t("standbyRecords.create.errorDateRange", "End time must be after check-in"))
            return
        }

        setSaving(true)
        try {
            const driver = drivers.find((d) => d.id === driverId)
            // Mobile flow stores driverId as auth uid; mirror that. Fall back to doc id.
            const driverAuthId = driver?.authId || driverId
            const recordRef = doc(collection(db, COLLECTIONS.STANDBY_RECORDS))
            const recordId = recordRef.id

            let worksheetUrl: string | null = null
            let siteUrl: string | null = null
            if (worksheetFile) {
                worksheetUrl = await uploadPhoto(recordId, "customer_worksheet", worksheetFile)
            }
            if (siteFile) {
                siteUrl = await uploadPhoto(recordId, "site_photo", siteFile)
            }

            const durationMinutes = Math.max(0, Math.round((endedAt.getTime() - startedAt.getTime()) / 60000))
            const photos: { url: string; type: string }[] = []
            if (worksheetUrl) photos.push({ url: worksheetUrl, type: "customer_worksheet" })
            if (siteUrl) photos.push({ url: siteUrl, type: "site_photo" })

            const finalTaskId = taskId === NONE_VALUE ? null : taskId
            const finalTripId = tripId === NONE_VALUE ? null : tripId

            const batch = writeBatch(db)
            batch.set(recordRef, {
                driverId: driverAuthId,
                taskId: finalTaskId,
                tripId: finalTripId,
                startLocation: origin.trim(),
                endLocation: destination.trim(),
                startedAt: Timestamp.fromDate(startedAt),
                endedAt: Timestamp.fromDate(endedAt),
                durationMinutes,
                photos,
                note: note.trim() ? note.trim() : null,
                status: "completed",
                lat: null,
                lng: null,
                createdAt: serverTimestamp(),
                updatedAt: serverTimestamp(),
                backfilledByAdmin: true,
            })

            if (finalTaskId) {
                batch.update(doc(db, COLLECTIONS.TASKS, finalTaskId), {
                    status: "Completed",
                    updatedAt: serverTimestamp(),
                })
            }
            if (finalTripId) {
                batch.update(doc(db, COLLECTIONS.TRIP_RECORDS, finalTripId), {
                    status: "standby",
                    updatedAt: serverTimestamp(),
                })
            }

            await batch.commit()
            onSaved?.()
            onOpenChange(false)
        } catch (err: unknown) {
            console.error("Error saving standby record:", err)
            const msg = err instanceof Error ? err.message : String(err)
            setError(`${t("standbyRecords.create.errorSave", "Failed to save standby record")}: ${msg}`)
        } finally {
            setSaving(false)
        }
    }

    return (
        <Dialog open={open} onOpenChange={(o) => !saving && onOpenChange(o)}>
            <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
                <DialogHeader>
                    <DialogTitle>{t("standbyRecords.create.title", "Backfill Standby Record")}</DialogTitle>
                    <DialogDescription>
                        {t("standbyRecords.create.subtitle", "Manually create a standby record for a past trip")}
                    </DialogDescription>
                </DialogHeader>

                <div className="grid gap-4 py-2">
                    {/* Driver */}
                    <div className="grid gap-2">
                        <Label>{t("standbyRecords.create.driver", "Driver")} *</Label>
                        <Select value={driverId} onValueChange={setDriverId}>
                            <SelectTrigger>
                                <SelectValue placeholder={t("standbyRecords.create.driverPlaceholder", "Select a driver")} />
                            </SelectTrigger>
                            <SelectContent className="z-[1005]" position="popper">
                                {drivers.map((d) => (
                                    <SelectItem key={d.id} value={d.id}>
                                        {driverNameMap[d.id]}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>

                    {/* Task + Trip side by side */}
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                        <div className="grid gap-2">
                            <Label>{t("standbyRecords.create.task", "Task (optional)")}</Label>
                            <Select value={taskId} onValueChange={setTaskId} disabled={!driverId || loadingTasks}>
                                <SelectTrigger>
                                    {taskId === NONE_VALUE ? (
                                        <SelectValue placeholder={
                                            loadingTasks
                                                ? t("standbyRecords.create.loadingTasks", "Loading tasks…")
                                                : t("standbyRecords.create.taskPlaceholder", "Select a task")
                                        } />
                                    ) : (
                                        <span className="font-mono text-sm truncate">{taskId}</span>
                                    )}
                                </SelectTrigger>
                                <SelectContent className="z-[1005]" position="popper">
                                    <SelectItem value={NONE_VALUE}>
                                        {t("standbyRecords.create.taskNone", "No task")}
                                    </SelectItem>
                                    {tasks.map((tk) => (
                                        <SelectItem key={tk.id} value={tk.id}>
                                            <span className="font-mono text-xs">{tk.id}</span>
                                            <span className="text-muted-foreground"> · </span>
                                            {tk.date ? format(tk.date, "dd/MM/yy") + " · " : ""}
                                            {tk.sourceHub ?? "-"} → {tk.destination ?? "-"}
                                            {tk.status ? ` (${tk.status})` : ""}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        <div className="grid gap-2">
                            <Label>{t("standbyRecords.create.trip", "Trip Record (optional)")}</Label>
                            <Select value={tripId} onValueChange={setTripId} disabled={!driverId || loadingTrips}>
                                <SelectTrigger>
                                    {tripId === NONE_VALUE ? (
                                        <SelectValue placeholder={
                                            loadingTrips
                                                ? t("standbyRecords.create.loadingTrips", "Loading trips…")
                                                : t("standbyRecords.create.tripPlaceholder", "Select a trip")
                                        } />
                                    ) : (
                                        <span className="font-mono text-sm truncate">{tripId}</span>
                                    )}
                                </SelectTrigger>
                                <SelectContent className="z-[1005]" position="popper">
                                    <SelectItem value={NONE_VALUE}>
                                        {t("standbyRecords.create.tripNone", "No trip")}
                                    </SelectItem>
                                    {trips.map((tr) => (
                                        <SelectItem key={tr.id} value={tr.id}>
                                            <span className="font-mono text-xs">{tr.id}</span>
                                            <span className="text-muted-foreground"> · </span>
                                            {tr.createdAt ? format(tr.createdAt, "dd/MM/yy") + " · " : ""}
                                            {tr.origin ?? "-"} → {tr.destination ?? "-"}
                                            {tr.status ? ` (${tr.status})` : ""}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    </div>

                    {/* Show resolved task/trip route as a hint when selected */}
                    {(taskId !== NONE_VALUE || tripId !== NONE_VALUE) && (
                        <div className="text-xs text-muted-foreground bg-muted/30 rounded px-3 py-2 -mt-2">
                            {(() => {
                                const tk = tasks.find((x) => x.id === taskId)
                                const tr = trips.find((x) => x.id === tripId)
                                const from = tk?.sourceHub ?? tr?.origin ?? "-"
                                const to = tk?.destination ?? tr?.destination ?? "-"
                                return <>{from} → {to}</>
                            })()}
                        </div>
                    )}

                    {/* Origin / Destination */}
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                        <div className="grid gap-2">
                            <Label>{t("standbyRecords.create.origin", "Origin")} *</Label>
                            <Input value={origin} onChange={(e) => setOrigin(e.target.value)} />
                        </div>
                        <div className="grid gap-2">
                            <Label>{t("standbyRecords.create.destination", "Destination")} *</Label>
                            <Input value={destination} onChange={(e) => setDestination(e.target.value)} />
                        </div>
                    </div>

                    {/* Dates */}
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                        <div className="grid gap-2">
                            <Label>{t("standbyRecords.create.startedAt", "Check-in at")} *</Label>
                            <Input
                                type="datetime-local"
                                value={startedAtStr}
                                onChange={(e) => {
                                    setStartedAtStr(e.target.value)
                                    if (!endedAtStr && e.target.value) {
                                        const d = new Date(e.target.value)
                                        if (!Number.isNaN(d.getTime())) {
                                            const plus30 = new Date(d.getTime() + 30 * 60 * 1000)
                                            setEndedAtStr(toLocalInputValue(plus30))
                                        }
                                    }
                                }}
                            />
                        </div>
                        <div className="grid gap-2">
                            <Label>{t("standbyRecords.create.endedAt", "Ended at")} *</Label>
                            <Input
                                type="datetime-local"
                                value={endedAtStr}
                                onChange={(e) => setEndedAtStr(e.target.value)}
                            />
                        </div>
                    </div>

                    {/* Note */}
                    <div className="grid gap-2">
                        <Label>{t("standbyRecords.create.note", "Note")}</Label>
                        <Textarea
                            value={note}
                            onChange={(e) => setNote(e.target.value)}
                            placeholder={t("standbyRecords.create.notePlaceholder", "Optional note")}
                            rows={2}
                        />
                    </div>

                    {/* Photos */}
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                        <PhotoSlot
                            label={t("standbyRecords.create.worksheetPhoto", "Customer Worksheet photo")}
                            file={worksheetFile}
                            onChange={setWorksheetFile}
                            uploadLabel={t("standbyRecords.create.uploadPhoto", "Upload photo")}
                            replaceLabel={t("standbyRecords.create.replacePhoto", "Replace")}
                            removeLabel={t("standbyRecords.create.removePhoto", "Remove")}
                        />
                        <PhotoSlot
                            label={t("standbyRecords.create.sitePhoto", "Site photo")}
                            file={siteFile}
                            onChange={setSiteFile}
                            uploadLabel={t("standbyRecords.create.uploadPhoto", "Upload photo")}
                            replaceLabel={t("standbyRecords.create.replacePhoto", "Replace")}
                            removeLabel={t("standbyRecords.create.removePhoto", "Remove")}
                        />
                    </div>

                    {error && (
                        <div className="text-sm text-red-600 bg-red-50 dark:bg-red-950/30 border border-red-200 dark:border-red-900 rounded-md px-3 py-2">
                            {error}
                        </div>
                    )}
                </div>

                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
                        {t("standbyRecords.create.cancel", "Cancel")}
                    </Button>
                    <Button onClick={handleSubmit} disabled={saving}>
                        {saving ? (
                            <>
                                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                                {t("standbyRecords.create.submitting", "Saving…")}
                            </>
                        ) : (
                            t("standbyRecords.create.submit", "Save Record")
                        )}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    )
}

interface PhotoSlotProps {
    label: string
    file: File | null
    onChange: (f: File | null) => void
    uploadLabel: string
    replaceLabel: string
    removeLabel: string
}

function PhotoSlot({ label, file, onChange, uploadLabel, replaceLabel, removeLabel }: PhotoSlotProps) {
    const previewUrl = useMemo(() => (file ? URL.createObjectURL(file) : null), [file])
    useEffect(() => {
        return () => {
            if (previewUrl) URL.revokeObjectURL(previewUrl)
        }
    }, [previewUrl])

    const inputId = `photo-${label.replace(/\s/g, "")}`

    return (
        <div className="grid gap-2">
            <Label>{label}</Label>
            <div className="border-2 border-dashed border-border rounded-lg p-3 flex flex-col items-center justify-center gap-2 min-h-[140px] bg-muted/20">
                {previewUrl ? (
                    <>
                        {/* eslint-disable-next-line @next/next/no-img-element */}
                        <img src={previewUrl} alt={label} className="max-h-24 rounded object-contain" />
                        <div className="flex items-center gap-2">
                            <Button asChild variant="outline" size="sm" type="button">
                                <label htmlFor={inputId} className="cursor-pointer">
                                    {replaceLabel}
                                </label>
                            </Button>
                            <Button variant="ghost" size="sm" type="button" onClick={() => onChange(null)}>
                                <X className="w-4 h-4 mr-1" />
                                {removeLabel}
                            </Button>
                        </div>
                    </>
                ) : (
                    <Button asChild variant="outline" size="sm" type="button">
                        <label htmlFor={inputId} className="cursor-pointer flex items-center">
                            <Upload className="w-4 h-4 mr-2" />
                            {uploadLabel}
                        </label>
                    </Button>
                )}
                <input
                    id={inputId}
                    type="file"
                    accept="image/*"
                    className="hidden"
                    onChange={(e) => {
                        const f = e.target.files?.[0] ?? null
                        onChange(f)
                        e.target.value = ""
                    }}
                />
            </div>
        </div>
    )
}
