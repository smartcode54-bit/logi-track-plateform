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

console.log('🔍 Scanning files for "/admin" routes...');
let allFiles = [];
dirsToScan.forEach(dir => {
    allFiles = allFiles.concat(walk(path.join(baseDir, dir)));
});

let changedFiles = 0;
allFiles.forEach(file => {
    try {
        const content = fs.readFileSync(file, 'utf8');
        
        // Replace "/admin/..." and `/admin/...` with "/app/..."
        let newContent = content.replace(/(["'`])\/admin(\/|["'`])/g, '$1/app$2');
        
        // Special case for breadcrumb logic in layout.tsx
        if (file.endsWith('layout.tsx') && newContent.includes("seg !== 'admin'")) {
            newContent = newContent.replace(/seg !== 'admin'/g, "seg !== 'app'");
        }

        if (content !== newContent) {
            fs.writeFileSync(file, newContent, 'utf8');
            changedFiles++;
            console.log('✅ Updated paths in: ' + path.relative(baseDir, file));
        }
    } catch (e) {
        console.error('❌ Error reading/writing ' + file, e);
    }
});
console.log(`\n✨ Successfully updated ${changedFiles} files!`);

console.log('\n📁 Renaming app/admin directory...');
const oldAdminPath = path.join(baseDir, 'app', 'admin');
const newAppPath = path.join(baseDir, 'app', 'app');

if (fs.existsSync(oldAdminPath)) {
    try {
        fs.renameSync(oldAdminPath, newAppPath);
        console.log('✅ Successfully renamed directory app/admin to app/app!');
        console.log('\n🎉 Refactoring complete! Please restart your dev server (pnpm run dev).');
    } catch (e) {
        console.error('❌ Error renaming directory app/admin to app/app:', e);
        console.log('👉 Please rename it manually in your editor: rename "app/admin" to "app/app".');
    }
} else {
    console.log('⚠️ Directory app/admin not found (might be already renamed).');
}
