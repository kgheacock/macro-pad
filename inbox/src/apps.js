// What the keys of the apps plugin do (task 0085): find how an app runs,
// open VS Code, open or reuse an iTerm tab, and pull. Every program runs
// through execFile with an argument list, never a shell. A command typed
// into an iTerm tab quotes each word.

import { execFile as nodeExecFile } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const VSCODE = 'com.microsoft.VSCode';
export const OPEN_TIMEOUT_MS = 5000;
export const PULL_TIMEOUT_MS = 60 * 1000;
// The inbox command a Run tab types, when the daemon is not told another.
export const INBOX_BIN = fileURLToPath(new URL('../bin/inbox.js', import.meta.url));

// The Makefile targets that run an app, best first.
const MAKE_TARGETS = ['dev', 'run', 'start', 'serve'];
const MAKEFILES = ['GNUmakefile', 'makefile', 'Makefile'];
// The package.json scripts that run an app, best first.
const SCRIPTS = ['dev', 'start'];
// The lockfile that names a package manager, first match wins; else npm.
const LOCKFILES = [
  ['pnpm-lock.yaml', 'pnpm'],
  ['yarn.lock', 'yarn'],
  ['bun.lock', 'bun'],
  ['bun.lockb', 'bun'],
];

// INBOX_APPS_RUN: NAME=CMD;NAME=CMD -> Map NAME -> [words].
export function parseRunOverrides(text = '') {
  const map = new Map();
  for (const entry of text.split(';')) {
    const eq = entry.indexOf('=');
    if (eq < 1) continue;
    const words = entry.slice(eq + 1).trim().split(/\s+/).filter(Boolean);
    if (words.length) map.set(entry.slice(0, eq).trim(), words);
  }
  return map;
}

function readText(file) {
  try {
    return readFileSync(file, 'utf8');
  } catch {
    return null;
  }
}

// How an app runs: { argv, label }, or null. `override` is the words of
// its INBOX_APPS_RUN entry, if any.
export function detectRun(dir, override) {
  if (override?.length) return { argv: override, label: override.join(' ') };
  for (const name of MAKEFILES) {
    const text = readText(path.join(dir, name));
    if (text === null) continue;
    // A rule line, TARGET: or TARGET::, and not an assignment TARGET := x.
    const targets = new Set(
      [...text.matchAll(/^([A-Za-z0-9_.-]+)\s*::?(?!=)/gm)].map((m) => m[1]),
    );
    const target = MAKE_TARGETS.find((t) => targets.has(t));
    if (target) return { argv: ['make', target], label: `make ${target}` };
    break;
  }
  const pkg = readText(path.join(dir, 'package.json'));
  if (pkg !== null) {
    let scripts = {};
    try {
      scripts = JSON.parse(pkg)?.scripts ?? {};
    } catch {
      // Not JSON: no script.
    }
    const script = SCRIPTS.find((s) => typeof scripts[s] === 'string');
    if (script) {
      const pm = LOCKFILES.find(([file]) => existsSync(path.join(dir, file)))?.[1] ?? 'npm';
      return { argv: [pm, 'run', script], label: `${pm} ${script}` };
    }
  }
  return null;
}

