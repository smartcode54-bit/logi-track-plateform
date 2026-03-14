import { generateThaiPublicHolidays } from "./thaiHolidays";

/**
 * Quick test script to verify holiday generation.
 * Run with: pnpm dlx tsx shared-docs/logic/test-holidays.ts
 */
function test() {
    const year = new Date().getFullYear();
    console.log(`--- Generating Holidays for Year: ${year} ---`);
    
    const holidays = generateThaiPublicHolidays(year);
    
    // Check total count
    console.log(`Total holidays generated: ${holidays.length}`);

    // Log the first few holidays
    console.log("\nFirst 5 Holidays:");
    holidays.slice(0, 5).forEach(h => {
        console.log(`- ${h.name} on ${h.date instanceof Date ? h.date.toDateString() : h.date}`);
    });

    // Check for substitutions (if any)
    const substitutions = holidays.filter(h => h.name?.includes("Substitution"));
    console.log(`\nSubstitutions found: ${substitutions.length}`);
    substitutions.forEach(s => {
        console.log(`- ${s.name} on ${s.date instanceof Date ? s.date.toDateString() : s.date}`);
    });

    console.log("\n--- Test Complete ---");
}

test();
