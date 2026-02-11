package audio

/*
#include <stdlib.h>
#include "audio.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

// ResamplePCMBytes resamples PCM using C implementation
func ResamplePCMBytes(pcm []byte, numChannels, oldRate, newRate int) ([]byte, error) {
	if len(pcm) == 0 {
		return nil, errors.New("empty PCM")
	}

	var outLen C.size_t
	outPtr := C.resample_pcm_bytes(
		(*C.uint8_t)(unsafe.Pointer(&pcm[0])),
		C.size_t(len(pcm)),
		C.int(numChannels),
		C.int(oldRate),
		C.int(newRate),
		&outLen,
	)

	if outPtr == nil {
		return nil, errors.New("resampling failed")
	}
	defer C.free(unsafe.Pointer(outPtr))

	return C.GoBytes(unsafe.Pointer(outPtr), C.int(outLen)), nil
}
