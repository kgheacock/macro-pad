// The JSON messages the daemon exchanges with macropadd. The shapes are the
// ones driver/README.md "Protocol" documents; test/protocol.test.js checks
// them against test/fixtures/protocol.json.

// A message to send: the full values of one key or child (`parent` names the
// key a child belongs to).
export function setLayerKeyMessage(fields) {
  return { kind: 'setLayerKey', setLayerKey: fields };
}

// A message to send: remove one key, or with `parent` one child of it.
export function removeLayerKeyMessage(id, parent) {
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
//   { kind: 'layerChanged', active }   (active is a string, or null for the top layer)
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
    return { kind: 'childPressed', keyIndex: p.keyIndex, parent: p.parent, child: p.child };
  }
  if (message.kind === 'layerChanged') {
    const active = message.layerChanged?.active;
    if (active !== null && typeof active !== 'string') return null;
    return { kind: 'layerChanged', active };
  }
  return { kind: 'other' };
}
