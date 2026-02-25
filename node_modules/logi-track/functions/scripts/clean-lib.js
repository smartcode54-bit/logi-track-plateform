const fs = require("fs");
const path = require("path");
const libDir = path.join(__dirname, "..", "lib");
try {
  fs.rmSync(libDir, { recursive: true, force: true });
} catch (_) {}
process.exit(0);
