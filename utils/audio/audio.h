#ifndef AUDIO_H
#define AUDIO_H

#include <stdint.h>
#include <stddef.h>

// Resample 16-bit PCM bytes
// pcm: input buffer
// pcm_len: length of input buffer in bytes
// num_channels: number of audio channels
// old_rate: original sample rate
// new_rate: target sample rate
// out_len: pointer to store length of output buffer
// Returns: pointer to newly allocated PCM buffer (caller must free)
uint8_t *resample_pcm_bytes(
    const uint8_t *pcm, size_t pcm_len,
    int num_channels, int old_rate, int new_rate,
    size_t *out_len);

#endif
