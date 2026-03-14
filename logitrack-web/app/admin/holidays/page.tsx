"use client";

import { useState, useEffect, useMemo, useRef } from "react";
import { collection, query, orderBy, onSnapshot, deleteDoc, doc, Timestamp } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Holiday } from "@/validate/holidaySchema";
import {
    Calendar as CalendarIcon,
    Plus,
    Loader2,
    MoreHorizontal,
    Trash2,
    Pencil,
    Search,
    ChevronRight,
    LayoutGrid,
    Calendar as CalendarViewIcon,
    List as ListViewIcon
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { useLanguage } from "@/context/language";
import { format, isSameMonth, isSameYear } from "date-fns";
import { toast } from "sonner";
import { AddHolidayDialog } from "./AddHolidayDialog";

import FullCalendar from "@fullcalendar/react";
import dayGridPlugin from "@fullcalendar/daygrid";
import multiMonthPlugin from "@fullcalendar/multimonth";
import interactionPlugin from "@fullcalendar/interaction";
import listPlugin from "@fullcalendar/list";

export default function HolidaysPage() {
    const { t } = useLanguage();
    const [holidays, setHolidays] = useState<Holiday[]>([]);
    const [loading, setLoading] = useState(true);
    const [searchQuery, setSearchQuery] = useState("");
    const [isAddDialogOpen, setIsAddDialogOpen] = useState(false);
    const [view, setView] = useState<"year" | "month" | "list">("month");
    const [selectedHoliday, setSelectedHoliday] = useState<Holiday | null>(null);
    const [initialDate, setInitialDate] = useState<string | undefined>(undefined);
    const [listFilter, setListFilter] = useState<"month" | "year" | "all">("month");
    const calendarRef = useRef<FullCalendar>(null);

    // Fetch holidays
    useEffect(() => {
        setLoading(true);
        const holidaysRef = collection(db, COLLECTIONS.HOLIDAYS);
        const q = query(holidaysRef, orderBy("date", "asc"));

        const unsubscribe = onSnapshot(q, (snapshot) => {
            const fetchedHolidays: Holiday[] = [];
            snapshot.forEach((doc) => {
                const data = doc.data();
                fetchedHolidays.push({
                    id: doc.id,
                    ...data,
                    date: data.date instanceof Timestamp ? data.date.toDate() : new Date(data.date),
                } as Holiday);
            });
            setHolidays(fetchedHolidays);
            setLoading(false);
        }, (err) => {
            console.error("Error fetching holidays:", err);
            toast.error("Failed to load holiday calendar");
            setLoading(false);
        });

        return () => unsubscribe();
    }, []);

    const filteredHolidays = useMemo(() => {
        const now = new Date();
        return holidays.filter(h => {
            const matchesSearch = h.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
                                 h.type.toLowerCase().includes(searchQuery.toLowerCase());
            
            if (!matchesSearch) return false;

            if (view === "list") {
                if (listFilter === "month") return isSameMonth(h.date, now);
                if (listFilter === "year") return isSameYear(h.date, now);
            }
            
            return true;
        });
    }, [holidays, searchQuery, view, listFilter]);

    const stats = useMemo(() => {
        const now = new Date();
        const upcoming = holidays.filter(h => h.date >= now).length;
        const recurring = holidays.filter(h => h.isRecurring).length;
        return { total: holidays.length, upcoming, recurring };
    }, [holidays]);

    const getTypeColor = (type: string) => {
        switch (type) {
            case "PUBLIC": return "bg-blue-500/10 text-blue-500 border-blue-500/20";
            case "COMPANY": return "bg-purple-500/10 text-purple-500 border-purple-500/20";
            default: return "bg-gray-500/10 text-gray-500 border-gray-500/20";
        }
    };

    const getTypeCalendarColor = (type: string) => {
        switch (type) {
            case "PUBLIC": return "#3b82f6"; // blue-500
            case "COMPANY": return "#a855f7"; // purple-500
            default: return "#6b7280"; // gray-500
        }
    };

    const handleDelete = async (id: string) => {
        if (!confirm("Are you sure you want to delete this holiday?")) return;
        try {
            await deleteDoc(doc(db, COLLECTIONS.HOLIDAYS, id));
            toast.success("Holiday deleted");
        } catch (error) {
            console.error("Error deleting holiday:", error);
            toast.error("Failed to delete holiday");
        }
    };

    const events = useMemo(() => {
        return holidays.map(h => ({
            id: h.id,
            title: h.name,
            start: h.date,
            allDay: true,
            backgroundColor: getTypeCalendarColor(h.type),
            borderColor: getTypeCalendarColor(h.type),
            extendedProps: { ...h }
        }));
    }, [holidays]);

    const handleDateClick = (arg: any) => {
        setInitialDate(arg.dateStr);
        setSelectedHoliday(null);
        setIsAddDialogOpen(true);
    };

    const handleEventClick = (arg: any) => {
        setSelectedHoliday(arg.event.extendedProps as Holiday);
        setInitialDate(undefined);
        setIsAddDialogOpen(true);
    };

    const handleAddClick = () => {
        setSelectedHoliday(null);
        setInitialDate(undefined);
        setIsAddDialogOpen(true);
    };

    const handleEditClick = (holiday: Holiday) => {
        setSelectedHoliday(holiday);
        setInitialDate(undefined);
        setIsAddDialogOpen(true);
    };

    const handleViewChange = (newView: "year" | "month" | "list") => {
        setView(newView);
        if (newView !== "list") {
            setTimeout(() => {
                const calendarApi = calendarRef.current?.getApi();
                if (calendarApi) {
                    calendarApi.today();
                    calendarApi.changeView(newView === "year" ? "multiMonthYear" : "dayGridMonth");
                }
            }, 0);
        }
    };

    return (
        <div className="container mx-auto p-6 space-y-6 max-w-[1200px]">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("nav.holidays")}</h1>
                    <p className="text-muted-foreground mt-1">
                        Manage company and public holidays for driver scheduling.
                    </p>
                </div>
                <div className="flex gap-3">
                    <div className="flex items-center bg-muted rounded-lg p-1">
                        <Button 
                            variant={view === "year" ? "secondary" : "ghost"} 
                            size="sm" 
                            onClick={() => handleViewChange("year")}
                            className="px-3"
                        >
                            <LayoutGrid className="h-4 w-4 mr-2" />
                            Year
                        </Button>
                        <Button 
                            variant={view === "month" ? "secondary" : "ghost"} 
                            size="sm" 
                            onClick={() => handleViewChange("month")}
                            className="px-3"
                        >
                            <CalendarViewIcon className="h-4 w-4 mr-2" />
                            Month
                        </Button>
                        <Button 
                            variant={view === "list" ? "secondary" : "ghost"} 
                            size="sm" 
                            onClick={() => handleViewChange("list")}
                            className="px-3"
                        >
                            <ListViewIcon className="h-4 w-4 mr-2" />
                            List
                        </Button>
                    </div>
                    <Button onClick={handleAddClick} className="bg-blue-600 hover:bg-blue-700 text-white">
                        <Plus className="mr-2 h-4 w-4" />
                        Add Holiday
                    </Button>
                </div>
            </div>

            <AddHolidayDialog 
                open={isAddDialogOpen} 
                onOpenChange={setIsAddDialogOpen} 
                initialDate={initialDate}
                editData={selectedHoliday}
            />

            {/* Stats */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Card>
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">Total Holidays</p>
                            <h2 className="text-3xl font-bold">{stats.total}</h2>
                        </div>
                        <CalendarIcon className="h-8 w-8 text-blue-500" />
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">Upcoming</p>
                            <h2 className="text-3xl font-bold">{stats.upcoming}</h2>
                        </div>
                        <ChevronRight className="h-8 w-8 text-green-500" />
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">Recurring</p>
                            <h2 className="text-3xl font-bold">{stats.recurring}</h2>
                        </div>
                        <CalendarIcon className="h-8 w-8 text-purple-500 opacity-50" />
                    </CardContent>
                </Card>
            </div>

            {/* Content */}
            <div className="space-y-4">
                {view === "list" ? (
                    <>
                        <div className="flex flex-col md:flex-row items-center gap-4 bg-card/50 p-4 rounded-lg border border-border/50">
                            <div className="relative flex-1 w-full max-w-sm">
                                <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                                <Input
                                    placeholder="Search holidays..."
                                    className="pl-10"
                                    value={searchQuery}
                                    onChange={(e) => setSearchQuery(e.target.value)}
                                />
                            </div>
                            <div className="flex items-center gap-2 bg-muted rounded-lg p-1 w-full md:w-auto">
                                <Button 
                                    variant={listFilter === "month" ? "secondary" : "ghost"} 
                                    size="sm" 
                                    onClick={() => setListFilter("month")}
                                    className="flex-1 md:flex-none px-3 h-8 text-xs"
                                >
                                    Current Month
                                </Button>
                                <Button 
                                    variant={listFilter === "year" ? "secondary" : "ghost"} 
                                    size="sm" 
                                    onClick={() => setListFilter("year")}
                                    className="flex-1 md:flex-none px-3 h-8 text-xs"
                                >
                                    Current Year
                                </Button>
                                <Button 
                                    variant={listFilter === "all" ? "secondary" : "ghost"} 
                                    size="sm" 
                                    onClick={() => setListFilter("all")}
                                    className="flex-1 md:flex-none px-3 h-8 text-xs"
                                >
                                    Show All
                                </Button>
                            </div>
                        </div>

                        <div className="border rounded-lg bg-card overflow-hidden">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>Holiday Name</TableHead>
                                        <TableHead>Date</TableHead>
                                        <TableHead>Type</TableHead>
                                        <TableHead>Recurring</TableHead>
                                        <TableHead className="text-right">Actions</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {loading ? (
                                        <TableRow>
                                            <TableCell colSpan={5} className="h-32 text-center">
                                                <div className="flex flex-col items-center justify-center gap-2">
                                                    <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                                    <p className="text-sm text-muted-foreground">Loading holidays...</p>
                                                </div>
                                            </TableCell>
                                        </TableRow>
                                    ) : filteredHolidays.length === 0 ? (
                                        <TableRow>
                                            <TableCell colSpan={5} className="h-32 text-center text-muted-foreground">
                                                No holidays found for this period.
                                            </TableCell>
                                        </TableRow>
                                    ) : (
                                        filteredHolidays.map((holiday) => (
                                            <TableRow key={holiday.id}>
                                                <TableCell className="font-medium">{holiday.name}</TableCell>
                                                <TableCell>{format(holiday.date, "PPP")}</TableCell>
                                                <TableCell>
                                                    <Badge variant="outline" className={getTypeColor(holiday.type)}>
                                                        {holiday.type}
                                                    </Badge>
                                                </TableCell>
                                                <TableCell>
                                                    {holiday.isRecurring ? (
                                                        <Badge variant="secondary">Annual</Badge>
                                                    ) : (
                                                        <span className="text-muted-foreground text-sm">One-time</span>
                                                    )}
                                                </TableCell>
                                                <TableCell className="text-right">
                                                    <DropdownMenu>
                                                        <DropdownMenuTrigger asChild>
                                                            <Button variant="ghost" size="icon">
                                                                <MoreHorizontal className="h-4 w-4" />
                                                            </Button>
                                                        </DropdownMenuTrigger>
                                                        <DropdownMenuContent align="end">
                                                            <DropdownMenuLabel>Actions</DropdownMenuLabel>
                                                            <DropdownMenuItem onClick={() => handleEditClick(holiday)}>
                                                                <Pencil className="mr-2 h-4 w-4" /> Edit
                                                            </DropdownMenuItem>
                                                            <DropdownMenuItem 
                                                                className="text-red-600"
                                                                onClick={() => holiday.id && handleDelete(holiday.id)}
                                                            >
                                                                <Trash2 className="mr-2 h-4 w-4" /> Delete
                                                            </DropdownMenuItem>
                                                        </DropdownMenuContent>
                                                    </DropdownMenu>
                                                </TableCell>
                                            </TableRow>
                                        ))
                                    )}
                                </TableBody>
                            </Table>
                        </div>
                    </>
                ) : (
                    <Card className="p-4 bg-card border-border">
                        <div className="calendar-container">
                            <FullCalendar
                                ref={calendarRef}
                                plugins={[dayGridPlugin, multiMonthPlugin, interactionPlugin, listPlugin]}
                                initialView={view === "year" ? "multiMonthYear" : "dayGridMonth"}
                                headerToolbar={{
                                    left: "prev,next today",
                                    center: "title",
                                    right: "" 
                                }}
                                events={events}
                                dateClick={handleDateClick}
                                eventClick={handleEventClick}
                                height="auto"
                                themeSystem="standard"
                                multiMonthMaxColumns={view === "year" ? 3 : 1}
                                eventClassNames="cursor-pointer hover:opacity-80 transition-opacity"
                                dayMaxEvents={true}
                                fixedWeekCount={false}
                                showNonCurrentDates={view === "month" ? false : true}
                            />
                        </div>
                        <style jsx global>{`
                            .fc {
                                --fc-border-color: var(--border);
                                --fc-daygrid-dot-event-hover-bg-color: var(--accent);
                                --fc-page-bg-color: transparent;
                                --fc-neutral-bg-color: transparent;
                                --fc-list-event-hover-bg-color: var(--accent);
                                --fc-today-bg-color: color-mix(in srgb, var(--primary) 10%, transparent);
                                color: var(--foreground);
                            }
                            .fc .fc-toolbar-title {
                                font-size: 1.25rem;
                                font-weight: 600;
                            }
                            .fc .fc-button-primary {
                                background-color: var(--secondary);
                                border-color: var(--border);
                                color: var(--secondary-foreground);
                            }
                            .fc .fc-button-primary:hover {
                                background-color: var(--accent);
                                border-color: var(--border);
                                color: var(--accent-foreground);
                            }
                            .fc .fc-button-primary:disabled {
                                opacity: 0.5;
                            }
                            .fc .fc-button-active {
                                background-color: var(--primary) !important;
                                border-color: var(--primary) !important;
                                color: var(--primary-foreground) !important;
                            }
                            .fc-theme-standard td, .fc-theme-standard th {
                                border-color: var(--border);
                            }
                            .fc .fc-multimonth-month {
                                border: 1px solid var(--border);
                                border-radius: 0.5rem;
                                overflow: hidden;
                                margin-bottom: 1rem;
                            }
                            .fc-multimonth-title {
                                background: var(--muted);
                                padding: 0.5rem !important;
                                border-bottom: 1px solid var(--border);
                                color: var(--muted-foreground);
                            }
                            .fc-day-other {
                                opacity: 0.3;
                            }
                            .dark .fc .fc-list-day-cushion {
                                background-color: var(--muted);
                            }
                        `}</style>
                    </Card>
                )}
            </div>
        </div>
    );
}
