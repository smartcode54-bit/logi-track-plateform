"use client";

import { useState, useEffect, useMemo, useRef } from "react";
import { collection, query, orderBy, onSnapshot, doc, Timestamp } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import { db, functions } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Holiday, HOLIDAY_TYPE_ENUM } from "@/validate/holidaySchema";
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
    List as ListViewIcon,
    Sparkles,
    Check,
    X
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
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { useLanguage } from "@/context/language";
import { format, isSameMonth, isSameYear } from "date-fns";
import { th } from "date-fns/locale";
import { toast } from "sonner";
import { AddHolidayDialog } from "./AddHolidayDialog";
import { YearSelectDialog } from "./YearSelectDialog";
import { ReviewGeneratedDialog } from "./ReviewGeneratedDialog";
import { generateThaiPublicHolidays } from "shared-docs/logic/thaiHolidays";
import {
    Tooltip,
    TooltipContent,
    TooltipProvider,
    TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

import FullCalendar from "@fullcalendar/react";
import dayGridPlugin from "@fullcalendar/daygrid";
import multiMonthPlugin from "@fullcalendar/multimonth";
import interactionPlugin from "@fullcalendar/interaction";
import listPlugin from "@fullcalendar/list";

export default function HolidaysPage() {
    const { t, language } = useLanguage();

    // Helper to get language-specific name with fallback for legacy data
    const getHolidayName = (h: Holiday) => {
        if (language === "th") {
            if (h.holidayNameTH) return h.holidayNameTH;
            const match = h.name.match(/\(([^)]+)\)/);
            return match ? match[1] : h.name;
        } else {
            if (h.holidayNameEN) return h.holidayNameEN;
            return h.name.split(" (")[0];
        }
    };

    const formatHolidayDate = (date: Date) =>
        format(date, "PPP", { locale: language === "th" ? th : undefined });

    const getHolidayDescription = (h: Holiday) => {
        if (language === "th") {
            if (h.descriptionTh) return h.descriptionTh;
            const match = h.description?.match(/\(([^)]+)\)/);
            return match ? match[1] : h.description;
        } else {
            if (h.descriptionEn) return h.descriptionEn;
            return h.description?.split(" (")[0];
        }
    };
    const [holidays, setHolidays] = useState<Holiday[]>([]);
    const [loading, setLoading] = useState(true);
    const [isGenerating, setIsGenerating] = useState(false);
    const [isSaving, setIsSaving] = useState(false);
    
    // Dialog states
    const [isYearSelectOpen, setIsYearSelectOpen] = useState(false);
    const [isReviewOpen, setIsReviewOpen] = useState(false);
    const [generatedHolidays, setGeneratedHolidays] = useState<Partial<Holiday>[]>([]);
    
    const [searchQuery, setSearchQuery] = useState("");
    const [isAddDialogOpen, setIsAddDialogOpen] = useState(false);
    const [view, setView] = useState<"year" | "month" | "list">("month");
    const [selectedHoliday, setSelectedHoliday] = useState<Holiday | null>(null);
    const [initialDate, setInitialDate] = useState<string | undefined>(undefined);
    const [listFilter, setListFilter] = useState<"month" | "year" | "all">("year"); // Default to year
    const calendarRef = useRef<FullCalendar>(null);

    // Fetch holidays from Firebase
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
            toast.error(t("holidays.toast.failedLoad"));
            setLoading(false);
        });

        return () => unsubscribe();
    }, [t]);

    const filteredHolidays = useMemo(() => {
        const now = new Date();
        const searchLower = searchQuery.toLowerCase();
        return holidays.filter(h => {
            const matchesSearch = 
                h.name.toLowerCase().includes(searchLower) ||
                (h.holidayNameEN && h.holidayNameEN.toLowerCase().includes(searchLower)) ||
                (h.holidayNameTH && h.holidayNameTH.toLowerCase().includes(searchLower)) ||
                h.type.toLowerCase().includes(searchLower);

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
        if (!confirm(t("holidays.deleteConfirm"))) return;
        try {
            const deleteHolidayFn = httpsCallable<{ id: string }, { ok: boolean }>(functions, "deleteHoliday");
            await deleteHolidayFn({ id });
            toast.success(t("holidays.toast.deleted"));
        } catch (error) {
            console.error("Error deleting holiday:", error);
            toast.error(t("holidays.toast.failedDelete"));
        }
    };

    const handleYearSelected = (year: number) => {
        setIsGenerating(true);
        setTimeout(() => {
            try {
                const generated = generateThaiPublicHolidays(year);
                setGeneratedHolidays(generated);
                setIsReviewOpen(true);
            } catch (error) {
                console.error("Error generating holidays:", error);
                toast.error(t("holidays.toast.failedGenerate"));
            } finally {
                setIsGenerating(false);
            }
        }, 500);
    };

    /** Calls the saveGeneratedHolidays Cloud Function (onCall + httpsCallable). See docs/CALLABLE_FUNCTIONS.md. */
    const handleSaveGenerated = async (finalHolidays: Partial<Holiday>[], initialHolidays: Partial<Holiday>[]) => {
        setIsSaving(true);
        try {
            const serialize = (h: Partial<Holiday>) => ({
                ...h,
                date: h.date ? format(h.date as Date, "yyyy-MM-dd") : "",
            });
            const saveGeneratedHolidays = httpsCallable<
                { finalHolidays: Record<string, unknown>[]; initialHolidays: Record<string, unknown>[] },
                { saved: number; deleted: number }
            >(functions, "saveGeneratedHolidays");
            await saveGeneratedHolidays({
                finalHolidays: finalHolidays.map(serialize),
                initialHolidays: initialHolidays.map(serialize),
            });
            toast.success(t("holidays.toast.saved", { count: finalHolidays.length }));
            setIsReviewOpen(false);
            setGeneratedHolidays([]);
        } catch (error) {
            console.error("Error saving holidays:", error);
            toast.error(t("holidays.toast.failedSave"));
            throw error;
        } finally {
            setIsSaving(false);
        }
    };

    const holidayDates = useMemo(() => {
        const set = new Set<string>();
        holidays.forEach(h => set.add(format(h.date, "yyyy-MM-dd")));
        return set;
    }, [holidays]);

    const events = useMemo(() => {
        return holidays.map(h => {
            return {
                id: h.id,
                title: getHolidayName(h),
                start: h.date,
                allDay: true,
                backgroundColor: getTypeCalendarColor(h.type),
                borderColor: "transparent",
                extendedProps: { ...h },
                display: 'block'
            };
        });
    }, [holidays, language]);

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
        <TooltipProvider>
            <div className="container mx-auto p-6 space-y-6 max-w-[1200px]">
                {/* Header */}
                <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                    <div>
                        <h1 className="text-3xl font-bold tracking-tight">{t("nav.holidays")}</h1>
                        <p className="text-muted-foreground mt-1">
                            {t("holidays.manageHolidays")}
                        </p>
                    </div>
                    <div className="flex flex-wrap gap-3">
                        <Button 
                            variant="outline" 
                            onClick={() => setIsYearSelectOpen(true)}
                            disabled={isGenerating || isSaving}
                            className="border-blue-500/50 text-blue-500 hover:bg-blue-500/10"
                        >
                            {isGenerating ? (
                                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                            ) : (
                                <Sparkles className="mr-2 h-4 w-4" />
                            )}
                            {t("holidays.generateHolidays")}
                        </Button>
                        
                        <div className="flex items-center bg-muted/50 rounded-xl p-1 gap-1 border border-border/50">
                            <Button 
                                variant="ghost" 
                                size="sm" 
                                onClick={() => handleViewChange("year")}
                                className={cn(
                                    "px-4 h-8 rounded-lg transition-all duration-200 text-xs font-semibold",
                                    view === "year" 
                                        ? "bg-blue-600 text-white shadow-sm hover:bg-blue-700 hover:text-white" 
                                        : "text-muted-foreground hover:bg-muted hover:text-foreground"
                                )}
                            >
                                <LayoutGrid className="h-3.5 w-3.5 mr-2" />
                                {t("holidays.viewYear")}
                            </Button>
                            <Button 
                                variant="ghost" 
                                size="sm" 
                                onClick={() => handleViewChange("month")}
                                className={cn(
                                    "px-4 h-8 rounded-lg transition-all duration-200 text-xs font-semibold",
                                    view === "month" 
                                        ? "bg-blue-600 text-white shadow-sm hover:bg-blue-700 hover:text-white" 
                                        : "text-muted-foreground hover:bg-muted hover:text-foreground"
                                )}
                            >
                                <CalendarViewIcon className="h-3.5 w-3.5 mr-2" />
                                {t("holidays.viewMonth")}
                            </Button>
                            <Button 
                                variant="ghost" 
                                size="sm" 
                                onClick={() => handleViewChange("list")}
                                className={cn(
                                    "px-4 h-8 rounded-lg transition-all duration-200 text-xs font-semibold",
                                    view === "list" 
                                        ? "bg-blue-600 text-white shadow-sm hover:bg-blue-700 hover:text-white" 
                                        : "text-muted-foreground hover:bg-muted hover:text-foreground"
                                )}
                            >
                                <ListViewIcon className="h-3.5 w-3.5 mr-2" />
                                {t("holidays.viewList")}
                            </Button>
                        </div>
                        <Button onClick={handleAddClick} className="bg-blue-600 hover:bg-blue-700 text-white px-6 shadow-sm">
                            <Plus className="mr-2 h-4 w-4" />
                            {t("holidays.addHoliday")}
                        </Button>
                    </div>
                </div>

                <AddHolidayDialog 
                    open={isAddDialogOpen} 
                    onOpenChange={setIsAddDialogOpen} 
                    initialDate={initialDate}
                    editData={selectedHoliday}
                />

                <YearSelectDialog 
                    open={isYearSelectOpen} 
                    onOpenChange={setIsYearSelectOpen} 
                    onSelect={handleYearSelected}
                />

                <ReviewGeneratedDialog 
                    open={isReviewOpen}
                    onOpenChange={setIsReviewOpen}
                    holidays={generatedHolidays}
                    onSave={handleSaveGenerated}
                />

                {/* Stats */}
                <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                    <Card className="shadow-sm border-border/60">
                        <CardContent className="p-6 flex items-center justify-between">
                            <div>
                                <p className="text-sm font-medium text-muted-foreground">{t("holidays.totalHolidays")}</p>
                                <h2 className="text-3xl font-bold">{stats.total}</h2>
                            </div>
                            <CalendarIcon className="h-8 w-8 text-blue-500/80" />
                        </CardContent>
                    </Card>
                    <Card className="shadow-sm border-border/60">
                        <CardContent className="p-6 flex items-center justify-between">
                            <div>
                                <p className="text-sm font-medium text-muted-foreground">{t("holidays.upcoming")}</p>
                                <h2 className="text-3xl font-bold">{stats.upcoming}</h2>
                            </div>
                            <ChevronRight className="h-8 w-8 text-green-500/80" />
                        </CardContent>
                    </Card>
                    <Card className="shadow-sm border-border/60">
                        <CardContent className="p-6 flex items-center justify-between">
                            <div>
                                <p className="text-sm font-medium text-muted-foreground">{t("holidays.recurring")}</p>
                                <h2 className="text-3xl font-bold">{stats.recurring}</h2>
                            </div>
                            <CalendarIcon className="h-8 w-8 text-purple-500/40" />
                        </CardContent>
                    </Card>
                </div>

                {/* Content */}
                <div className="space-y-4">
                    {view === "list" ? (
                        <>
                            <div className="flex flex-col md:flex-row items-center gap-4 bg-card/50 p-4 rounded-xl border border-border/50 shadow-sm">
                                <div className="relative flex-1 w-full max-w-sm">
                                    <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                                    <Input
                                        placeholder={t("holidays.searchPlaceholder")}
                                        className="pl-10 h-10 rounded-lg"
                                        value={searchQuery}
                                        onChange={(e) => setSearchQuery(e.target.value)}
                                    />
                                </div>
                                <div className="flex items-center gap-2 bg-muted/50 rounded-lg p-1 w-full md:w-auto border border-border/50">
                                    <Button 
                                        variant={listFilter === "month" ? "secondary" : "ghost"} 
                                        size="sm" 
                                        onClick={() => setListFilter("month")}
                                        className={cn("flex-1 md:flex-none px-3 h-8 text-[11px] font-bold uppercase tracking-wider", listFilter === "month" && "bg-background shadow-sm")}
                                    >
                                        {t("holidays.thisMonth")}
                                    </Button>
                                    <Button 
                                        variant={listFilter === "year" ? "secondary" : "ghost"} 
                                        size="sm" 
                                        onClick={() => setListFilter("year")}
                                        className={cn("flex-1 md:flex-none px-3 h-8 text-[11px] font-bold uppercase tracking-wider", listFilter === "year" && "bg-background shadow-sm")}
                                    >
                                        {t("holidays.thisYear")}
                                    </Button>
                                    <Button 
                                        variant={listFilter === "all" ? "secondary" : "ghost"} 
                                        size="sm" 
                                        onClick={() => setListFilter("all")}
                                        className={cn("flex-1 md:flex-none px-3 h-8 text-[11px] font-bold uppercase tracking-wider", listFilter === "all" && "bg-background shadow-sm")}
                                    >
                                        {t("holidays.showAll")}
                                    </Button>
                                </div>
                            </div>

                            <div className="border rounded-xl bg-card overflow-hidden shadow-sm">
                                <Table className="table-fixed w-full">
                                    <TableHeader className="bg-muted/30">
                                        <TableRow>
                                            <TableHead className="font-bold w-12 text-center">{t("holidays.table.no")}</TableHead>
                                            <TableHead className="font-bold w-2/5 min-w-0">{t("holidays.table.name")}</TableHead>
                                            <TableHead className="font-bold w-1/4">{t("holidays.table.date")}</TableHead>
                                            <TableHead className="font-bold w-1/5">{t("holidays.table.type")}</TableHead>
                                            <TableHead className="font-bold w-20 text-right">{t("holidays.table.actions")}</TableHead>
                                        </TableRow>
                                    </TableHeader>
                                    <TableBody>
                                        {/* Firebase Rows */}
                                        {loading ? (
                                            <TableRow>
                                                <TableCell colSpan={5} className="h-32 text-center">
                                                    <div className="flex flex-col items-center justify-center gap-2">
                                                        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                                        <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
                                                    </div>
                                                </TableCell>
                                            </TableRow>
                                        ) : filteredHolidays.length === 0 ? (
                                            <TableRow>
                                                <TableCell colSpan={5} className="h-32 text-center text-muted-foreground">
                                                    {t("holidays.noHolidays")}
                                                </TableCell>
                                            </TableRow>
                                        ) : (
                                            filteredHolidays.map((holiday, index) => (
                                                    <TableRow key={holiday.id} className="hover:bg-muted/20">
                                                        <TableCell className="w-12 text-center align-middle text-muted-foreground tabular-nums">
                                                            {index + 1}
                                                        </TableCell>
                                                        <TableCell className="font-semibold w-2/5 min-w-0 break-words whitespace-normal align-middle">
                                                            {getHolidayName(holiday)}
                                                        </TableCell>
                                                        <TableCell className="w-1/4 align-middle">{formatHolidayDate(holiday.date)}</TableCell>
                                                        <TableCell className="w-1/5 align-middle">
                                                            <Badge variant="outline" className={getTypeColor(holiday.type)}>
                                                                {t(`holidays.type.${holiday.type.toLowerCase()}`)}
                                                            </Badge>
                                                        </TableCell>
                                                        <TableCell className="w-20 text-right align-middle">
                                                            <DropdownMenu>
                                                                <DropdownMenuTrigger asChild>
                                                                    <Button variant="ghost" size="icon" className="h-8 w-8">
                                                                        <MoreHorizontal className="h-4 w-4" />
                                                                    </Button>
                                                                </DropdownMenuTrigger>
                                                                <DropdownMenuContent align="end" className="w-32">
                                                                    <DropdownMenuLabel>{t("common.actions")}</DropdownMenuLabel>
                                                                    <DropdownMenuItem onClick={() => handleEditClick(holiday)}>
                                                                        <Pencil className="mr-2 h-3.5 w-3.5" /> {t("holidays.editHoliday")}
                                                                    </DropdownMenuItem>
                                                                    <DropdownMenuItem 
                                                                        className="text-red-600 focus:text-red-600"
                                                                        onClick={() => holiday.id && handleDelete(holiday.id)}
                                                                    >
                                                                        <Trash2 className="mr-2 h-3.5 w-3.5" /> {t("holidays.deleteHoliday")}
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
                        <Card className="p-4 bg-card border-border shadow-md rounded-xl">
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
                                    eventClassNames="cursor-pointer hover:opacity-80 transition-opacity font-medium text-xs px-1 shadow-sm border-l-4"
                                    dayMaxEvents={10} // High number to avoid "+n more" link
                                    fixedWeekCount={false}
                                    showNonCurrentDates={view === "month" ? false : true}
                                    dayCellClassNames={(arg) => {
                                        const dateStr = format(arg.date, "yyyy-MM-dd");
                                        const classes = ["transition-all duration-200"];
                                        if (holidayDates.has(dateStr)) {
                                            classes.push("holiday-cell");
                                        }
                                        const day = arg.date.getDay();
                                        if (day === 0) classes.push("bg-red-500/5", "weekend-sun");
                                        if (day === 6) classes.push("bg-blue-500/5", "weekend-sat");
                                        return classes;
                                    }}
                                    dayCellContent={(arg) => {
                                        const dateStr = format(arg.date, "yyyy-MM-dd");
                                        const holiday = holidays.find(h => format(h.date, "yyyy-MM-dd") === dateStr);
                                        
                                        const content = (
                                            <div className="flex items-center gap-1.5 px-2 py-1 cursor-default group">
                                                <span className={cn(
                                                    "text-sm font-medium transition-colors",
                                                    holiday ? "text-red-600 dark:text-red-400 font-bold" : "text-foreground"
                                                )}>
                                                    {arg.dayNumberText}
                                                </span>
                                                {holiday && (
                                                    <div className="w-1.5 h-1.5 bg-red-600 rounded-full animate-pulse shadow-[0_0_4px_rgba(220,38,38,0.4)]" />
                                                )}
                                            </div>
                                        );

                                        // Only show Tooltip in Year view where names aren't visible
                                        if (view === "year" && holiday) {
                                            return (
                                                <Tooltip delayDuration={100}>
                                                    <TooltipTrigger asChild>
                                                        {content}
                                                    </TooltipTrigger>
                                                    <TooltipContent side="top" className="p-0 overflow-hidden border-none shadow-2xl">
                                                        <div className="w-64 bg-card text-card-foreground">
                                                            <div className="bg-red-600 px-3 py-1.5">
                                                                <p className="text-white text-xs font-bold uppercase tracking-wider">
                                                                    {t("holidays.table.type")}: {t(`holidays.type.${holiday.type.toLowerCase()}`)}
                                                                </p>
                                                            </div>
                                                            <div className="p-3 space-y-2">
                                                                <p className="font-bold text-base leading-tight">
                                                                    {language === "th" ? (holiday.holidayNameTH || holiday.name) : (holiday.holidayNameEN || holiday.name)}
                                                                </p>
                                                                <Badge variant="secondary" className="bg-red-100 text-red-700 hover:bg-red-100 border-none">
                                                                    {t(`holidays.type.${holiday.type.toLowerCase()}`)}
                                                                </Badge>
                                                                {holiday.description && (
                                                                    <p className="text-xs text-muted-foreground italic leading-relaxed pt-1 border-t border-border">
                                                                        {holiday.description}
                                                                    </p>
                                                                )}
                                                            </div>
                                                        </div>
                                                    </TooltipContent>
                                                </Tooltip>
                                            );
                                        }

                                        return content;
                                    }}
                                    eventDidMount={(arg) => {
                                        arg.el.setAttribute('title', arg.event.title);
                                    }}
                                />
                            </div>
                            <style jsx global>{`
                                .fc {
                                    --fc-border-color: var(--border);
                                    --fc-daygrid-dot-event-hover-bg-color: var(--accent);
                                    --fc-page-bg-color: transparent;
                                    --fc-neutral-bg-color: transparent;
                                    --fc-list-event-hover-bg-color: var(--accent);
                                    --fc-today-bg-color: color-mix(in srgb, var(--primary) 15%, transparent);
                                    color: var(--foreground);
                                }
                                
                                /* Today Styling */
                                .fc .fc-day-today {
                                    background-color: #e0f2fe !important; /* sky-100 */
                                    position: relative;
                                }
                                .fc .fc-day-today::after {
                                    content: '';
                                    position: absolute;
                                    top: 0;
                                    left: 0;
                                    right: 0;
                                    bottom: 0;
                                    border: 2px solid #38bdf8; /* sky-400 */
                                    pointer-events: none;
                                    z-index: 3;
                                }
                                .fc .fc-day-today .fc-daygrid-day-number {
                                    background-color: #38bdf8; /* sky-400 */
                                    color: white !important;
                                    border-radius: 4px;
                                    padding: 0 4px;
                                    margin: 2px;
                                    z-index: 4;
                                    position: relative;
                                }

                                /* Hide "+n more" link */
                                .fc-daygrid-more-link {
                                    display: none !important;
                                }

                                /* Weekend Styling */
                                .fc .weekend-sun {
                                    background-color: rgba(239, 68, 68, 0.04) !important;
                                }
                                .fc .weekend-sat {
                                    background-color: rgba(59, 130, 246, 0.04) !important;
                                }
                                .fc .fc-col-header-cell.fc-day-sun {
                                    color: #ef4444;
                                }
                                .fc .fc-col-header-cell.fc-day-sat {
                                    color: #3b82f6;
                                }

                                /* Holiday Cell Background */
                                .holiday-cell {
                                    background-color: rgba(239, 68, 68, 0.08) !important;
                                }

                                /* Year View Holiday Styling */
                                .fc-multimonth-year-view .holiday-cell {
                                    background-color: #ef4444 !important; /* red-500 fill */
                                    position: relative;
                                }
                                .fc-multimonth-year-view .holiday-cell .fc-daygrid-day-number {
                                    color: white !important;
                                    font-weight: bold;
                                    position: relative;
                                    z-index: 2;
                                }
                                /* Hide event content in Year view to avoid clutter, using cell background instead */
                                .fc-multimonth-year-view .fc-daygrid-event {
                                    position: absolute;
                                    top: 0;
                                    left: 0;
                                    right: 0;
                                    bottom: 0;
                                    opacity: 0; /* Hidden but interactive for hover */
                                    z-index: 1;
                                }

                                /* Hover State: Only for Year view */
                                .fc-multimonth-year-view .fc-daygrid-day:hover {
                                    filter: brightness(1.1);
                                    z-index: 5;
                                    cursor: pointer;
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
                                    background: var(--card);
                                }
                                .fc-multimonth-title {
                                    background: var(--muted);
                                    padding: 0.5rem !important;
                                    border-bottom: 1px solid var(--border);
                                    color: var(--muted-foreground);
                                    font-weight: 700;
                                }
                                .fc-day-other {
                                    opacity: 0.3;
                                    background-color: transparent !important;
                                }
                                .fc-event-title {
                                    white-space: normal !important;
                                    word-wrap: break-word;
                                    padding: 2px 4px;
                                }
                                .fc-daygrid-event {
                                    border-radius: 4px;
                                    margin-bottom: 2px;
                                    border-top: none !important;
                                    border-right: none !important;
                                    border-bottom: none !important;
                                }
                                
                                /* Highlight day cell if it has a holiday in month view */
                                .fc-daygrid-month-view .holiday-cell {
                                    background-color: rgba(239, 68, 68, 0.08) !important;
                                }

                                /* Hide time element if any */
                                .fc-event-time {
                                    display: none !important;
                                }
                            `}</style>
                        </Card>
                    )}
                </div>
            </div>
        </TooltipProvider>
    );
}
