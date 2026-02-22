// rnnoise_bridge.c
#include "rnnoise_bridge.h"
#include <stdlib.h>

DenoiseState *rnnoise_bridge_create(void)
{
    // NULL model uses the built-in RNNoise model
    return rnnoise_create(NULL);
}

void rnnoise_bridge_destroy(DenoiseState *st)
{
    if (st)
    {
        rnnoise_destroy(st);
    }
}

float rnnoise_bridge_process_frame(DenoiseState *st, float *out, const float *in)
{
    if (!st || !out || !in)
    {
        return 0.0f;
    }
    return rnnoise_process_frame(st, out, (float *)in);
}

int rnnoise_bridge_get_frame_size(void)
{
    return rnnoise_get_frame_size();
}

void rnnoise_bridge_pcm16_to_float(const int16_t *in, float *out, int n)
{
    // RNNoise expects raw int16 scale floats, NOT normalized [-1, 1]
    for (int i = 0; i < n; i++)
    {
        out[i] = (float)in[i];
    }
}

void rnnoise_bridge_float_to_pcm16(const float *in, int16_t *out, int n)
{
    for (int i = 0; i < n; i++)
    {
        float val = in[i];
        if (val > 32767.0f)
            val = 32767.0f;
        else if (val < -32768.0f)
            val = -32768.0f;
        out[i] = (int16_t)val;
    }
}
