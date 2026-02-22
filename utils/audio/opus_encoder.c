#include "opus_encoder.h"
#include <opus/opus.h>
#include <stdlib.h>
#include <string.h>

struct OpusEncoderHandle
{
    OpusEncoder *encoder;
    int sample_rate;
    int channels;
};

struct OpusDecoderHandle
{
    OpusDecoder *decoder;
    int sample_rate;
    int channels;
};

OpusEncoderHandle *wrapper_opus_encoder_create(int sample_rate, int channels, int application, int *error)
{
    OpusEncoderHandle *handle = (OpusEncoderHandle *)malloc(sizeof(OpusEncoderHandle));
    if (!handle)
    {
        if (error)
            *error = OPUS_ALLOC_FAIL;
        return NULL;
    }

    int err;
    handle->encoder = opus_encoder_create(sample_rate, channels, application, &err);
    if (err != OPUS_OK)
    {
        free(handle);
        if (error)
            *error = err;
        return NULL;
    }

    handle->sample_rate = sample_rate;
    handle->channels = channels;

    // Set default options for better quality
    opus_encoder_ctl(handle->encoder, OPUS_SET_BITRATE(OPUS_AUTO));
    opus_encoder_ctl(handle->encoder, OPUS_SET_COMPLEXITY(10));            // Max complexity
    opus_encoder_ctl(handle->encoder, OPUS_SET_SIGNAL(OPUS_SIGNAL_VOICE)); // Optimize for voice
    opus_encoder_ctl(handle->encoder, OPUS_SET_APPLICATION(application));

    if (error)
        *error = OPUS_OK;
    return handle;
}

void wrapper_opus_encoder_destroy(OpusEncoderHandle *handle)
{
    if (handle)
    {
        if (handle->encoder)
        {
            opus_encoder_destroy(handle->encoder);
        }
        free(handle);
    }
}

int wrapper_opus_encode(OpusEncoderHandle *handle, const int16_t *pcm, int frame_size,
                        unsigned char *data, int max_data_bytes)
{
    if (!handle || !handle->encoder || !pcm || !data)
    {
        return OPUS_BAD_ARG;
    }

    return opus_encode(handle->encoder, pcm, frame_size, data, max_data_bytes);
}

OpusDecoderHandle *wrapper_opus_decoder_create(int sample_rate, int channels, int *error)
{
    OpusDecoderHandle *handle = (OpusDecoderHandle *)malloc(sizeof(OpusDecoderHandle));
    if (!handle)
    {
        if (error)
            *error = OPUS_ALLOC_FAIL;
        return NULL;
    }

    int err;
    handle->decoder = opus_decoder_create(sample_rate, channels, &err);
    if (err != OPUS_OK)
    {
        free(handle);
        if (error)
            *error = err;
        return NULL;
    }

    handle->sample_rate = sample_rate;
    handle->channels = channels;

    if (error)
        *error = OPUS_OK;
    return handle;
}

void wrapper_opus_decoder_destroy(OpusDecoderHandle *handle)
{
    if (handle)
    {
        if (handle->decoder)
        {
            opus_decoder_destroy(handle->decoder);
        }
        free(handle);
    }
}

int wrapper_opus_decode(OpusDecoderHandle *handle, const unsigned char *data, int data_size,
                        int16_t *pcm, int frame_size, int decode_fec)
{
    if (!handle || !handle->decoder || !pcm)
    {
        return OPUS_BAD_ARG;
    }

    if (data == NULL || data_size <= 0)
    {
        // PLC (Packet Loss Concealment) mode
        return opus_decode(handle->decoder, NULL, 0, pcm, frame_size, decode_fec);
    }

    return opus_decode(handle->decoder, data, data_size, pcm, frame_size, decode_fec);
}

int wrapper_opus_packet_get_samples_per_frame(const unsigned char *data, int sampling_rate)
{
    return opus_packet_get_samples_per_frame(data, sampling_rate);
}

int wrapper_opus_packet_get_nb_channels(const unsigned char *data)
{
    return opus_packet_get_nb_channels(data);
}

int wrapper_opus_packet_get_nb_frames(const unsigned char *packet, int len)
{
    return opus_packet_get_nb_frames(packet, len);
}