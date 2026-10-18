// A zlib inflater in the style of zlib's puff.c: canonical Huffman codes
// decoded with a count and a symbol array, and no allocation. See inflate.h.
//
// SPDX-License-Identifier: MIT

#include "inflate.h"

#define MAXBITS 15
#define MAXLCODES 286
#define MAXDCODES 30
#define FIXLCODES 288
#define CANCEL_CHECK_MASK 0x3FF  // look at `cancel` every 1024 symbols

typedef struct {
    uint16_t count[MAXBITS + 1];
    uint16_t symbol[FIXLCODES];
} huffman_t;

typedef struct {
    const uint8_t *in;
    size_t in_len;
    size_t in_pos;
    uint32_t bit_buffer;
    int bit_count;
    uint8_t *out;
    size_t out_cap;
    size_t out_pos;
    const volatile uint8_t *cancel;
} state_t;

// These are not const on purpose: a const table would sit in flash.
static uint16_t length_base[29] = {
    3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31,
    35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258
};
static uint16_t length_extra[29] = {
    0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2,
    3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0
};
static uint16_t distance_base[30] = {
    1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193,
    257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145,
    8193, 12289, 16385, 24577
};
static uint16_t distance_extra[30] = {
    0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6,
    7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13
};
static uint8_t code_length_order[19] = {
    16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15
};

// Negative return values are errors; `bits` returns -1 when the input ends.
static int GLYPHCORE_RAMFUNC(bits)(state_t *s, int need) {
    while (s->bit_count < need) {
        if (s->in_pos >= s->in_len) {
            return -1;
        }
        s->bit_buffer |= (uint32_t)s->in[s->in_pos++] << s->bit_count;
        s->bit_count += 8;
    }
    int value = (int)(s->bit_buffer & ((1u << need) - 1));
    s->bit_buffer >>= need;
    s->bit_count -= need;
    return value;
}

// Decode one symbol, one bit at a time. Returns -1 for a code that is not
// in the table, and -2 when the input ends.
static int GLYPHCORE_RAMFUNC(decode)(state_t *s, const huffman_t *h) {
    int code = 0;
    int first = 0;
    int index = 0;
    for (int len = 1; len <= MAXBITS; len++) {
        int bit = bits(s, 1);
        if (bit < 0) {
            return -2;
        }
        code |= bit;
        int count = h->count[len];
        if (code - count < first) {
            return h->symbol[index + (code - first)];
        }
        index += count;
        first += count;
        first <<= 1;
        code <<= 1;
    }
    return -1;
}

// Build the decode tables from `length[0..n-1]`. Returns zero for a complete
// code, a positive number for an incomplete one, and a negative number for an
// over-subscribed one.
static int GLYPHCORE_RAMFUNC(construct)(huffman_t *h, const uint8_t *length, int n) {
    uint16_t offs[MAXBITS + 1];
    for (int len = 0; len <= MAXBITS; len++) {
        h->count[len] = 0;
    }
    for (int symbol = 0; symbol < n; symbol++) {
        h->count[length[symbol]]++;
    }
    if (h->count[0] == n) {
        return 0;
    }
    int left = 1;
    for (int len = 1; len <= MAXBITS; len++) {
        left <<= 1;
        left -= h->count[len];
        if (left < 0) {
            return left;
        }
    }
    offs[1] = 0;
    for (int len = 1; len < MAXBITS; len++) {
        offs[len + 1] = offs[len] + h->count[len];
    }
    for (int symbol = 0; symbol < n; symbol++) {
        if (length[symbol] != 0) {
            h->symbol[offs[length[symbol]]++] = symbol;
        }
    }
    return left;
}

