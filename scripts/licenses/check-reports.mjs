#!/usr/bin/env node

import { readFileSync } from 'node:fs';

const [goLicensePath, frontendLicensePath] = process.argv.slice(2);
if (!goLicensePath || !frontendLicensePath) {
  console.error('usage: check-reports.mjs GO_LICENSES FRONTEND_LICENSES');
  process.exit(2);
}

function readNonEmpty(path) {
  const value = readFileSync(path, 'utf8').trim();
  if (!value) throw new Error(`${path}: report is empty`);
  return value;
}

const goRows = readNonEmpty(goLicensePath)
  .split(/\r?\n/)
  .filter((line) => line.trim());
if (goRows[0] !== 'module,license_url,license_type') {
  throw new Error(`${goLicensePath}: unexpected CSV header`);
}
if (goRows.length < 2) {
  throw new Error(`${goLicensePath}: report contains only a header`);
}

function parseCSVRow(line) {
  const values = [];
  let value = '';
  let quoted = false;
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (quoted) {
      if (ch === '"' && line[i + 1] === '"') {
        value += '"';
        i++;
      } else if (ch === '"') {
        quoted = false;
      } else {
        value += ch;
      }
    } else if (ch === '"') {
      quoted = true;
    } else if (ch === ',') {
      values.push(value);
      value = '';
    } else {
      value += ch;
    }
  }
  if (quoted) throw new Error(`${goLicensePath}: unterminated quoted CSV row`);
  values.push(value);
  return values;
}

const allowedLicenses = new Set([
  'Apache-2.0', 'BSD-2-Clause', 'BSD-3-Clause', 'GPL-2.0', 'GPL-3.0',
  'ISC', 'LGPL-2.1', 'LGPL-3.0', 'MIT', 'MPL-2.0', 'Unlicense',
]);
const seenModules = new Set();
const seenRows = new Set();
for (const line of goRows.slice(1)) {
  const values = parseCSVRow(line);
  if (values.length !== 3) throw new Error(`${goLicensePath}: expected 3 CSV columns, got ${values.length}`);
  const [module, licenseUrl, licenseType] = values;
  if (!module) throw new Error(`${goLicensePath}: missing module`);
  const rowKey = values.join('\u0000');
  if (seenRows.has(rowKey)) throw new Error(`${goLicensePath}: duplicate license row for ${module}`);
  seenRows.add(rowKey);
  seenModules.add(module);
  let parsedLicenseUrl;
  try {
    parsedLicenseUrl = new URL(licenseUrl);
  } catch {
    throw new Error(`${goLicensePath}: invalid license URL for ${module}`);
  }
  if (parsedLicenseUrl.protocol !== 'https:' || !parsedLicenseUrl.host) {
    throw new Error(`${goLicensePath}: license URL must use HTTPS for ${module}`);
  }
  if (!allowedLicenses.has(licenseType)) throw new Error(`${goLicensePath}: weak or unsupported license type ${licenseType} for ${module}`);
}

const frontendLicense = JSON.parse(readNonEmpty(frontendLicensePath));
if (frontendLicense?.report_format !== 'pnpm-production-license-report-v1') {
  throw new Error(`${frontendLicensePath}: unexpected report format`);
}
if (!Array.isArray(frontendLicense.packages) || frontendLicense.packages.length < 1) {
  throw new Error(`${frontendLicensePath}: no production dependency license entries found`);
}
if (!frontendLicense.packages.some((entry) => entry?.name === 'react')) {
  throw new Error(`${frontendLicensePath}: expected production dependency "react" was not found`);
}

console.log(`license reports valid: Go rows=${goRows.length - 1}, frontend entries=${frontendLicense.packages.length}`);
