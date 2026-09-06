const fs = require("fs");
const path = require("path");

const version = process.argv[2] || "1.0.0";
const match = /^(\d+)\.(\d+)\.(\d+)(?:[-+][0-9A-Za-z.-]+)?$/.exec(version);
if (!match) {
  console.error(`invalid semantic version: ${version}`);
  process.exit(1);
}
const numeric = match.slice(1, 4).map(Number);
if (numeric.some((part) => part > 65535)) {
  console.error(`version component exceeds Win32 limit: ${version}`);
  process.exit(1);
}
const destination = path.join(__dirname, "..", "build", "version.rcinc");
fs.writeFileSync(destination,
  `#define APP_FILE_VERSION ${numeric.join(",")},0\n` +
  `#define APP_VERSION_STRING "${version}"\n`, "ascii");
