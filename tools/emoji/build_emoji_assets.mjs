// Draws assets/emoji/<codepoints>.png for each line of assets/emoji/set.txt
// (task 0073), and of assets/emoji/extra.txt, emoji a key names directly and
// no folder or session hashes to (the apps plugin's actions, task 0085), from
// Microsoft Fluent Emoji (flat), pinned in package.json.
//
//   cd tools/emoji && npm ci && npm run build
//
// Each emoji is drawn once at 512 px to find how far its solid pixels reach,
// then again at 128x128, scaled and centred so that the farthest pixel of alpha
// 0x80 or more lies RADIUS px from the centre: inside the white round of
// driver/layers/pip.go with room to spare. A run with no change to set.txt
// writes the same bytes.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import { Resvg } from '@resvg/resvg-js';

const require = createRequire(import.meta.url);
const here = path.dirname(fileURLToPath(import.meta.url));
const out = path.resolve(here, '../../assets/emoji');
const svgDir = path.join(path.dirname(require.resolve('@lobehub/fluent-emoji-flat/package.json')), 'assets');

const SIZE = 128;
const PROBE = 512;
const RADIUS = 50;
const SOLID = 0x80;

export function readSet(file) {
  return fs
    .readFileSync(file, 'utf8')
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith('#'))
    .map((line) => {
      const [codepoints, name] = line.split(/\s+/);
      return { codepoints, name };
    });
}

function svgFor(codepoints) {
  for (const name of [codepoints, `${codepoints}-fe0f`]) {
    const file = path.join(svgDir, `${name}.svg`);
    if (fs.existsSync(file)) return fs.readFileSync(file, 'utf8');
  }
  throw new Error(`no Fluent SVG for ${codepoints}`);
}

// The viewBox of the root <svg>, as [x, y, w, h].
function viewBox(svg) {
  const m = /<svg[^>]*\bviewBox="([^"]+)"/.exec(svg);
  if (!m) throw new Error('no viewBox');
  return m[1].trim().split(/[\s,]+/).map(Number);
}

// How far the solid pixels reach, in viewBox units: the centre of their box
// and the largest distance from it.
function reach(svg, [vx, vy, vw]) {
  const img = new Resvg(svg, { fitTo: { mode: 'width', value: PROBE } }).render();
  const { width, height, pixels } = img;
  let x0 = width, y0 = height, x1 = -1, y1 = -1;
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++)
      if (pixels[(y * width + x) * 4 + 3] >= SOLID) {
        if (x < x0) x0 = x;
        if (x > x1) x1 = x;
        if (y < y0) y0 = y;
        if (y > y1) y1 = y;
      }
  const cx = (x0 + x1 + 1) / 2, cy = (y0 + y1 + 1) / 2;
  let far = 0;
  for (let y = y0; y <= y1; y++)
    for (let x = x0; x <= x1; x++)
      if (pixels[(y * width + x) * 4 + 3] >= SOLID) far = Math.max(far, Math.hypot(x + 0.5 - cx, y + 0.5 - cy));
  const unit = vw / width;
  return { cx: vx + cx * unit, cy: vy + cy * unit, far: far * unit };
}

export function draw(codepoints) {
  const svg = svgFor(codepoints);
  const box = viewBox(svg);
  const { cx, cy, far } = reach(svg, box);
  // A square of the source that maps `far` to RADIUS px, centred on the emoji.
  const side = (far * SIZE) / RADIUS;
  const framed = svg.replace(/<svg\b[^>]*>/, (open) =>
    open
      .replace(/\b(width|height|viewBox)="[^"]*"/g, '')
      .replace('<svg', `<svg width="${SIZE}" height="${SIZE}" viewBox="${cx - side / 2} ${cy - side / 2} ${side} ${side}"`),
  );
  return new Resvg(framed, { fitTo: { mode: 'original' } }).render().asPng();
}

function main() {
  const set = [...readSet(path.join(out, 'set.txt')), ...readSet(path.join(out, 'extra.txt'))];
  const seen = new Set();
  for (const { codepoints, name } of set) {
    if (seen.has(codepoints)) throw new Error(`${codepoints} (${name}) is listed twice`);
    seen.add(codepoints);
    fs.writeFileSync(path.join(out, `${codepoints}.png`), draw(codepoints));
  }
  // A PNG whose line was removed goes too.
  for (const file of fs.readdirSync(out)) {
    if (file.endsWith('.png') && !seen.has(file.slice(0, -4))) fs.rmSync(path.join(out, file));
  }
  console.log(`${set.length} emoji in ${path.relative(process.cwd(), out)}`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) main();
