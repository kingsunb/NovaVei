#!/usr/bin/env node

import { readFileSync, writeFileSync } from 'node:fs';

const [inputPath, outputPath] = process.argv.slice(2);
if (!inputPath || !outputPath) {
  console.error('usage: normalize-frontend-report.mjs INPUT OUTPUT');
  process.exit(2);
}

const raw = JSON.parse(readFileSync(inputPath, 'utf8'));
if (!raw || Array.isArray(raw) || typeof raw !== 'object') {
  throw new Error(`${inputPath}: expected pnpm license groups object`);
}

const packages = Object.entries(raw)
  .flatMap(([license, entries]) =>
    (Array.isArray(entries) ? entries : []).map((entry) => ({
      name: entry.name,
      versions: Array.isArray(entry.versions) ? entry.versions : [],
      license: entry.license || license,
      author: entry.author || null,
      homepage: entry.homepage || null,
      repository: entry.repository || null,
    })),
  )
  .sort((a, b) => a.name.localeCompare(b.name) || a.license.localeCompare(b.license));

if (packages.length < 1) {
  throw new Error(`${inputPath}: no production dependency licenses found`);
}

const report = {
  report_format: 'pnpm-production-license-report-v1',
  generated_from: 'web-next/pnpm-lock.yaml',
  packages,
};
writeFileSync(outputPath, `${JSON.stringify(report, null, 2)}\n`);
