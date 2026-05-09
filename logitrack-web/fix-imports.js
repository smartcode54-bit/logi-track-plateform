const fs = require('fs');
const path = require('path');

const baseDir = __dirname;
const dirsToScan = ['app', 'components', 'features', 'lib', 'context', 'tests'];

function walk(dir) {
    let results = [];
    if (!fs.existsSync(dir)) return results;
    const list = fs.readdirSync(dir);
    list.forEach(file => {
        const fullPath = path.join(dir, file);
        const stat = fs.statSync(fullPath);
        if (stat && stat.isDirectory()) {
            results = results.concat(walk(fullPath));
        } else {
            if (fullPath.endsWith('.ts') || fullPath.endsWith('.tsx')) {
                results.push(fullPath);
            }
        }
    });
    return results;
}

console.log('🔍 Scanning files for "@/app/admin" imports...');
let allFiles = [];
dirsToScan.forEach(dir => {
    allFiles = allFiles.concat(walk(path.join(baseDir, dir)));
});

let changedFiles = 0;
allFiles.forEach(file => {
    try {
        const content = fs.readFileSync(file, 'utf8');
        
        // Replace "@/app/admin/..." with "@/app/app/..."
        let newContent = content.replace(/@\/app\/admin\//g, '@/app/app/');
        
        // Also handle regexes in tests like /\/admin\//
        if (file.includes('tests')) {
            newContent = newContent.replace(/\\\/admin\\\//g, '\\/app\\/');
        }

        if (content !== newContent) {
            fs.writeFileSync(file, newContent, 'utf8');
            changedFiles++;
            console.log('✅ Fixed imports in: ' + path.relative(baseDir, file));
        }
    } catch (e) {
        console.error('❌ Error reading/writing ' + file, e);
    }
});
console.log(`\n✨ Successfully fixed imports in ${changedFiles} files!`);
