// A profile says how one command line (inbox, ai-inbox) differs from the
// others: its name, its store, the statuses it accepts, and the renderer its
// daemon uses. cli.js and daemon.js take a profile; bin/*.js pick one.

import os from 'node:os';
import path from 'node:path';
import { AI_STATUSES, createAiRenderer } from './ai-render.js';
import { createRenderer } from './render.js';
import { DEFAULT_STATUS, defaultDbPath } from './store.js';

export const INBOX_PROFILE = {
  name: 'inbox',
  dbEnv: 'INBOX_DB',
  dbPath: defaultDbPath,
  statuses: null,
  defaultStatus: DEFAULT_STATUS,
  createRenderer,
};

export const AI_PROFILE = {
  name: 'ai-inbox',
  dbEnv: 'AI_INBOX_DB',
  dbPath(env = process.env) {
    if (env.AI_INBOX_DB) return env.AI_INBOX_DB;
    const data = env.XDG_DATA_HOME || path.join(os.homedir(), '.local', 'share');
    return path.join(data, 'ai-inbox', 'ai-inbox.db');
  },
  statuses: AI_STATUSES,
  defaultStatus: 'done',
  createRenderer: createAiRenderer,
};

export const PROFILES = { inbox: INBOX_PROFILE, 'ai-inbox': AI_PROFILE };