// One word for a POSIX shell (and zsh, fish): in single quotes.
export function shellWord(word) {
  return /^[A-Za-z0-9_\/.,:=@%+-]+$/.test(word) ? word : `'${String(word).replace(/'/g, `'\\''`)}'`;
}

export function shellLine(words) {
  return words.map(shellWord).join(' ');
}

// The line a Run tab types: cd DIR && inbox --plugin apps [--db F] run NAME -- CMD.
export function runLine({ dir, name, argv, file, node = process.execPath, bin = INBOX_BIN }) {
  const inbox = [node, bin, '--plugin', 'apps', ...(file ? ['--db', file] : []), 'run', name, '--', ...argv];
  return `cd ${shellWord(dir)} && ${shellLine(inbox)}`;
}

// Opens a new iTerm tab (a window when none is open), types `line` into it,
// and returns the tab's session id. The line is an argument of the script.
const NEW_TAB_SCRIPT = [
  'on run argv',
  'set line to item 1 of argv',
  'tell application "iTerm"',
  'activate',
  'if (count of windows) is 0 then',
  'set w to (create window with default profile)',
  'else',
  'set w to current window',
  'tell w to create tab with default profile',
  'end if',
  'set s to current session of w',
  'tell s to write text line',
  'return unique id of s',
  'end tell',
  'end run',
];

// Brings the session with this id to the front and types `line` into it.
// Fails when no session has the id.
const TYPE_SCRIPT = [
  'on run argv',
  'set target to item 1 of argv',
  'set line to item 2 of argv',
  'tell application "iTerm"',
  'repeat with w in windows',
  'repeat with t in tabs of w',
  'repeat with s in sessions of t',
  'if unique id of s is target then',
  'select w',
  'select t',
  'select s',
  'activate',
  'tell s to write text line',
  'return "typed"',
  'end if',
  'end repeat',
  'end repeat',
  'end repeat',
  'end tell',
  'error "no iTerm session " & target',
  'end run',
];

const script = (lines) => lines.flatMap((line) => ['-e', line]);

function run(execFile, file, args, options) {
  return new Promise((resolve, reject) => {
    execFile(file, args, { shell: false, ...options }, (error, stdout, stderr) => {
      if (!error) {
        resolve(String(stdout));
        return;
      }
      if (error.killed || error.signal === 'SIGTERM') {
        reject(new Error(`${file} did not answer within ${options.timeout / 1000} s`));
        return;
      }
      const err = new Error(String(stderr ?? '').trim().split('\n')[0] || error.message);
      err.code = error.code;
      err.stdout = String(stdout ?? '');
      err.stderr = String(stderr ?? '');
      reject(err);
    });
  });
}

// The actions need a Mac; a test passes platform.
function needMac(platform) {
  if (platform !== 'darwin') throw new Error('this key works on macOS only');
}

export async function openCode(dir, { execFile = nodeExecFile, platform = process.platform } = {}) {
  needMac(platform);
  await run(execFile, 'open', ['-b', VSCODE, dir], { timeout: OPEN_TIMEOUT_MS });
}

// A new iTerm tab that types `line`. Resolves the tab's "here", iterm2:UUID.
export async function newTab(line, { execFile = nodeExecFile, platform = process.platform } = {}) {
  needMac(platform);
  const id = (await run(execFile, 'osascript', [...script(NEW_TAB_SCRIPT), line], { timeout: OPEN_TIMEOUT_MS })).trim();
  return `iterm2:${id}`;
}

// Types `line` into the open tab of `here`, at the front. Rejects when the
// tab is gone.
export async function typeInTab(here, line, { execFile = nodeExecFile, platform = process.platform } = {}) {
  needMac(platform);
  const id = here.replace(/^iterm2:/, '');
  await run(execFile, 'osascript', [...script(TYPE_SCRIPT), id, line], { timeout: OPEN_TIMEOUT_MS });
}

const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`;

// git pull --rebase in dir. Resolves { ok, body }: "up to date", "N commits",
// or why it failed. A rebase stopped by a conflict is aborted, so the
// repository is as it was.
export async function pull(dir, { env = process.env, execFile = nodeExecFile, timeoutMs = PULL_TIMEOUT_MS } = {}) {
  const git = env.INBOX_GIT || 'git';
  const options = { cwd: dir, timeout: timeoutMs, env: { ...env, GIT_TERMINAL_PROMPT: '0' } };
  const head = async () => (await run(execFile, git, ['rev-parse', 'HEAD'], options)).trim();
  let before;
  try {
    before = await head();
    await run(execFile, git, ['pull', '--rebase'], options);
  } catch (e) {
    if (e.code === 'ENOENT') return { ok: false, body: `no ${git}; install git or set INBOX_GIT` };
    const gitDir = path.join(dir, '.git');
    if (existsSync(path.join(gitDir, 'rebase-merge')) || existsSync(path.join(gitDir, 'rebase-apply'))) {
      try {
        await run(execFile, git, ['rebase', '--abort'], options);
        return { ok: false, body: 'conflict; rebase aborted' };
      } catch (abort) {
        return { ok: false, body: `conflict; rebase --abort failed: ${abort.message}` };
      }
    }
    const lines = `${e.stderr ?? ''}\n${e.stdout ?? ''}`.split('\n').map((l) => l.trim());
    return { ok: false, body: lines.find((l) => /^(error|fatal):/.test(l)) ?? lines.find(Boolean) ?? e.message };
  }
  // The upstream commits HEAD did not hold before; local commits rebased on
  // top do not count.
  try {
    const n = Number((await run(execFile, git, ['rev-list', '--count', `${before}..@{upstream}`], options)).trim());
    return { ok: true, body: n === 0 ? 'up to date' : plural(n, 'commit') };
  } catch {
    return { ok: true, body: 'pulled' };
  }
}
