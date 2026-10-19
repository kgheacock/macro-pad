import { realpathSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { UsageError } from './errors.js';

const SEGMENT = /^[A-Za-z0-9_-]+$/;
// A directory name may hold dots between valid segments, as in github.com.
// The dot is kept, so ~/source/github.com/me/app gives github.com.me.app.
const DIRECTORY = /^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$/;

export function tildify(p, home = os.homedir()) {
  if (p === home) return '~';
  return p.startsWith(home + path.sep) ? `~${p.slice(home.length)}` : p;
}

export function sourceRoot(env = process.env) {
  return path.resolve(env.INBOX_SOURCE_ROOT || path.join(os.homedir(), 'source'));
}

// Returns the channel, or throws a UsageError that names the problem.
export function validateChannel(channel) {
  if (channel === '') throw new UsageError('bad channel "": a channel may not be empty');
  for (const segment of channel.split('.')) {
    if (segment === '') {
      throw new UsageError(`bad channel "${channel}": a channel segment may not be empty`);
    }
    if (!SEGMENT.test(segment)) {
      throw new UsageError(
        `bad channel "${channel}": a channel segment may hold only letters, digits, - and _`,
      );
    }
  }
  return channel;
}

function resolveLinks(p) {
  try {
    return realpathSync(p);
  } catch {
    return path.resolve(p);
  }
}

// The channel for a working directory under the source root, or null when
// the directory is the root or is not below it. Throws a UsageError when a
// path component cannot be a channel segment.
export function inferChannel(cwd, root) {
  const base = resolveLinks(root);
  const dir = resolveLinks(cwd);
  const rel = path.relative(base, dir);
  if (rel === '' || rel === '..' || rel.startsWith(`..${path.sep}`) || path.isAbsolute(rel)) {
    return null;
  }
  const parts = rel.split(path.sep);
  for (const part of parts) {
    if (!DIRECTORY.test(part)) {
      throw new UsageError(
        `cannot use directory "${part}" in ${tildify(dir)} as a channel segment; pass the channel first`,
      );
    }
  }
  return parts.join('.');
}
