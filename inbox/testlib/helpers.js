import { spawn, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, realpathSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const BIN_DIR = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'bin');
export const BIN = path.join(BIN_DIR, 'inbox.js');
export const AI_BIN = path.join(BIN_DIR, 'ai-inbox.js');

// A scratch directory with a source root and a database path inside it.
export function scratch(t) {
  const dir = realpathSync(mkdtempSync(path.join(os.tmpdir(), 'inbox-test-')));
  t?.after(() => rmSync(dir, { recursive: true, force: true }));
  const root = path.join(dir, 'src');
  mkdirSync(root);
  return { dir, root, db: path.join(dir, 'data', 'inbox.db'), aiDb: path.join(dir, 'data', 'ai-inbox.db') };
}

export function envFor({ db, aiDb, root }, extra = {}) {
  return {
    PATH: process.env.PATH,
    HOME: process.env.HOME,
    INBOX_DB: db,
    INBOX_SOURCE_ROOT: root,
    AI_INBOX_DB: aiDb,
    ...extra,
  };
}

// Runs the CLI to the end. Returns { status, stdout, stderr }. `bin` picks
// the command: BIN (inbox, the default) or AI_BIN (ai-inbox).
export function run(args, { sc, cwd = sc.root, input, env = {}, bin = BIN } = {}) {
  const result = spawnSync(process.execPath, [bin, ...args], {
    cwd,
    env: envFor(sc, env),
    input,
    encoding: 'utf8',
  });
  return { status: result.status, stdout: result.stdout, stderr: result.stderr };
}

// Runs the ai-inbox command to the end.
export function runAi(args, options = {}) {
  return run(args, { ...options, bin: AI_BIN });
}

// Starts the CLI and returns the child plus a promise for its exit.
export function start(args, { sc, cwd = sc.root, env = {} } = {}) {
  const child = spawn(process.execPath, [BIN, ...args], {
    cwd,
    env: envFor(sc, env),
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  let stderr = '';
  child.stderr.setEncoding('utf8').on('data', (d) => (stderr += d));
  const exited = new Promise((resolve) =>
    child.on('close', (status) => resolve({ status, stderr })),
  );
  return { child, exited };
}
