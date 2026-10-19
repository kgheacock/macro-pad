// A "here" is where a message came from, and where a press on its channel key
// goes: KIND:VALUE with one of three kinds. Any process can write the store,
// so parseHere runs when a value is stored and again when the daemon reads it,
// and focus runs a program with an argument list, never a shell.

import { execFile as nodeExecFile } from 'node:child_process';
import { UsageError } from './errors.js';

export const FOCUS_TIMEOUT_MS = 5000;
const MAX_HERE_LENGTH = 2048;

const UUID = /^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$/;
// Starts with a letter or digit, so `open -b` never reads it as an option.
const BUNDLE_ID = /^[A-Za-z0-9][A-Za-z0-9-]*(\.[A-Za-z0-9-]+)+$/;
const URL_SCHEMES = ['slack:', 'msteams:', 'ms-outlook:', 'https:'];
const URL_START = /^(slack|msteams|ms-outlook|https):/i;

const KINDS = 'iterm2:UUID, app:BUNDLE_ID, or url:URL';

// iTerm's session id is w0t0p0:UUID, and UUID is the session's `unique id`.
const ITERM_SESSION = /^w\d+t\d+p\d+:([0-9A-Fa-f-]{36})$/;

// The script gets the UUID as an argument, not as part of its text.
const ITERM_SCRIPT = [
  'on run argv',
  'set target to item 1 of argv',
  'tell application "iTerm"',
  'repeat with w in windows',
  'repeat with t in tabs of w',
  'repeat with s in sessions of t',
  'if unique id of s is target then',
  'select w',
  'select t',
  'select s',
  'activate',
  'return "focused"',
  'end if',
  'end repeat',
  'end repeat',
  'end repeat',
  'end tell',
  'error "no iTerm session " & target',
  'end run',
];

function validUrl(text) {
  if (!URL_START.test(text) || /[\s\u0000-\u001f\u007f]/.test(text)) return false;
  try {
    return URL_SCHEMES.includes(new URL(text).protocol);
  } catch {
    return false;
  }
}

// Returns { kind, value } for a good "here", else null.
export function parseHere(text) {
  if (typeof text !== 'string' || text.length > MAX_HERE_LENGTH) return null;
  const colon = text.indexOf(':');
  if (colon < 1) return null;
  const kind = text.slice(0, colon);
  const value = text.slice(colon + 1);
  if (kind === 'iterm2' && UUID.test(value)) return { kind, value };
  if (kind === 'app' && BUNDLE_ID.test(value)) return { kind, value };
  if (kind === 'url' && validUrl(value)) return { kind, value };
  return null;
}

// Returns the text, or throws a UsageError. `source` says where it came from.
export function validateHere(text, source = '--here') {
  if (parseHere(text) === null) {
    throw new UsageError(`bad ${source} "${text}": use ${KINDS}`, { help: source === '--here' });
  }
  return text;
}

// The "here" an iTerm session gives without help, or null. Inside tmux,
// $ITERM_SESSION_ID names the session that started tmux, not the pane.
export function detectHere(env = process.env) {
  if (env.TERM_PROGRAM !== 'iTerm.app' || env.TMUX) return null;
  const match = ITERM_SESSION.exec(env.ITERM_SESSION_ID ?? '');
  if (match === null) return null;
  const here = `iterm2:${match[1]}`;
  return parseHere(here) === null ? null : here;
}

// For send and pipe: --here, then $INBOX_HERE, then detection, else null.
export function resolveHere({ flag, env = process.env } = {}) {
  if (flag !== undefined) return validateHere(flag, '--here');
  if (env.INBOX_HERE) return validateHere(env.INBOX_HERE, '$INBOX_HERE');
  return detectHere(env);
}

function actionFor({ kind, value }) {
  if (kind === 'iterm2') {
    return { file: 'osascript', args: [...ITERM_SCRIPT.flatMap((line) => ['-e', line]), value] };
  }
  if (kind === 'app') return { file: 'open', args: ['-b', value] };
  return { file: 'open', args: [value] };
}

// Brings a "here" to the front. Resolves when the program exits 0; rejects
// with an Error that says why. execFile and platform are for a test.
export function focus(
  here,
  { execFile = nodeExecFile, platform = process.platform, timeoutMs = FOCUS_TIMEOUT_MS } = {},
) {
  const parsed = parseHere(here);
  if (parsed === null) return Promise.reject(new Error(`not a valid "here" (use ${KINDS})`));
  if (platform !== 'darwin') return Promise.reject(new Error('a "here" works on macOS only'));
  const { file, args } = actionFor(parsed);
  return new Promise((resolve, reject) => {
    execFile(file, args, { timeout: timeoutMs, shell: false }, (error, stdout, stderr) => {
      if (!error) {
        resolve();
        return;
      }
      if (error.killed || error.signal === 'SIGTERM') {
        const hint =
          file === 'osascript' ? '; allow it in System Settings, Privacy & Security, Automation' : '';
        reject(new Error(`${file} did not answer within ${timeoutMs / 1000} s${hint}`));
        return;
      }
      const detail = String(stderr ?? '').trim() || error.message;
      reject(new Error(detail));
    });
  });
}
