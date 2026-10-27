// The JSON messages the daemon exchanges with macropadd. The shapes are the
// ones driver/README.md "Protocol" documents; test/protocol.test.js checks
// them against test/fixtures/protocol.json.

// A message to send: the full values of one key or child (`parent` names the
// key a child belongs to, and `path` the ids from a key down to the parent of
// a deeper child, task 0086).
export function setLayerKeyMessage(fields) {
  return { kind: 'setLayerKey', setLayerKey: fields };
}

// A message to send: remove one key, or with `parent` one child of it. A
// `parent` that is an array is a path, the ids from a key down to the
// child's parent (task 0086).
export function removeLayerKeyMessage(id, parent) {
  if (Array.isArray(parent)) return { kind: 'removeLayerKey', removeLayerKey: { id, path: parent } };
  return { kind: 'removeLayerKey', removeLayerKey: parent ? { id, parent } : { id } };
}

// The JSON text of a frame.
export function encodeSetLayerKey(fields) {
  return JSON.stringify(setLayerKeyMessage(fields));
}

export function encodeRemoveLayerKey(id, parent) {
  return JSON.stringify(removeLayerKeyMessage(id, parent));
}

// Reads one text frame from macropadd. Returns
//   { kind: 'event', keyIndex, type, timestamp }
//   { kind: 'layerChanged', active, path }   (active is the open layer's id, or null for
//                                             the top layer; path is the ids down to it, task 0086)
//   { kind: 'childPressed', keyIndex, parent, child, path }
//   { kind: 'other' }                  (a message the daemon does not use)
// or null when the text is not a message.
export function decodeMessage(text) {
  let message;
  try {
    message = JSON.parse(text);
  } catch {
    return null;
  }
  if (message === null || typeof message !== 'object' || typeof message.kind !== 'string') {
    return null;
  }
  if (message.kind === 'event') {
    const e = message.event;
    if (!e || !Number.isInteger(e.keyIndex) || typeof e.type !== 'string') return null;
    return { kind: 'event', keyIndex: e.keyIndex, type: e.type, timestamp: e.timestamp ?? 0 };
  }
  if (message.kind === 'childPressed') {
    const p = message.childPressed;
    if (!p || !Number.isInteger(p.keyIndex) || typeof p.parent !== 'string' || typeof p.child !== 'string') return null;
    const path = idPath(p.path, [p.parent]);
    if (path === null) return null;
    return { kind: 'childPressed', keyIndex: p.keyIndex, parent: p.parent, child: p.child, path };
  }
  if (message.kind === 'layerChanged') {
    const active = message.layerChanged?.active;
    if (active !== null && typeof active !== 'string') return null;
    const path = idPath(message.layerChanged.path, active === null ? [] : [active]);
    if (path === null) return null;
    return { kind: 'layerChanged', active, path };
  }
  return { kind: 'other' };
}

// A path of ids from a message, or fallback when the message has none (one
// from a macropadd before task 0086). Null when it is not a list of strings.
function idPath(path, fallback) {
  if (path === undefined) return fallback;
  if (!Array.isArray(path) || !path.every((id) => typeof id === 'string')) return null;
  return path;
}
