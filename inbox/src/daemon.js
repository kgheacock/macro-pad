// The inbox daemon: watch the store, keep macropadd's layer tree in step.
// startDaemon is the loop; acquirePidFile keeps one daemon per store.

import { mkdirSync, readFileSync, unlinkSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { DaemonRunningError, StoreError } from './errors.js';
import { decodeMessage, encodeSetLayerKey } from './protocol.js';
import { openStore } from './store.js';
import { createSummaryRenderer } from './summary.js';

export const DEFAULT_PORT = 8765;
export const DEFAULT_POLL_MS = 500;
const GUARD_MS = 30_000;
const LAYER_WAIT_MS = 1000;
const CLOSE_WAIT_MS = 300;
const BACKOFF_FIRST_MS = 250;
const BACKOFF_MAX_MS = 5000;

function isAlive(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch (e) {
    // EPERM: the process exists but belongs to someone else.
    return e.code === 'EPERM';
  }
}

function readPid(pidFile) {
  try {
    const pid = Number.parseInt(readFileSync(pidFile, 'utf8'), 10);
    return Number.isInteger(pid) && pid > 0 ? pid : null;
  } catch {
    return null;
  }
}

// Creates <db>.daemon.pid with this process's id. Throws DaemonRunningError
// when a live daemon holds it, and replaces a stale file. Returns a function
// that removes the file.
export function acquirePidFile(file, { log = () => {}, pid = process.pid, alive = isAlive } = {}) {
  const pidFile = `${file}.daemon.pid`;
  try {
    mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
  } catch (e) {
    throw new StoreError(`cannot create ${path.dirname(file)}: ${e.message}`);
  }
  for (let attempt = 0; attempt < 5; attempt++) {
    try {
      writeFileSync(pidFile, `${pid}\n`, { flag: 'wx', mode: 0o600 });
      return () => {
        if (readPid(pidFile) !== pid) return;
        try {
          unlinkSync(pidFile);
        } catch {
          // Already gone.
        }
      };
    } catch (e) {
      if (e.code !== 'EEXIST') throw new StoreError(`cannot write ${pidFile}: ${e.message}`);
    }
    const other = readPid(pidFile);
    if (other !== null && other !== pid && alive(other)) {
      throw new DaemonRunningError(`a daemon already runs for ${file} (pid ${other})`);
    }
    log(`replacing a stale pid file ${pidFile} (pid ${other ?? 'unreadable'} is gone)`);
    try {
      unlinkSync(pidFile);
    } catch (e) {
      if (e.code !== 'ENOENT') throw new StoreError(`cannot remove ${pidFile}: ${e.message}`);
    }
  }
  throw new StoreError(`cannot take ${pidFile}: another daemon keeps replacing it`);
}

// Starts the watch loop and the connection to macropadd. Returns
// { stop() }; stop() closes everything and resolves when it is done.
//
// store     an open Store (a test passes one to count queries); else the
//           daemon opens `file` and closes it on stop
// renderer  createRenderer({ slot, assetsDir }), see summary.js
// onEvent   (event, { active }) for each event from macropadd; task 0067's hook
export function startDaemon({
  file,
  env = process.env,
  port = DEFAULT_PORT,
  assetsDir,
  slot,
  pollMs = DEFAULT_POLL_MS,
  guardMs = GUARD_MS,
  layerWaitMs = LAYER_WAIT_MS,
  log = () => {},
  store,
  createRenderer = createSummaryRenderer,
  onEvent = () => {},
  WebSocketImpl = globalThis.WebSocket,
}) {
  const db = store ?? openStore(file, env);
  const renderer = createRenderer({ slot, assetsDir });
  const url = `ws://127.0.0.1:${port}/`;

  let version;
  let unseen;
  try {
    version = db.dataVersion();
    unseen = db.unseen();
  } catch (e) {
    if (!store) db.close();
    throw e;
  }
  let lastRead = Date.now();
  let readFailed = false;

  let socket = null;
  let ready = false;
  let active = null;
  let linkState = null;
  let delay = BACKOFF_FIRST_MS;
  let stopped = false;
  let layerTimer = null;
  let reconnectTimer = null;

  const setLink = (state, why) => {
    if (state === linkState) return;
    const wasUp = linkState === 'up';
    linkState = state;
    if (state === 'up') log(`connected to macropadd on 127.0.0.1:${port}`);
    else log(`cannot reach macropadd on 127.0.0.1:${port} (${wasUp ? 'connection closed' : why}); retrying with backoff up to ${BACKOFF_MAX_MS / 1000} s`);
  };

  // Sends what the renderer plans. Does nothing while disconnected: `unseen`
  // keeps the newest state, and the next connect sends all of it.
  const push = () => {
    if (!ready || !socket) return;
    for (const payload of renderer.plan({ unseen, active })) {
      try {
        socket.send(encodeSetLayerKey(payload));
      } catch (e) {
        log(`cannot send to macropadd: ${e.message}`);
        return;
      }
    }
  };

  const becomeReady = () => {
    if (ready || stopped || !socket) return;
    clearTimeout(layerTimer);
    renderer.reset();
    ready = true;
    push();
  };

  const onMessage = (data) => {
    if (typeof data !== 'string') return;
    const message = decodeMessage(data);
    if (message === null) {
      log('ignored a message from macropadd that is not JSON');
      return;
    }
    if (message.kind === 'layerChanged') {
      active = message.active;
      // The first layerChanged after a connect says the replay is done.
      if (!ready) becomeReady();
      else push();
    } else if (message.kind === 'event') {
      try {
        onEvent(message, { active });
      } catch (e) {
        log(`event handler failed: ${e.message}`);
      }
    }
  };

  const connect = () => {
    reconnectTimer = null;
    if (stopped) return;
    let sock;
    try {
      sock = new WebSocketImpl(url);
    } catch (e) {
      setLink('down', e.message);
      scheduleReconnect();
      return;
    }
    socket = sock;
    sock.onopen = () => {
      delay = BACKOFF_FIRST_MS;
      setLink('up');
      // If macropadd never says which layer is open, draw anyway.
      layerTimer = setTimeout(becomeReady, layerWaitMs);
    };
    sock.onmessage = (event) => onMessage(event.data);
    // A failed connect may raise only `error`, with no `close`, so both end the socket.
    const ended = () => {
      if (socket !== sock) return;
      socket = null;
      ready = false;
      clearTimeout(layerTimer);
      try {
        sock.close();
      } catch {
        // Already closed.
      }
      if (stopped) return;
      setLink('down', 'connection failed');
      scheduleReconnect();
    };
    sock.onerror = ended;
    sock.onclose = ended;
  };

  const scheduleReconnect = () => {
    if (stopped || reconnectTimer) return;
    reconnectTimer = setTimeout(connect, delay);
    delay = Math.min(delay * 2, BACKOFF_MAX_MS);
  };

  // data_version changes when another connection commits; the guard read
  // catches a change the pragma missed.
  const poll = () => {
    if (stopped) return;
    try {
      const v = db.dataVersion();
      if (v === version && Date.now() - lastRead < guardMs) return;
      version = v;
      unseen = db.unseen();
      lastRead = Date.now();
      readFailed = false;
      push();
    } catch (e) {
      if (!readFailed) log(e.message);
      readFailed = true;
    }
  };
  const pollTimer = setInterval(poll, pollMs);

  connect();

  return {
    stop() {
      if (stopped) return Promise.resolve();
      stopped = true;
      clearInterval(pollTimer);
      clearTimeout(layerTimer);
      clearTimeout(reconnectTimer);
      const sock = socket;
      socket = null;
      ready = false;
      return new Promise((resolve) => {
        let done = false;
        const finish = () => {
          if (done) return;
          done = true;
          if (!store) db.close();
          resolve();
        };
        if (!sock || sock.readyState === 3) {
          finish();
          return;
        }
        const timer = setTimeout(finish, CLOSE_WAIT_MS);
        sock.onclose = () => {
          clearTimeout(timer);
          finish();
        };
        try {
          sock.close();
        } catch {
          clearTimeout(timer);
          finish();
        }
      });
    },
  };
}
