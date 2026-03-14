import { Holiday } from "../schemas/holidaySchema";

/**
 * Calculates major Thai Buddhist holidays for a given Gregorian year.
 * Based on the Thai Lunar Calendar (Patitin Chantarakati).
 */
function calculateThaiBuddhistHolidays(year: number) {
    // 1. Leap Year Logic (Thai Lunar Calendar)
    const isLeapMonth = (y: number) => {
        const cs = y - 638; // Chulasakarat Era
        return (cs * 7 + 13) % 19 < 7;
    };

    // Leap Day years (Athikawan) for 2000-2050
    const leapDayYears = [
        2001, 2004, 2007, 2012, 2017, 2020, 2025, 2030, 2035, 2038, 2043, 2046, 2049
    ];
    const isLeapDay = (y: number) => leapDayYears.includes(y);

    const getYearDays = (y: number) => {
        if (isLeapMonth(y)) return 384;
        if (isLeapDay(y)) return 355;
        return 354;
    };

    // 2. Reference Anchor: Visakha Bucha 2024 (May 22, 2024)
    // 15th day of the waxing moon in the 6th lunar month.
    let anchorDate = new Date(Date.UTC(2024, 4, 22)); 
    let currentYear = 2024;

    // 3. Adjust Anchor to Target Year
    if (year > currentYear) {
        for (let y = currentYear; y < year; y++) {
            anchorDate.setUTCDate(anchorDate.getUTCDate() + getYearDays(y));
        }
    } else if (year < currentYear) {
        for (let y = currentYear - 1; y >= year; y--) {
            anchorDate.setUTCDate(anchorDate.getUTCDate() - getYearDays(y));
        }
    }

    // anchorDate is now the Visakha Bucha date for the target 'year'
    const visakhaBucha = new Date(anchorDate);

    const isLM = isLeapMonth(year);
    const isLD = isLeapDay(year);
    
    const makhaBucha = new Date(visakhaBucha);
    makhaBucha.setUTCDate(makhaBucha.getUTCDate() - (isLM ? 89 : 88));

    const asalhaBucha = new Date(visakhaBucha);
    asalhaBucha.setUTCDate(asalhaBucha.getUTCDate() + (isLD ? 60 : 59));

    const khaoPhansa = new Date(asalhaBucha);
    khaoPhansa.setUTCDate(khaoPhansa.getUTCDate() + 1);

    return [
        { name: "Makha Bucha Day (วันมาฆบูชา)", date: makhaBucha },
        { name: "Visakha Bucha Day (วันวิสาขบูชา)", date: visakhaBucha },
        { name: "Asalha Bucha Day (วันอาสาฬหบูชา)", date: asalhaBucha },
        { name: "Buddhist Lent Day (วันเข้าพรรษา)", date: khaoPhansa },
    ];
}

/**
 * Generates Thai Public Holidays for a given year.
 * Includes fixed date holidays and calculated Buddhist holidays.
 */
