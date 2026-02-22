// resample_bridge.c
#include "resample_bridge.h"
#include <samplerate.h>
#include <stdlib.h>
#include <string.h>

struct ResamplerInternal
{
    SRC_STATE *src_state;
    SRC_DATA src_data;
    int channels;
    float *input_buffer;
    size_t input_buffer_size;
};

ResamplerState resampler_new(int channels, int quality, double src_ratio)
{
    if (channels <= 0 || quality < 0 || quality > 4 || src_ratio <= 0)
    {
        return NULL;
    }

    struct ResamplerInternal *resampler =
        (struct ResamplerInternal *)calloc(1, sizeof(struct ResamplerInternal));
    if (!resampler)
        return NULL;

    int src_error = 0;
    int src_quality = SRC_SINC_BEST_QUALITY;

    // Map quality to libsamplerate converter types
    switch (quality)
    {
    case 0:
        src_quality = SRC_SINC_BEST_QUALITY;
        break;
    case 1:
        src_quality = SRC_SINC_MEDIUM_QUALITY;
        break;
    case 2:
        src_quality = SRC_SINC_FASTEST;
        break;
    case 3:
        src_quality = SRC_LINEAR;
        break;
    case 4:
        src_quality = SRC_ZERO_ORDER_HOLD;
        break;
    default:
        src_quality = SRC_SINC_MEDIUM_QUALITY;
    }

    resampler->src_state = src_new(src_quality, channels, &src_error);
    if (!resampler->src_state)
    {
        free(resampler);
        return NULL;
    }

    resampler->channels = channels;

    // Initialize SRC_DATA structure
    memset(&resampler->src_data, 0, sizeof(SRC_DATA));
    resampler->src_data.src_ratio = src_ratio;

    return (ResamplerState)resampler;
}

int resampler_process(ResamplerState state,
                      const float *input, long input_frames,
                      float *output, long output_capacity)
{
    struct ResamplerInternal *resampler = (struct ResamplerInternal *)state;
    if (!resampler || !input || !output)
        return -1;

    // Set up SRC_DATA for this process call
    resampler->src_data.data_in = (float *)input;
    resampler->src_data.input_frames = input_frames;
    resampler->src_data.data_out = output;
    resampler->src_data.output_frames = output_capacity;
    resampler->src_data.end_of_input = 0;

    int error = src_process(resampler->src_state, &resampler->src_data);
    if (error)
        return -1;

    return (int)resampler->src_data.output_frames_gen;
}

void resampler_reset(ResamplerState state)
{
    struct ResamplerInternal *resampler = (struct ResamplerInternal *)state;
    if (resampler && resampler->src_state)
    {
        src_reset(resampler->src_state);
    }
}

long resampler_get_output_len(ResamplerState state, long input_frames)
{
    struct ResamplerInternal *resampler = (struct ResamplerInternal *)state;
    if (!resampler)
        return -1;

    double output_frames = (double)input_frames * resampler->src_data.src_ratio;
    return (long)(output_frames + 8); // Add some margin for filter settling
}

void resampler_free(ResamplerState state)
{
    struct ResamplerInternal *resampler = (struct ResamplerInternal *)state;
    if (resampler)
    {
        if (resampler->src_state)
        {
            src_delete(resampler->src_state);
        }
        free(resampler);
    }
}

void pcm16_to_float(const int16_t *input, float *output, int samples)
{
    for (int i = 0; i < samples; i++)
    {
        // Convert int16 to float in range [-1.0, 1.0]
        output[i] = (float)input[i] / 32768.0f;
    }
}

void float_to_pcm16(const float *input, int16_t *output, int samples)
{
    for (int i = 0; i < samples; i++)
    {
        // Convert float to int16 with clipping
        float val = input[i] * 32768.0f;
        if (val > 32767.0f)
            val = 32767.0f;
        if (val < -32768.0f)
            val = -32768.0f;
        output[i] = (int16_t)val;
    }
}