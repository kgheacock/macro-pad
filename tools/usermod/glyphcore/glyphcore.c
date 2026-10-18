// glyphcore: inflate glyphs on the second core of the RP2350, and keep the
// frame pool outside the Python heap (task 0059).
//
// Core 1 runs one job at a time. `submit` copies a zlib stream into a buffer
// of this module and wakes core 1, which inflates it into one of the glyph
// buffers. `poll` reports the result. The glyph buffers and the frame slots
// are one block of PSRAM from `port_malloc`: the garbage collector does not
// scan it, and a Python `memoryview` looks into it.
//
// SPDX-License-Identifier: MIT

#include <errno.h>
#include <string.h>

#include "hardware/sync.h"
#include "hardware/timer.h"
#include "pico/multicore.h"

#include "py/obj.h"
#include "py/objarray.h"
#include "py/runtime.h"
#include "supervisor/port.h"

#include "inflate.h"

#define GLYPH_BYTES 32768u  // 128 x 128 pixels, 2 bytes each
// Glyph buffers: two for each of up to 7 keys, and one to inflate a Store
// frame into (firmware/wire.py's GLYPH_BUFFERS).
#define GLYPH_BUFFER_COUNT 16
#define STREAM_CAP 33024u  // the longest payload of a type 6 frame is 32,768
#define CORE1_STACK_WORDS 1024
#define CORE1_IDLE_WAIT_MS 500
#define CANCEL_WAIT_MS 50
#define FLASH_WAIT_US 500000

enum {
    JOB_IDLE = 0,
    JOB_QUEUED,
    JOB_RUNNING,
    JOB_DONE,
    JOB_FAILED,
};

// All of this is static, so it survives a soft reload: core 1 keeps running
// its idle loop, and the pool stays allocated until `alloc` asks for another
// size.
static uint8_t *pool_base;
static uint8_t *stream_buffer;
static size_t frame_slot_count;

static volatile uint32_t job_state = JOB_IDLE;
static volatile uint8_t cancel_flag;
static volatile int32_t job_result;
static volatile uint32_t job_us;
static volatile uint8_t flash_blocked;
static size_t job_in_len;
static uint8_t *job_out;

static bool core1_started;
static uint32_t core1_stack[CORE1_STACK_WORDS] __attribute__((aligned(8)));

static void __not_in_flash_func(core1_main)(void) {
    for (;;) {
        while (job_state != JOB_QUEUED) {
            __wfe();
        }
        job_state = JOB_RUNNING;
        __dmb();
        uint32_t start = timer_hw->timerawl;
        size_t out_len = 0;
        int status = glyphcore_inflate_zlib(stream_buffer, job_in_len, job_out, GLYPH_BYTES, &out_len, &cancel_flag);
        job_us = timer_hw->timerawl - start;
        job_result = status == GLYPHCORE_OK ? (int32_t)out_len : status;
        __dmb();
        job_state = status == GLYPHCORE_OK ? JOB_DONE : JOB_FAILED;
        __dmb();
        __sev();
    }
}

static void start_core1(void) {
    if (!core1_started) {
        multicore_launch_core1_with_stack(core1_main, core1_stack, sizeof(core1_stack));
        core1_started = true;
    }
}

// The port calls flash_range_erase and flash_range_program for a write to the
// CIRCUITPY drive or to NVM. While one runs, nothing may read the XIP window,
// and core 1 reads PSRAM through it. Wait for the job on core 1 to end, and
// refuse a new one until the flash call returns.
extern void __real_flash_range_erase(uint32_t flash_offs, size_t count);
extern void __real_flash_range_program(uint32_t flash_offs, const uint8_t *data, size_t count);
void __wrap_flash_range_erase(uint32_t flash_offs, size_t count);
void __wrap_flash_range_program(uint32_t flash_offs, const uint8_t *data, size_t count);

static void __no_inline_not_in_flash_func(flash_enter)(void) {
    flash_blocked = 1;
    __dmb();
    // A glyph takes under 100 ms on core 1. Let the job finish, so that its
    // result is not lost, and give up only for a core 1 that has hung: a flash
    // write that never starts would hang the board as well.
    uint32_t start = timer_hw->timerawl;
    while ((job_state == JOB_QUEUED || job_state == JOB_RUNNING) &&
           timer_hw->timerawl - start < FLASH_WAIT_US) {
        tight_loop_contents();
    }
}

static void __no_inline_not_in_flash_func(flash_leave)(void) {
    __dmb();
    flash_blocked = 0;
}

