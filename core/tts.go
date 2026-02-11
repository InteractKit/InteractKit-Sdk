package core

type TTSContext struct {
	PreviousAudio        []AudioChunk // Previously generated audio chunks. [limited to last N chunks for context]
	ConversationMessages []LLMMessage // Messages exchanged in the conversation so far.
}
