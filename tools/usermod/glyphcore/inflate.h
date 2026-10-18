// A zlib inflater for a flat output buffer, for the core-1 worker of glyphcore
// (task 0059). It allocates nothing and calls nothing in flash: the code and
// the tables live in RAM, so core 1 can run it while core 0 reads flash.
//
// SPDX-License-Identifier: MIT

#ifndef GLYPHCORE_INFLATE_H
#define GLYPHCORE_INFLATE_H

#include <stddef.h>
#include <stdint.h>

#ifdef GLYPHCORE_HOST
#define GLYPHCORE_RAMFUNC
#else
#include "pico.h"
#define GLYPHCORE_RAMFUNC __not_in_flash_func
#endif

#define GLYPHCORE_OK 0
#define GLYPHCORE_ERR_HEADER (-1)
#define GLYPHCORE_ERR_DATA (-2)
#define GLYPHCORE_ERR_OVERFLOW (-3)
#define GLYPHCORE_ERR_CHECKSUM (-4)
#define GLYPHCORE_ERR_CANCELLED (-5)

// Inflate the zlib stream `in` into `out`. The output is the dictionary, so
// `out` must stay whole until the call returns. `*out_len` is the number of
// bytes written. `cancel` may be NULL; the call stops with
// GLYPHCORE_ERR_CANCELLED soon after `*cancel` becomes non-zero.
int glyphcore_inflate_zlib(const uint8_t *in, size_t in_len, uint8_t *out,
    size_t out_cap, size_t *out_len, const volatile uint8_t *cancel);

#endif
