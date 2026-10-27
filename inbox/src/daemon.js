// The inbox daemon: watch the store, keep macropadd's layer tree in step.
// startDaemon is the loop; acquirePidFile keeps one daemon per store and plugin.

import { mkdirSync, readFileSync, unlinkSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { DaemonRunningError, StoreError } from './errors.js';
import { focus as focusHere, parseHere } from './here.js';
import { decodeMessage } from './protocol.js';
import { openStore } from './store.js';
import { getPlugin } from './plugins/index.js';
import { createRenderer } from './render.js';

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

// The pid file of the daemon of one plugin on one store.
export function pidPath(file, plugin) {
  return `${file}.${plugin}.daemon.pid`;
}

// Creates <db>.<plugin>.daemon.pid with this process's id. Throws
// DaemonRunningError when a live daemon holds it, and replaces a stale file.
// Returns a function that removes the file.
export function acquirePidFile(
  file,
  { plugin = getPlugin().name, log = () => {}, pid = process.pid, alive = isAlive } = {},
) {
  const pidFile = pidPath(file, plugin);
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
      throw new DaemonRunningError(`a ${plugin} daemon already runs for ${file} (pid ${other})`);
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
// plugin    the plugin it shows (plugins/index.js): the store rows it reads,
//           and the rules of its keys. A press is read only in the layer of
//           its top key.
// store     an open Store for that plugin (a test passes one to count
//           queries); else the daemon opens `file` and closes it on stop
// focus     (here) -> Promise, brings a "here" to the front (here.js); a test
//           passes a stub
// onEvent   (event, { active }) for each event from macropadd, after the daemon
//           has acted on it
// feedMs    how often a plugin with a feed runs it; default its everyMs
//
// A plugin may have a feed, which writes the plugin's own messages (task
// 0076): { source, everyMs, fetch({ env }) -> Promise<answer>,
// sync(store, answer) -> how many messages it wrote }. The daemon runs it at
// start and every everyMs, one run at a time, and draws what it wrote. fetch
// also gets the store, and the renderer gets the newest answer as `fed`.
//
// A plugin may draw its own keys (task 0085): createRenderer({ slot, env })
// returns a renderer like render.js's, with owns(parent), which says whether
// a key is one of its own. Its press({ parent, child, store, file, env, fed,
// focus, changed, log }) -> Promise acts on a press on a child of such a key;
// the daemon draws again when it settles, and when it calls changed().
export function startDaemon({
  file,
  env = process.env,
  port = DEFAULT_PORT,
  slot,
  pollMs = DEFAULT_POLL_MS,
  guardMs = GUARD_MS,
  layerWaitMs = LAYER_WAIT_MS,
  log = () => {},
  plugin = getPlugin(),
  store,
  focus = focusHere,
  onEvent = () => {},
  feedMs,
  WebSocketImpl = globalThis.WebSocket,
}) {
  const db = store ?? openStore(file, env, { plugin: plugin.name });
  const renderer = plugin.createRenderer
    ? plugin.createRenderer({ slot, env })
    : createRenderer({ slot, rules: plugin.rules });
  const topId = renderer.topId;
  if (plugin.createRenderer && slot !== undefined) log(`--slot is ignored for ${plugin.name}: its keys take the free slots`);
  const url = `ws://127.0.0.1:${port}/`;

  // The rows to show: the channels with an unseen message, or, for a plugin
  // with activeMs, the channels with a message in that time.
  const { activeMs } = plugin.rules;
  const readRows = () => (activeMs ? db.recent(Date.now() - activeMs) : db.unseen());

  let version;
  let unseen;
  try {
    version = db.dataVersion();
    unseen = readRows();
  } catch (e) {
    if (!store) db.close();
    throw e;
  }
  let lastRead = Date.now();
  let readFailed = false;
  // The newest answer of the plugin's feed, or null.
  let fed = null;

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
    for (const message of renderer.plan({ unseen, active, fed })) {
      try {
        socket.send(JSON.stringify(message));
      } catch (e) {
        log(`cannot send to macropadd: ${e.message}`);
        return;
      }
    }
  };

  const becomeReady = () => {
    if (ready || stopped || !socket) return;
    clearTimeout(layerTimer);
    let channels = [];
    try {
      channels = db.channels();
    } catch (e) {
      log(e.message);
    }
    renderer.reset(channels);
    ready = true;
    push();
  };

  // Reads the store and draws now. A write on this daemon's own connection
  // does not change data_version, so poll() would not see it.
  const reread = () => {
    try {
      version = db.dataVersion();
      unseen = readRows();
      lastRead = Date.now();
      readFailed = false;
      push();
    } catch (e) {
      log(e.message);
    }
  };

  // Runs the plugin's feed. A failure is logged once until a run succeeds.
  const { feed } = plugin;
  let feeding = false;
  let feedFailure = null;
  const runFeed = async () => {
    if (stopped || feeding) return;
    feeding = true;
    try {
      const answer = await feed.fetch({ env, store: db });
      if (stopped) return;
      const wrote = feed.sync(db, answer);
      if (feedFailure !== null) log(`${feed.source} read again`);
      feedFailure = null;
      fed = answer;
      if (wrote > 0) reread();
      else push();
    } catch (e) {
      if (stopped) return;
      const text = `cannot read ${feed.source}: ${e.message}`;
      if (text !== feedFailure) log(text);
      feedFailure = text;
    } finally {
      feeding = false;
    }
  };

  // Goes to the "here" of a pressed key. A failure is logged and changes
  // nothing else; the mark has already happened.
  const goHere = async (child) => {
    const kind = parseHere(child.here)?.kind ?? 'invalid';
    try {
      await focus(child.here);
    } catch (e) {
      log(`cannot go to the ${kind} "here" of ${child.channel}: ${e.message}`);
    }
  };

  // A plugin's own press: a failure is logged, and the keys drawn again.
  const pressed = async (parent, child) => {
    const changed = () => {
      if (!stopped) reread();
    };
    try {
      await plugin.press({ parent, child, store: db, file, env, fed, focus, changed, log });
    } catch (e) {
      log(`cannot do ${child}: ${e.message}`);
    }
    changed();
  };

  // A press on a channel key of the top key's layer marks the messages that
  // key showed as seen, then goes to their "here". macropadd names the child
  // a press landed on, on whatever page (task 0078), so the daemon needs no
  // key map. A press on a child of another client's key is not ours.
  const onChildPressed = (parent, channel) => {
    if (plugin.press) {
      if (renderer.owns(parent)) pressed(parent, channel);
      return;
    }
    if (parent !== topId) return;
    const child = renderer.target(channel);
    if (child === null || child.shownId === 0) return;
    try {
      db.markSeen(child.channel, child.shownId);
    } catch (e) {
      log(`cannot mark ${child.channel} seen: ${e.message}`);
      return;
    }
    // Show it now rather than at the next poll.
    reread();
    if (child.here) goHere(child);
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
    } else if (message.kind === 'childPressed') {
      onChildPressed(message.parent, message.child);
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
      unseen = readRows();
      lastRead = Date.now();
      readFailed = false;
      push();
    } catch (e) {
      if (!readFailed) log(e.message);
      readFailed = true;
    }
  };
  const pollTimer = setInterval(poll, pollMs);
  const feedTimer = feed ? setInterval(runFeed, feedMs ?? feed.everyMs) : null;

  connect();
  if (feed) runFeed();

  return {
    stop() {
      if (stopped) return Promise.resolve();
      stopped = true;
      clearInterval(pollTimer);
      clearInterval(feedTimer);
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
