# InteractKit

A Go framework for building real-time voice AI agents. It connects live audio from callers through a full speech pipeline — VAD, STT, LLM, TTS — and streams synthesized speech back, all in real time.

## Architecture

Audio flows through an ordered handler pipeline:

```
TransportInput → VAD → STT → UserContext → LLM → AssistantContext → TTS → ActivityControl → TransportOutput
```

| Handler | Role |
|---|---|
| **TransportInput/Output** | Receives and sends audio via WebRTC or WebSocket |
| **VAD** | Silero ONNX model detects speech vs. silence |
| **STT** | Streams audio to speech-to-text, emits transcripts |
| **UserContextAggregator** | Appends user messages to LLM context |
| **LLM** | Runs completion with tool calling support |
| **AssistantContextAggregator** | Appends assistant messages, executes tool handlers |
| **TTS** | Converts text chunks to PCM audio |
| **ActivityControl** | Gates audio delivery, handles interruption logic |

Each handler runs in its own goroutine. Events flow left-to-right through unbuffered channels, or can be sent to the top of the pipeline to propagate through all handlers.

## Supported Providers

| Category | Providers |
|---|---|
| **Transport** | LiveKit, Daily, Twilio |
| **STT** | Deepgram |
| **LLM** | OpenAI |
| **TTS** | Deepgram, ElevenLabs, Cartesia |
| **VAD** | Silero (ONNX, with RNNoise denoising) |

## Key Features

- **Filler generation** — Concurrently generates conversational fillers ("Let me check...") while the real LLM response is being produced. Fillers are prepended if the real response is slow.
- **Interruption handling** — When VAD detects speech while the bot is talking, audio delivery is suspended. If STT confirms real speech, the interruption is committed; otherwise buffered audio resumes.
- **Pre-emptive generation** — LLM starts generating before the user fully finishes speaking.
- **Tool calling** — LLM can invoke registered tools; results are injected into context and generation re-triggers.
- **Task groups** — Structured conversation flows with automatic variable capture, validation, and branching. See [TASKS.md](TASKS.md).
- **Session API** — Instead of static config, point to an HTTP endpoint that returns per-caller session configuration.
- **Backup services** — Every handler supports ordered fallback services.
- **External event handler** — WebSocket sidecar (port 19304) broadcasts pipeline events to external consumers.

## Quick Start

### Prerequisites

- Go 1.24+
- CGO dependencies: `libsamplerate`, `libopus`, `libsoxr`, `librnnoise`
- [ONNX Runtime](https://github.com/microsoft/onnxruntime/releases) shared library
- [Silero VAD ONNX model](https://github.com/snakers4/silero-vad)

### Environment Variables

Create a `.env.local` file:

```env
# Transport (pick one)
LIVEKIT_URL=wss://your-livekit-server
LIVEKIT_API_KEY=...
LIVEKIT_API_SECRET=...
# DAILY_API_KEY=...

# AI services
DEEPGRAM_API_KEY=...
OPENAI_API_KEY=...
# ELEVENLABS_API_KEY=...
# CARTESIA_API_KEY=...

# Runtime paths
SILERO_MODEL_PATH=./external/models/silero_vad.onnx
ONNX_RUNTIME_PATH=./external/onnx/libonnxruntime.so
```

### Configuration

Edit `settings.json` to configure the pipeline:

```json
{
  "transport": {
    "livekit": {}
  },
  "session_config": {
    "stt": {
      "service": { "deepgram": { "interim_results": true, "punctuate": true, "smart_format": true } }
    },
    "llm": {
      "handler": { "allow_tool_calls": true, "pre_emptive_generation": true, "generate_fillers": true },
      "service": { "openai": { "streaming": true } }
    },
    "tts": {
      "service": { "deepgram": {} }
    },
    "context": {
      "manager": { "allow_continue_listening": true, "human_like_speech": true },
      "handler": {
        "context": {
          "messages": [{ "role": "system", "message": "You are a helpful assistant." }],
          "tools": []
        }
      }
    }
  }
}
```

To use a different TTS provider, replace the `tts.service` key:

```json
"service": { "elevenlabs": { "voice_id": "...", "model_id": "eleven_turbo_v2_5" } }
```
```json
"service": { "cartesia": { "voice_id": "...", "model": "sonic-2" } }
```

To use Daily instead of LiveKit:

```json
"transport": { "daily": {} }
```

### Run Directly

```bash
CGO_ENABLED=1 go build -o interactkit .
./interactkit
```

### Docker

The Dockerfile is a 3-stage build:

1. **Node.js stage** — Builds the Next.js management UI into static files
2. **Go stage** — Compiles the agent binary and UI server, downloads ONNX Runtime + Silero model, builds RNNoise from source
3. **Runtime stage** — Minimal Debian image with both binaries and all dependencies

```bash
docker build -t interactkit .
docker run -p 8888:8888 --env-file .env.local interactkit
```

The container entrypoint is the UI server (port 8888), which manages the agent process lifecycle.

## Management UI

A web dashboard served at port 8888 for configuring and monitoring the agent:

- **Provider** — Configure transport (LiveKit/Daily) connection settings
- **API Keys** — Manage service API keys (stored in `.env.local`, never in settings.json)
- **Session Config** — Configure STT, LLM, TTS, context, and task groups
- **Session API** — Point to an external endpoint for dynamic per-call configuration
- **Logs** — Live session log viewer with SSE streaming, level filtering, and search
- **Raw JSON** — Direct settings.json editor

Built with Next.js 14, Tailwind CSS v4, and shadcn/ui. Compiled to a static export during Docker build — no Node.js runtime needed.

### UI API Endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/api/settings` | GET / PUT | Read or write settings.json |
| `/api/keys` | GET / PUT | Read or write API keys |
| `/api/restart` | POST | Restart the agent process |
| `/api/status` | GET | Agent process status and PID |
| `/api/sessions` | GET | List all session logs |
| `/api/sessions/{id}/logs` | GET (SSE) | Stream session log entries |

Protect with `INTERACTKIT_API_TOKEN` environment variable for Bearer token auth.

## Project Structure

```
├── main.go                    # Entry point, pipeline wiring
├── settings.json              # Default configuration
├── Dockerfile                 # 3-stage build
├── core/                      # Runner, handler interface, events, logging
├── handlers/
│   ├── transport/             # Audio I/O handler
│   ├── vad/                   # Voice activity detection handler
│   ├── stt/                   # Speech-to-text handler
│   ├── llm/                   # LLM handler (fillers, tool calls)
│   ├── tts/                   # Text-to-speech handler
│   ├── context/               # Context aggregators, task groups
│   └── activity_control/      # Interruption gating
├── services/
│   ├── deepgram/              # Deepgram STT + TTS
│   ├── openai/                # OpenAI LLM
│   ├── elevenlabs/            # ElevenLabs TTS
│   └── cartesia/              # Cartesia TTS
├── transports/
│   ├── livekit/               # LiveKit WebRTC transport
│   ├── daily/                 # Daily.co transport
│   └── twilio/                # Twilio Media Streams transport
├── vad/silero/                # Silero VAD ONNX inference
├── factories/                 # Config parsing, handler construction
├── utils/audio/               # PCM conversion, resampling, denoising
└── ui/                        # Next.js management dashboard
    ├── server/                # Go HTTP server (serves UI + API)
    └── src/                   # React app (Tailwind + shadcn)
```

## License

MIT License. See [LICENSE](LICENSE) for details.
