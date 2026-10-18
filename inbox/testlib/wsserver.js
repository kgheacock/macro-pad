// A small WebSocket server standing in for macropadd in tests: it accepts
// connections on 127.0.0.1, collects the text frames clients send, and sends
// text frames to every client. Node has no built-in server, so this speaks
// just enough of RFC 6455 (text frames under 64 KiB, close).

import { createHash } from 'node:crypto';
import http from 'node:http';

const GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11';

function frame(opcode, payload) {
  const body = Buffer.from(payload);
  const head =
    body.length < 126
      ? Buffer.from([0x80 | opcode, body.length])
      : Buffer.from([0x80 | opcode, 126, body.length >> 8, body.length & 0xff]);
  return Buffer.concat([head, body]);
}

// Reads whole client frames out of buf. Returns the frames and what is left.
function readFrames(buf) {
  const frames = [];
  for (;;) {
    if (buf.length < 2) break;
    let length = buf[1] & 0x7f;
    let offset = 2;
    if (length === 126) {
      if (buf.length < 4) break;
      length = buf.readUInt16BE(2);
      offset = 4;
    }
    if (buf.length < offset + 4 + length) break;
    const mask = buf.subarray(offset, offset + 4);
    const data = Buffer.from(buf.subarray(offset + 4, offset + 4 + length));
    for (let i = 0; i < data.length; i++) data[i] ^= mask[i % 4];
    frames.push({ opcode: buf[0] & 0x0f, data });
    buf = buf.subarray(offset + 4 + length);
  }
  return { frames, rest: buf };
}

export class TestServer {
  constructor() {
    this.received = []; // parsed JSON of each text frame, in order
    this.connections = 0;
    this.sockets = new Set();
    this.onConnect = () => {};
    this.http = http.createServer();
    this.http.on('upgrade', (req, socket) => {
      const accept = createHash('sha1').update(req.headers['sec-websocket-key'] + GUID).digest('base64');
      socket.write(
        `HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`,
      );
      this.sockets.add(socket);
      this.connections++;
      let pending = Buffer.alloc(0);
      socket.on('data', (chunk) => {
        const { frames, rest } = readFrames(Buffer.concat([pending, chunk]));
        pending = rest;
        for (const f of frames) {
          if (f.opcode === 1) this.received.push(JSON.parse(f.data.toString('utf8')));
          if (f.opcode === 8) {
            socket.end(frame(8, f.data.subarray(0, 2)));
          }
        }
      });
      socket.on('error', () => {});
      socket.on('close', () => this.sockets.delete(socket));
      this.onConnect(socket);
    });
  }

  // Starts on port (0: any free port). Returns the port.
  listen(port = 0) {
    return new Promise((resolve, reject) => {
      this.http.once('error', reject);
      this.http.listen(port, '127.0.0.1', () => {
        this.port = this.http.address().port;
        resolve(this.port);
      });
    });
  }

  send(message) {
    for (const s of this.sockets) s.write(frame(1, JSON.stringify(message)));
  }

  layerChanged(active = null) {
    this.send({ kind: 'layerChanged', layerChanged: { active } });
  }

  // Sends layerChanged to each client as it connects, as macropadd does.
  announceLayer(active = null) {
    this.onConnect = (socket) => socket.write(frame(1, JSON.stringify({ kind: 'layerChanged', layerChanged: { active } })));
  }

  // Closes every connection and stops listening.
  close() {
    for (const s of this.sockets) s.destroy();
    this.sockets.clear();
    return new Promise((resolve) => this.http.close(() => resolve()));
  }
}

// Polls until check() returns something truthy, or fails after ms.
export async function waitFor(check, ms = 3000, what = 'condition') {
  const deadline = Date.now() + ms;
  for (;;) {
    const value = check();
    if (value) return value;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, 10));
  }
}
