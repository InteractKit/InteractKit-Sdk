#ifndef OPUS_ENCODER_H
#define OPUS_ENCODER_H

#include <stdint.h>
#include <opus/opus.h>

#ifdef __cplusplus
extern "C"
{
#endif

    // Opaque handle types
    typedef struct OpusEncoderHandle OpusEncoderHandle;
    typedef struct OpusDecoderHandle OpusDecoderHandle;

    // Create Opus encoder
    OpusEncoderHandle *wrapper_opus_encoder_create(int sample_rate, int channels, int application, int *error);

    // Destroy Opus encoder
    void wrapper_opus_encoder_destroy(OpusEncoderHandle *encoder);

    // Encode PCM to Opus
    int wrapper_opus_encode(OpusEncoderHandle *encoder, const int16_t *pcm, int frame_size,
                            unsigned char *data, int max_data_bytes);

    // Create Opus decoder
    OpusDecoderHandle *wrapper_opus_decoder_create(int sample_rate, int channels, int *error);

    // Destroy Opus decoder
    void wrapper_opus_decoder_destroy(OpusDecoderHandle *decoder);

    // Decode Opus to PCM
    int wrapper_opus_decode(OpusDecoderHandle *decoder, const unsigned char *data, int data_size,
                            int16_t *pcm, int frame_size, int decode_fec);

    // Get frame size from packet
    int wrapper_opus_packet_get_samples_per_frame(const unsigned char *data, int sampling_rate);

    // Get number of channels from packet
    int wrapper_opus_packet_get_nb_channels(const unsigned char *data);

    // Get frame count from packet
    int wrapper_opus_packet_get_nb_frames(const unsigned char *packet, int len);

#ifdef __cplusplus
}
#endif

#endif // OPUS_ENCODER_H