static int GLYPHCORE_RAMFUNC(codes)(state_t *s, const huffman_t *lencode, const huffman_t *distcode) {
    unsigned symbols = 0;
    for (;;) {
        if ((++symbols & CANCEL_CHECK_MASK) == 0 && s->cancel != NULL && *s->cancel) {
            return GLYPHCORE_ERR_CANCELLED;
        }
        int symbol = decode(s, lencode);
        if (symbol < 0) {
            return GLYPHCORE_ERR_DATA;
        }
        if (symbol < 256) {
            if (s->out_pos >= s->out_cap) {
                return GLYPHCORE_ERR_OVERFLOW;
            }
            s->out[s->out_pos++] = (uint8_t)symbol;
        } else if (symbol == 256) {
            return GLYPHCORE_OK;
        } else {
            symbol -= 257;
            if (symbol >= 29) {
                return GLYPHCORE_ERR_DATA;
            }
            int extra = bits(s, length_extra[symbol]);
            if (extra < 0) {
                return GLYPHCORE_ERR_DATA;
            }
            size_t len = length_base[symbol] + extra;
            symbol = decode(s, distcode);
            if (symbol < 0 || symbol >= 30) {
                return GLYPHCORE_ERR_DATA;
            }
            extra = bits(s, distance_extra[symbol]);
            if (extra < 0) {
                return GLYPHCORE_ERR_DATA;
            }
            size_t dist = distance_base[symbol] + extra;
            if (dist > s->out_pos) {
                return GLYPHCORE_ERR_DATA;
            }
            if (s->out_cap - s->out_pos < len) {
                return GLYPHCORE_ERR_OVERFLOW;
            }
            // The copy may overlap itself, so it goes byte by byte.
            uint8_t *to = s->out + s->out_pos;
            const uint8_t *from = to - dist;
            for (size_t i = 0; i < len; i++) {
                to[i] = from[i];
            }
            s->out_pos += len;
        }
    }
}

static int GLYPHCORE_RAMFUNC(stored)(state_t *s) {
    s->bit_buffer = 0;
    s->bit_count = 0;
    if (s->in_len - s->in_pos < 4) {
        return GLYPHCORE_ERR_DATA;
    }
    unsigned len = s->in[s->in_pos] | (s->in[s->in_pos + 1] << 8);
    unsigned nlen = s->in[s->in_pos + 2] | (s->in[s->in_pos + 3] << 8);
    s->in_pos += 4;
    if (len != (~nlen & 0xFFFF)) {
        return GLYPHCORE_ERR_DATA;
    }
    if (s->in_len - s->in_pos < len) {
        return GLYPHCORE_ERR_DATA;
    }
    if (s->out_cap - s->out_pos < len) {
        return GLYPHCORE_ERR_OVERFLOW;
    }
    for (unsigned i = 0; i < len; i++) {
        s->out[s->out_pos++] = s->in[s->in_pos++];
    }
    return GLYPHCORE_OK;
}

static int GLYPHCORE_RAMFUNC(fixed)(state_t *s, huffman_t *lencode, huffman_t *distcode) {
    uint8_t lengths[FIXLCODES];
    int symbol = 0;
    for (; symbol < 144; symbol++) {
        lengths[symbol] = 8;
    }
    for (; symbol < 256; symbol++) {
        lengths[symbol] = 9;
    }
    for (; symbol < 280; symbol++) {
        lengths[symbol] = 7;
    }
    for (; symbol < FIXLCODES; symbol++) {
        lengths[symbol] = 8;
    }
    construct(lencode, lengths, FIXLCODES);
    for (symbol = 0; symbol < MAXDCODES; symbol++) {
        lengths[symbol] = 5;
    }
    construct(distcode, lengths, MAXDCODES);
    return codes(s, lencode, distcode);
}

static int GLYPHCORE_RAMFUNC(dynamic)(state_t *s, huffman_t *lencode, huffman_t *distcode) {
    uint8_t lengths[MAXLCODES + MAXDCODES];
    int nlen = bits(s, 5);
    int ndist = bits(s, 5);
    int ncode = bits(s, 4);
    if (nlen < 0 || ndist < 0 || ncode < 0) {
        return GLYPHCORE_ERR_DATA;
    }
    nlen += 257;
    ndist += 1;
    ncode += 4;
    if (nlen > MAXLCODES || ndist > MAXDCODES) {
        return GLYPHCORE_ERR_DATA;
    }
    int index = 0;
    for (; index < ncode; index++) {
        int value = bits(s, 3);
        if (value < 0) {
            return GLYPHCORE_ERR_DATA;
        }
        lengths[code_length_order[index]] = (uint8_t)value;
    }
    for (; index < 19; index++) {
        lengths[code_length_order[index]] = 0;
    }
    if (construct(lencode, lengths, 19) != 0) {
        return GLYPHCORE_ERR_DATA;
    }
    index = 0;
    while (index < nlen + ndist) {
        int symbol = decode(s, lencode);
        if (symbol < 0) {
            return GLYPHCORE_ERR_DATA;
        }
        if (symbol < 16) {
            lengths[index++] = (uint8_t)symbol;
        } else {
            int repeat;
            uint8_t value = 0;
            if (symbol == 16) {
                if (index == 0) {
                    return GLYPHCORE_ERR_DATA;
                }
                value = lengths[index - 1];
                repeat = bits(s, 2);
                repeat = repeat < 0 ? repeat : repeat + 3;
            } else if (symbol == 17) {
                repeat = bits(s, 3);
                repeat = repeat < 0 ? repeat : repeat + 3;
            } else {
                repeat = bits(s, 7);
                repeat = repeat < 0 ? repeat : repeat + 11;
            }
            if (repeat < 0 || index + repeat > nlen + ndist) {
                return GLYPHCORE_ERR_DATA;
            }
            while (repeat--) {
                lengths[index++] = value;
            }
        }
    }
    if (lengths[256] == 0) {
        return GLYPHCORE_ERR_DATA;
    }
    int err = construct(lencode, lengths, nlen);
    if (err < 0 || (err > 0 && nlen - lencode->count[0] != 1)) {
        return GLYPHCORE_ERR_DATA;
    }
    err = construct(distcode, lengths + nlen, ndist);
    if (err < 0 || (err > 0 && ndist - distcode->count[0] != 1)) {
        return GLYPHCORE_ERR_DATA;
    }
    return codes(s, lencode, distcode);
}

