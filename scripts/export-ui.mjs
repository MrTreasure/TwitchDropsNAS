// Export only the reusable component and CSS; no host code or configuration.
import { mkdir, copyFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
const destination = process.argv[2];
if (!destination) throw new Error('Usage: node scripts/export-ui.mjs <destination>');
const source = resolve(dirname(fileURLToPath(import.meta.url)), '../ui/src');
await mkdir(destination, { recursive: true });
await copyFile(resolve(source, 'TwitchDrops.tsx'), resolve(destination, 'TwitchDropsView.tsx'));
await copyFile(resolve(source, 'twitch-drops.css'), resolve(destination, 'twitch-drops.css'));
await copyFile(resolve(source, 'war-thunder-style.ts'), resolve(destination, 'war-thunder-style.ts'));
console.log('Exported reusable component and CSS; host adapter unchanged.');
