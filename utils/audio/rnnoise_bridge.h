// rnnoise_bridge.h
#ifndef RNNOISE_BRIDGE_H
#define RNNOISE_BRIDGE_H

#include <stdint.h>
#include <rnnoise.h>

#ifdef __cplusplus
extern "C"
{
#endif

// RNNoise processes exactly 480 samples (10ms) at 48kHz mono.
#define RNNOISE_BRIDGE_FRAME_SIZE 480

    // Create a new denoiser state using the built-in model.
    // Returns NULL on failure.
    DenoiseState *rnnoise_bridge_create(void);

    // Destroy a denoiser state.
    void rnnoise_bridge_destroy(DenoiseState *st);

    // Process one 480-sample frame.
    // in/out: float arrays of exactly RNNOISE_BRIDGE_FRAME_SIZE elements.
    // RNNoise uses raw int16 scale ([-32768, 32767]) as floats, not normalized.
    // Returns the VAD probability in [0.0, 1.0].
    float rnnoise_bridge_process_frame(DenoiseState *st, float *out, const float *in);

    // Get the frame size (always 480).
    int rnnoise_bridge_get_frame_size(void);

    // Convert int16 PCM samples to float using raw int16 scale (no normalization).
    void rnnoise_bridge_pcm16_to_float(const int16_t *in, float *out, int n);

    // Convert float (raw int16 scale) back to int16 PCM with saturation clipping.
    void rnnoise_bridge_float_to_pcm16(const float *in, int16_t *out, int n);

#ifdef __cplusplus
}
#endif

#endif // RNNOISE_BRIDGE_H
