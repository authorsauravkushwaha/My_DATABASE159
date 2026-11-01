import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';

const root = process.cwd();
const read = (file) => JSON.parse(fs.readFileSync(path.join(root, file), 'utf8'));
const manifest = read('package.json');
const lock = read('package-lock.json');
assert.equal(lock.name, manifest.name);
assert.equal(lock.version, manifest.version);
assert.ok(manifest.engines.node.includes('22'));
for (const [name, version] of Object.entries(manifest.devDependencies)) assert.match(version, /^\d+\.\d+\.\d+$/, `root: pin ${name} exactly`);
for (const workspace of manifest.workspaces) {
  const pkg = read(path.join(workspace, 'package.json'));
  assert.ok(pkg.scripts.test && pkg.scripts.typecheck, `${workspace}: required scripts`);
  assert.equal(pkg.version, manifest.version, `${workspace}: version drift`);
  assert.equal(lock.packages[workspace].version, pkg.version, `${workspace}: lockfile drift`);
  for (const [name, version] of Object.entries({ ...pkg.dependencies, ...pkg.devDependencies })) {
    if (name.startsWith('@kestrel/')) continue;
    assert.match(version, /^\d+\.\d+\.\d+$/, `${workspace}: pin ${name} exactly`);
  }
}
for (const file of ['README.md', 'SECURITY.md', 'alembic.ini', 'apps/dashboard/index.html', 'apps/dashboard/src/content/getting-started.mdx']) {
  assert.ok(fs.existsSync(path.join(root, file)), `Missing ${file}`);
}
assert.ok(fs.readFileSync('.gitignore', 'utf8').includes('node_modules/'));
console.log('Workspace manifests, exact dependency pins, lockfile, and required files are consistent.');