static uint32_t GLYPHCORE_RAMFUNC(adler32)(const uint8_t *data, size_t len) {
    uint32_t a = 1;
    uint32_t b = 0;
    while (len) {
        size_t chunk = len < 5552 ? len : 5552;  // the most that cannot overflow
        len -= chunk;
        while (chunk--) {
            a += *data++;
            b += a;
        }
        a %= 65521;
        b %= 65521;
    }
    return (b << 16) | a;
}

int GLYPHCORE_RAMFUNC(glyphcore_inflate_zlib)(const uint8_t *in, size_t in_len,
    uint8_t *out, size_t out_cap, size_t *out_len, const volatile uint8_t *cancel) {
    *out_len = 0;
    // The 2-byte header: method 8, a window of at most 32 KB, no dictionary,
    // and a check value that makes the 16-bit number a multiple of 31.
    if (in_len < 2 + 4) {
        return GLYPHCORE_ERR_HEADER;
    }
    unsigned cmf = in[0];
    unsigned flg = in[1];
    if ((cmf & 0x0F) != 8 || (cmf >> 4) > 7 || ((cmf << 8) | flg) % 31 != 0 || (flg & 0x20)) {
        return GLYPHCORE_ERR_HEADER;
    }

    // The tables are static: they are 1.2 KB each, and the stack of core 1 is
    // small. Only one inflate runs at a time.
    static huffman_t lencode;
    static huffman_t distcode;
    state_t s = {
        .in = in + 2,
        .in_len = in_len - 2,
        .out = out,
        .out_cap = out_cap,
        .cancel = cancel,
    };

    int last;
    do {
        last = bits(&s, 1);
        int type = bits(&s, 2);
        if (last < 0 || type < 0) {
            return GLYPHCORE_ERR_DATA;
        }
        int err;
        switch (type) {
            case 0:
                err = stored(&s);
                break;
            case 1:
                err = fixed(&s, &lencode, &distcode);
                break;
            case 2:
                err = dynamic(&s, &lencode, &distcode);
                break;
            default:
                err = GLYPHCORE_ERR_DATA;
        }
        if (err != GLYPHCORE_OK) {
            return err;
        }
        if (cancel != NULL && *cancel) {
            return GLYPHCORE_ERR_CANCELLED;
        }
    } while (!last);

    // The Adler-32 of the output follows the deflate data, big-endian. The
    // bit reader may have buffered whole bytes of it; give those back.
    size_t consumed = s.in_pos - (size_t)(s.bit_count / 8);
    const uint8_t *tail = in + 2 + consumed;
    if (in_len - 2 - consumed < 4) {
        return GLYPHCORE_ERR_CHECKSUM;
    }
    uint32_t want = ((uint32_t)tail[0] << 24) | ((uint32_t)tail[1] << 16) | ((uint32_t)tail[2] << 8) | tail[3];
    if (adler32(out, s.out_pos) != want) {
        return GLYPHCORE_ERR_CHECKSUM;
    }
    *out_len = s.out_pos;
    return GLYPHCORE_OK;
}