void __no_inline_not_in_flash_func(__wrap_flash_range_erase)(uint32_t flash_offs, size_t count) {
    flash_enter();
    __real_flash_range_erase(flash_offs, count);
    flash_leave();
}

void __no_inline_not_in_flash_func(__wrap_flash_range_program)(uint32_t flash_offs, const uint8_t *data, size_t count) {
    flash_enter();
    __real_flash_range_program(flash_offs, data, count);
    flash_leave();
}

// Wait until core 1 has no job in progress. Returns false when it does not.
static bool wait_for_idle(uint32_t timeout_ms) {
    uint64_t deadline = time_us_64() + (uint64_t)timeout_ms * 1000;
    cancel_flag = 1;
    while (job_state == JOB_QUEUED || job_state == JOB_RUNNING) {
        if (time_us_64() > deadline) {
            cancel_flag = 0;
            return false;
        }
    }
    cancel_flag = 0;
    job_state = JOB_IDLE;
    return true;
}

// glyphcore.alloc(frame_slots)
// Make the pool: `frame_slots` frame slots and GLYPH_BUFFER_COUNT glyph
// buffers, each 32 KB. The same size again changes nothing.
static mp_obj_t glyphcore_alloc(mp_obj_t frame_slots_in) {
    mp_int_t slots = mp_obj_get_int(frame_slots_in);
    if (slots < 0 || slots > 255) {
        mp_raise_ValueError(MP_ERROR_TEXT("pool size out of range"));
    }
    start_core1();
    if (!wait_for_idle(CORE1_IDLE_WAIT_MS)) {
        mp_raise_OSError(EBUSY);
    }
    if (pool_base != NULL && (size_t)slots == frame_slot_count) {
        return mp_const_none;
    }
    if (pool_base != NULL) {
        port_free(pool_base);
        pool_base = NULL;
        frame_slot_count = 0;
    }
    if (stream_buffer == NULL) {
        stream_buffer = port_malloc(STREAM_CAP, false);
        if (stream_buffer == NULL) {
            mp_raise_type(&mp_type_MemoryError);
        }
    }
    uint8_t *block = port_malloc(((size_t)slots + GLYPH_BUFFER_COUNT) * GLYPH_BYTES, false);
    if (block == NULL) {
        mp_raise_type(&mp_type_MemoryError);
    }
    pool_base = block;
    frame_slot_count = slots;
    return mp_const_none;
}
static MP_DEFINE_CONST_FUN_OBJ_1(glyphcore_alloc_obj, glyphcore_alloc);

static uint8_t *frame_slot(mp_int_t slot) {
    if (pool_base == NULL || slot < 0 || (size_t)slot >= frame_slot_count) {
        mp_raise_ValueError(MP_ERROR_TEXT("no such frame slot"));
    }
    return pool_base + (size_t)slot * GLYPH_BYTES;
}

static uint8_t *glyph_buffer(mp_int_t index) {
    if (pool_base == NULL || index < 0 || index >= GLYPH_BUFFER_COUNT) {
        mp_raise_ValueError(MP_ERROR_TEXT("no such glyph buffer"));
    }
    return pool_base + (frame_slot_count + (size_t)index) * GLYPH_BYTES;
}

// glyphcore.slot_view(slot) -> memoryview('B', 32768)
static mp_obj_t glyphcore_slot_view(mp_obj_t slot_in) {
    return mp_obj_new_memoryview('B', GLYPH_BYTES, frame_slot(mp_obj_get_int(slot_in)));
}
static MP_DEFINE_CONST_FUN_OBJ_1(glyphcore_slot_view_obj, glyphcore_slot_view);

// glyphcore.glyph_view(index) -> memoryview('H', 16384), a new object each call
static mp_obj_t glyphcore_glyph_view(mp_obj_t index_in) {
    return mp_obj_new_memoryview('H', GLYPH_BYTES / 2, glyph_buffer(mp_obj_get_int(index_in)));
}
static MP_DEFINE_CONST_FUN_OBJ_1(glyphcore_glyph_view_obj, glyphcore_glyph_view);