export function generateThaiPublicHolidays(year: number): Partial<Holiday>[] {
    const holidays: Partial<Holiday>[] = [];

    // Helper to add holiday with substitution logic
    const addHoliday = (name: string, date: Date, type: "PUBLIC" | "RELIGIOUS" = "PUBLIC", isRecurring: boolean = true) => {
        // Ensure date is at start of day in local time for consistent comparison
        const holidayDate = new Date(date.getFullYear(), date.getMonth(), date.getDate());
        
        holidays.push({
            name,
            date: holidayDate,
            type: type === "RELIGIOUS" ? "PUBLIC" : type, // Keep schema compatibility, or map as needed
            status: "DRAFT",
            isRecurring,
        });

        // Substitution Logic: If falls on Sat/Sun, add substitution on Monday
        // Note: Buddhist Lent (Khao Phansa) is a holiday but usually doesn't have a substitution 
        // if it falls on a weekend because Asalha Bucha (the day before) already has one.
        // However, standard Thai practice is to have substitution for major public holidays.
        if (name !== "Buddhist Lent Day (วันเข้าพรรษา)") {
            const dayOfWeek = holidayDate.getDay(); // 0 = Sunday, 6 = Saturday
            if (dayOfWeek === 0 || dayOfWeek === 6) {
                const subDate = new Date(holidayDate);
                subDate.setDate(holidayDate.getDate() + (dayOfWeek === 0 ? 1 : 2));
                
                // Special case: If Asalha Bucha is Sunday, Substitution is Tuesday (since Mon is Buddhist Lent)
                if (name.includes("Asalha Bucha") && dayOfWeek === 0) {
                    subDate.setDate(holidayDate.getDate() + 2);
                }
                // Special case: If Asalha Bucha is Saturday, Substitution is Monday
                // If Asalha Bucha is Saturday (0), Buddhist Lent is Sunday (1). 
                // Many times the government adds an extra day on Tuesday.
                // For simplicity, we'll follow standard +1/+2 rule unless it overlaps.

                holidays.push({
                    name: `Substitution for ${name.split(' (')[0]} (วันหยุดชดเชย)`,
                    date: subDate,
                    type: "PUBLIC",
                    status: "DRAFT",
                    isRecurring: false,
                });
            }
        }
    };

    // Fixed Date Holidays
    const fixedHolidays = [
        { name: "New Year's Day (วันขึ้นปีใหม่)", month: 0, day: 1 },
        { name: "Chakri Day (วันจักรี)", month: 3, day: 6 },
        { name: "Songkran Festival (วันสงกรานต์)", month: 3, day: 13 },
        { name: "Songkran Festival (วันสงกรานต์)", month: 3, day: 14 },
        { name: "Songkran Festival (วันสงกรานต์)", month: 3, day: 15 },
        { name: "Labor Day (วันแรงงานแห่งชาติ)", month: 4, day: 1 },
        { name: "Coronation Day (วันฉัตรมงคล)", month: 4, day: 4 },
        { name: "HM Queen Suthida's Birthday (วันเฉลิมพระชนมพรรษาสมเด็จพระนางเจ้าฯ พระบรมราชินี)", month: 5, day: 3 },
        { name: "King Rama X's Birthday (วันเฉลิมพระชนมพรรษาพระบาทสมเด็จพระเจ้าอยู่หัว)", month: 6, day: 28 },
        { name: "HM Queen Sirikit The Queen Mother's Birthday (วันเฉลิมพระชนมพรรษาสมเด็จพระบรมราชชนนีพันปีหลวง / วันแม่แห่งชาติ)", month: 7, day: 12 },
        { name: "King Rama IX Memorial Day (วันคล้ายวันสวรรคตพระบาทสมเด็จพระบรมชนกาธิเบศร มหาภูมิพลอดุลยเดชมหาราช บรมนาถบพิตร)", month: 9, day: 13 },
        { name: "Chulalongkorn Day (วันปิยมหาราช)", month: 9, day: 23 },
        { name: "King Rama IX's Birthday / Father's Day (วันคล้ายวันพระบรมราชสมภพ พระบาทสมเด็จพระบรมชนกาธิเบศร มหาภูมิพลอดุลยเดชมหาราช บรมนาถบพิตร / วันพ่อแห่งชาติ)", month: 11, day: 5 },
        { name: "Constitution Day (วันรัฐธรรมนูญ)", month: 11, day: 10 },
    ];

    fixedHolidays.forEach(h => {
        addHoliday(h.name, new Date(year, h.month, h.day));
    });

    // Variable Buddhist Holidays
    const buddhistHolidays = calculateThaiBuddhistHolidays(year);
    buddhistHolidays.forEach(bh => {
        addHoliday(bh.name, bh.date, "PUBLIC", false);
    });

    // Sort by date
    holidays.sort((a, b) => (a.date as Date).getTime() - (b.date as Date).getTime());

    return holidays;
}
