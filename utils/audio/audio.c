#include "audio.h"
#include <stdlib.h>
#include <stdint.h>

uint8_t *resample_pcm_bytes(
    const uint8_t *pcm, size_t pcm_len,
    int num_channels, int old_rate, int new_rate,
    size_t *out_len)
{
    if (old_rate == new_rate || pcm_len < 2 || num_channels <= 0 || old_rate <= 0 || new_rate <= 0)
    {
        // just copy input
        uint8_t *out = malloc(pcm_len);
        if (!out)
            return NULL;
        for (size_t i = 0; i < pcm_len; i++)
            out[i] = pcm[i];
        *out_len = pcm_len;
        return out;
    }

    size_t num_samples = pcm_len / 2;
    if (num_samples % num_channels != 0)
        return NULL;

    size_t num_frames = num_samples / num_channels;
    size_t new_num_frames = (num_frames * new_rate) / old_rate;
    if (new_num_frames < 1)
        new_num_frames = 1;

    size_t out_size = new_num_frames * num_channels * 2;
    uint8_t *out = malloc(out_size);
    if (!out)
        return NULL;
    *out_len = out_size;

    uint32_t step = ((uint64_t)old_rate << 16) / new_rate;
    uint32_t pos = 0;

    for (size_t i = 0; i < new_num_frames; i++)
    {
        uint32_t frame = pos >> 16;
        uint32_t alpha = pos & 0xFFFF;
        if (frame >= num_frames)
        {
            frame = num_frames - 1;
            alpha = 0;
        }

        size_t f0 = frame;
        size_t f1 = frame + 1;
        if (f1 >= num_frames)
            f1 = num_frames - 1;

        for (int ch = 0; ch < num_channels; ch++)
        {
            size_t idx0 = (f0 * num_channels + ch) * 2;
            size_t idx1 = (f1 * num_channels + ch) * 2;

            int32_t s0 = (int16_t)(pcm[idx0] | (pcm[idx0 + 1] << 8));
            int32_t s1 = (int16_t)(pcm[idx1] | (pcm[idx1 + 1] << 8));

            int32_t v = s0 + ((s1 - s0) * (int32_t)alpha >> 16);

            if (v > 32767)
                v = 32767;
            else if (v < -32768)
                v = -32768;

            size_t out_idx = (i * num_channels + ch) * 2;
            out[out_idx] = v & 0xFF;
            out[out_idx + 1] = (v >> 8) & 0xFF;
        }

        pos += step;
    }

    return out;
}
