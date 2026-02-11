package core

type VideoFormat string

const (
	VideoFormatMP4 VideoFormat = "mp4"
	VideoFormatAVI VideoFormat = "avi"
	VideoFormatMKV VideoFormat = "mkv"
)

type VideoChunk struct {
	Data       *[]byte     // Raw video data.
	FrameRate  int         // Frame rate of the video data.
	Resolution string      // Resolution of the video data (e.g., "1920x1080").
	Format     VideoFormat // Format of the video data.
}