// glyphcore.submit(stream, glyph_index)
// Start an inflate of the zlib `stream` on core 1, into glyph buffer
// `glyph_index`. The stream is copied: the caller may reuse it at once.
// Raises OSError(EBUSY) while a job is in progress or its result is not
// collected, or while a flash write is on. The caller can inflate on core 0.
static mp_obj_t glyphcore_submit(mp_obj_t stream_in, mp_obj_t index_in) {
    mp_buffer_info_t stream;
    mp_get_buffer_raise(stream_in, &stream, MP_BUFFER_READ);
    uint8_t *out = glyph_buffer(mp_obj_get_int(index_in));
    if (stream.len > STREAM_CAP) {
        mp_raise_ValueError(MP_ERROR_TEXT("stream too long"));
    }
    if (job_state != JOB_IDLE || flash_blocked) {
        mp_raise_OSError(EBUSY);
    }
    memcpy(stream_buffer, stream.buf, stream.len);
    job_in_len = stream.len;
    job_out = out;
    cancel_flag = 0;
    __dmb();
    job_state = JOB_QUEUED;
    __dmb();
    __sev();
    return mp_const_none;
}
static MP_DEFINE_CONST_FUN_OBJ_2(glyphcore_submit_obj, glyphcore_submit);

// glyphcore.poll() -> None while a job runs, else the inflated length.
// Raises ValueError for a stream that does not inflate to a whole glyph.
static mp_obj_t glyphcore_poll(void) {
    uint32_t state = job_state;
    if (state == JOB_DONE) {
        int32_t length = job_result;
        job_state = JOB_IDLE;
        if (length != (int32_t)GLYPH_BYTES) {
            mp_raise_ValueError(MP_ERROR_TEXT("glyph is not 32768 bytes"));
        }
        return mp_obj_new_int(length);
    }
    if (state == JOB_FAILED) {
        job_state = JOB_IDLE;
        mp_raise_ValueError(MP_ERROR_TEXT("glyph does not inflate"));
    }
    return mp_const_none;
}
static MP_DEFINE_CONST_FUN_OBJ_0(glyphcore_poll_obj, glyphcore_poll);

// glyphcore.cancel() -> bool
// Stop the job in progress and drop its result. True when core 1 is idle.
static mp_obj_t glyphcore_cancel(void) {
    return mp_obj_new_bool(wait_for_idle(CANCEL_WAIT_MS));
}
static MP_DEFINE_CONST_FUN_OBJ_0(glyphcore_cancel_obj, glyphcore_cancel);

// glyphcore.last_us() -> the time core 1 spent on the last job, in microseconds
static mp_obj_t glyphcore_last_us(void) {
    return mp_obj_new_int_from_uint(job_us);
}
static MP_DEFINE_CONST_FUN_OBJ_0(glyphcore_last_us_obj, glyphcore_last_us);

// glyphcore.has_transparent(pixels) -> bool
// True when the 16-bit buffer has a pixel of value 0x0000.
static mp_obj_t glyphcore_has_transparent(mp_obj_t pixels_in) {
    mp_buffer_info_t pixels;
    mp_get_buffer_raise(pixels_in, &pixels, MP_BUFFER_READ);
    const uint16_t *p = pixels.buf;
    size_t count = pixels.len / 2;
    for (size_t i = 0; i < count; i++) {
        if (p[i] == 0) {
            return mp_const_true;
        }
    }
    return mp_const_false;
}
static MP_DEFINE_CONST_FUN_OBJ_1(glyphcore_has_transparent_obj, glyphcore_has_transparent);

static const mp_rom_map_elem_t glyphcore_module_globals_table[] = {
    { MP_ROM_QSTR(MP_QSTR___name__), MP_ROM_QSTR(MP_QSTR_glyphcore) },
    { MP_ROM_QSTR(MP_QSTR_alloc), MP_ROM_PTR(&glyphcore_alloc_obj) },
    { MP_ROM_QSTR(MP_QSTR_slot_view), MP_ROM_PTR(&glyphcore_slot_view_obj) },
    { MP_ROM_QSTR(MP_QSTR_glyph_view), MP_ROM_PTR(&glyphcore_glyph_view_obj) },
    { MP_ROM_QSTR(MP_QSTR_submit), MP_ROM_PTR(&glyphcore_submit_obj) },
    { MP_ROM_QSTR(MP_QSTR_poll), MP_ROM_PTR(&glyphcore_poll_obj) },
    { MP_ROM_QSTR(MP_QSTR_cancel), MP_ROM_PTR(&glyphcore_cancel_obj) },
    { MP_ROM_QSTR(MP_QSTR_last_us), MP_ROM_PTR(&glyphcore_last_us_obj) },
    { MP_ROM_QSTR(MP_QSTR_has_transparent), MP_ROM_PTR(&glyphcore_has_transparent_obj) },
};
static MP_DEFINE_CONST_DICT(glyphcore_module_globals, glyphcore_module_globals_table);

const mp_obj_module_t glyphcore_module = {
    .base = { &mp_type_module },
    .globals = (mp_obj_dict_t *)&glyphcore_module_globals,
};

MP_REGISTER_MODULE(MP_QSTR_glyphcore, glyphcore_module);
