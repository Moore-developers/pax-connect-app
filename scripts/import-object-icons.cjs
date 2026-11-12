/* Rebuild the curated offline PNGs. Requires Node 20+ and sharp on NODE_PATH.
   Sources are pinned and checksummed in catalog.json; no user history is read. */
const fs = require('node:fs/promises');
const path = require('node:path');
const crypto = require('node:crypto');
const sharp = require('sharp');
const root = path.resolve(__dirname, '../macos/Packages/Features/Sources/Features/Resources/ObjectIcons');
(async () => {
  const catalog = JSON.parse(await fs.readFile(path.join(root, 'catalog.json'), 'utf8'));
  for (const [name, asset] of Object.entries(catalog.assets)) {
    const response = await fetch(asset.sourceURL, {signal: AbortSignal.timeout(15000)});
    if (!response.ok) throw new Error(`${name}: HTTP ${response.status}`);
    const source = Buffer.from(await response.arrayBuffer());
    if (source.length > 200000 || crypto.createHash('sha256').update(source).digest('hex') !== asset.sourceSHA256)
      throw new Error(`${name}: invalid source checksum/size`);
    const svg = source.toString('utf8').replace('<svg ', `<svg fill="#${asset.hex}" `);
    const png = await sharp(Buffer.from(svg), {density: 192}).resize(64,64,{fit:'contain',background:'#ffffff00'}).png().toBuffer();
    await fs.writeFile(path.join(root, asset.file), png);
    console.log(`${name}: ${png.length} bytes`);
  }
})().catch(error => { console.error(error.message); process.exitCode=1; });
