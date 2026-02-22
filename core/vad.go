package core

type VADResult struct {
	Confidence float32
	Ready      bool // True when enough audio has been buffered to produce a valid result
}
