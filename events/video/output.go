package video

import "interactkit/core"

type VideoOutputEvent struct {
	VideoChunk core.VideoChunk
}

func (e *VideoOutputEvent) GetId() string {
	return "video.output"
}
