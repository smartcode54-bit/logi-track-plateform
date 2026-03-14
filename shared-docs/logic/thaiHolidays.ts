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
        { holidayNameEN: "Makha Bucha Day", holidayNameTH: "วันมาฆบูชา", date: makhaBucha },
        { holidayNameEN: "Visakha Bucha Day", holidayNameTH: "วันวิสาขบูชา", date: visakhaBucha },
        { holidayNameEN: "Asalha Bucha Day", holidayNameTH: "วันอาสาฬหบูชา", date: asalhaBucha },
        { holidayNameEN: "Buddhist Lent Day", holidayNameTH: "วันเข้าพรรษา", date: khaoPhansa },
    ];
}

/**
 * Generates Thai Public Holidays for a given year.
 * Includes fixed date holidays and calculated Buddhist holidays.
 */
export function generateThaiPublicHolidays(year: number): Partial<Holiday>[] {
    const holidays: Partial<Holiday>[] = [];

    // Helper to add holiday with substitution logic
    const addHoliday = (holidayNameEN: string, holidayNameTH: string, date: Date, type: "PUBLIC" | "RELIGIOUS" = "PUBLIC", isRecurring: boolean = true) => {
        // Ensure date is at start of day in local time for consistent comparison
        const holidayDate = new Date(date.getFullYear(), date.getMonth(), date.getDate());
        
        holidays.push({
            name: `${holidayNameEN} (${holidayNameTH})`,
            holidayNameEN,
            holidayNameTH,
            date: holidayDate,
            type: type === "RELIGIOUS" ? "PUBLIC" : type, 
            status: "DRAFT",
            isRecurring,
        });

        // Substitution Logic
        if (holidayNameEN !== "Buddhist Lent Day") {
            const dayOfWeek = holidayDate.getDay(); // 0 = Sunday, 6 = Saturday
            if (dayOfWeek === 0 || dayOfWeek === 6) {
                const subDate = new Date(holidayDate);
                subDate.setDate(holidayDate.getDate() + (dayOfWeek === 0 ? 1 : 2));
                
                if (holidayNameEN === "Asalha Bucha Day" && dayOfWeek === 0) {
                    subDate.setDate(holidayDate.getDate() + 2);
                }

                holidays.push({
                    name: `Substitution for ${holidayNameEN} (วันหยุดชดเชย${holidayNameTH})`,
                    holidayNameEN: `Substitution for ${holidayNameEN}`,
                    holidayNameTH: `วันหยุดชดเชย${holidayNameTH}`,
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
        { holidayNameEN: "New Year's Day", holidayNameTH: "วันขึ้นปีใหม่", month: 0, day: 1 },
        { holidayNameEN: "Chakri Day", holidayNameTH: "วันจักรี", month: 3, day: 6 },
        { holidayNameEN: "Songkran Festival", holidayNameTH: "วันสงกรานต์", month: 3, day: 13 },
        { holidayNameEN: "Songkran Festival", holidayNameTH: "วันสงกรานต์", month: 3, day: 14 },
        { holidayNameEN: "Songkran Festival", holidayNameTH: "วันสงกรานต์", month: 3, day: 15 },
        { holidayNameEN: "Labor Day", holidayNameTH: "วันแรงงานแห่งชาติ", month: 4, day: 1 },
        { holidayNameEN: "Coronation Day", holidayNameTH: "วันฉัตรมงคล", month: 4, day: 4 },
        { holidayNameEN: "HM Queen Suthida's Birthday", holidayNameTH: "วันเฉลิมพระชนมพรรษาสมเด็จพระนางเจ้าฯ พระบรมราชินี", month: 5, day: 3 },
        { holidayNameEN: "King Rama X's Birthday", holidayNameTH: "วันเฉลิมพระชนมพรรษาพระบาทสมเด็จพระเจ้าอยู่หัว", month: 6, day: 28 },
        { holidayNameEN: "HM Queen Sirikit The Queen Mother's Birthday", holidayNameTH: "วันเฉลิมพระชนมพรรษาสมเด็จพระบรมราชชนนีพันปีหลวง / วันแม่แห่งชาติ", month: 7, day: 12 },
        { holidayNameEN: "King Rama IX Memorial Day", holidayNameTH: "วันคล้ายวันสวรรคตพระบาทสมเด็จพระบรมชนกาธิเบศร มหาภูมิพลอดุลยเดชมหาราช บรมนาถบพิตร", month: 9, day: 13 },
        { holidayNameEN: "Chulalongkorn Day", holidayNameTH: "วันปิยมหาราช", month: 9, day: 23 },
        { holidayNameEN: "King Rama IX's Birthday / Father's Day", holidayNameTH: "วันคล้ายวันพระบรมราชสมภพ พระบาทสมเด็จพระบรมชนกาธิเบศร มหาภูมิพลอดุลยเดชมหาราช บรมนาถบพิตร / วันพ่อแห่งชาติ", month: 11, day: 5 },
        { holidayNameEN: "Constitution Day", holidayNameTH: "วันรัฐธรรมนูญ", month: 11, day: 10 },
    ];

    fixedHolidays.forEach(h => {
        addHoliday(h.holidayNameEN, h.holidayNameTH, new Date(year, h.month, h.day));
    });

    // Variable Buddhist Holidays
    const buddhistHolidays = calculateThaiBuddhistHolidays(year);
    buddhistHolidays.forEach(bh => {
        addHoliday(bh.holidayNameEN as string, bh.holidayNameTH as string, bh.date as Date, "PUBLIC", false);
    });

    // Sort by date
    holidays.sort((a, b) => (a.date as Date).getTime() - (b.date as Date).getTime());

    return holidays;
}
