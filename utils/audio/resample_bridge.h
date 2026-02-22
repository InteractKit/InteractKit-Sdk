// resample_bridge.h
#ifndef RESAMPLE_BRIDGE_H
#define RESAMPLE_BRIDGE_H

#include <stdint.h>

#ifdef __cplusplus
extern "C"
{
#endif

    // Opaque pointer to resampler state
    typedef void *ResamplerState;

    // Resampler configuration
    typedef struct
    {
        double src_ratio;
        int channels;
        int quality; // 0-4: best, medium, fast, linear, zero-order hold
    } ResamplerConfig;

    // Initialize resampler
    ResamplerState resampler_new(int channels, int quality, double src_ratio);

    // Process audio data
    // Returns number of output frames produced, or -1 on error
    int resampler_process(ResamplerState state,
                          const float *input, long input_frames,
                          float *output, long output_capacity);

    // Reset resampler state
    void resampler_reset(ResamplerState state);

    // Get required output size for a given input
    long resampler_get_output_len(ResamplerState state, long input_frames);

    // Destroy resampler
    void resampler_free(ResamplerState state);

    // Utility: Convert int16 PCM to float (libsamplerate uses floats)
    void pcm16_to_float(const int16_t *input, float *output, int samples);

    // Utility: Convert float to int16 PCM
    void float_to_pcm16(const float *input, int16_t *output, int samples);

#ifdef __cplusplus
}
#endif
#endif