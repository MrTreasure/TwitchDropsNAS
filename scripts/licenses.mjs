// Collect license text from the build toolchain/modules without recording local paths.
import { createRequire } from 'node:module';
import { dirname } from 'node:path';
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';
const sections = ['# Third-party notices\n\nRedistribution notices for the Go executable. UI dependency license texts follow the Go notices; the upstream references are listed in NOTICE.md.\n'];
const goroot = execFileSync('go', ['env', 'GOROOT'], { encoding: 'utf8' }).trim();
sections.push('## Go standard library\n\n```text\n' + readFileSync(resolve(goroot, 'LICENSE'), 'utf8').trim() + '\n```\n');
const modules = execFileSync('go', ['list', '-deps', '-f', '{{if .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}', '.'], { encoding: 'utf8' }).trim().split('\n');
for (const row of [...new Set(modules)].filter(Boolean).sort()) {
 const [name, version, directory] = row.split('|');
 if (!version) continue;
 const files = ['LICENSE', 'LICENSE.txt', 'LICENSE.md', 'COPYING', 'NOTICE'].filter(file => existsSync(resolve(directory, file)));
 if (!files.length) throw new Error(`Missing license for ${name}`);
 for (const file of files) sections.push(`## ${name} ${version} — ${file}\n\n\`\`\`text\n${readFileSync(resolve(directory, file), 'utf8').trim()}\n\`\`\`\n`);
}
const seen = new Set();
function uiLicenses(packagePath) {
 const pkg = JSON.parse(readFileSync(packagePath, 'utf8'));
 const id = `${pkg.name}@${pkg.version}`;
 if (seen.has(id)) return;
 seen.add(id);
 if (pkg.name !== 'twitch-drops-nas-ui') {
  const directory = dirname(packagePath);
  const files = ['LICENSE', 'LICENSE.txt', 'LICENSE.md', 'license', 'license.md', 'COPYING', 'NOTICE'].filter(file => existsSync(resolve(directory, file)));
  if (!files.length) {
   const readme = ['README.md','readme.md'].map(file => resolve(directory,file)).find(existsSync);
   const content = readme ? readFileSync(readme,'utf8') : '';
   const match = /^#{1,3} licen[sc]e\b.*$/im.exec(content);
   if (match) sections.push(`## UI: ${id} — README license section\n\n${content.slice(match.index).trim()}\n`);
   else {
    const repository = typeof pkg.repository === 'object' ? pkg.repository.url : pkg.repository;
    sections.push(`## UI: ${id} — package metadata\n\nDeclared license: ${pkg.license ?? 'see upstream'}. Upstream: ${repository ?? pkg.homepage ?? pkg.name}. This npm package does not include a separate license text.\n`);
   }
  }
  for (const file of files) sections.push(`## UI: ${id} — ${file}\n\n\`\`\`text\n${readFileSync(resolve(directory, file), 'utf8').trim()}\n\`\`\`\n`);
 }
 const request = createRequire(packagePath);
 for (const dependency of Object.keys({ ...pkg.dependencies, ...pkg.optionalDependencies, ...pkg.peerDependencies }).sort()) {
  let entry;
  try { entry = request.resolve(`${dependency}/package.json`); }
  catch {
   try {
    let directory = dirname(request.resolve(dependency));
    while (dirname(directory) !== directory) {
     const candidate = resolve(directory, 'package.json');
     if (existsSync(candidate) && JSON.parse(readFileSync(candidate, 'utf8')).name === dependency) { entry = candidate; break; }
     directory = dirname(directory);
    }
   } catch { if (pkg.optionalDependencies?.[dependency] || pkg.peerDependenciesMeta?.[dependency]?.optional) continue; }
  }
  if (!entry) throw new Error(`Cannot locate UI dependency ${dependency}`);
  uiLicenses(entry);
 }
}
uiLicenses(resolve('ui/package.json'));
writeFileSync('THIRD_PARTY_NOTICES.md', sections.join('\n').split('\n').map(line => line.trimEnd()).join('\n'));
console.log('Collected Go executable notices without build-host paths.');
