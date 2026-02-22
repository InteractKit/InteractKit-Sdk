package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"interactkit/controlplane"
	"interactkit/core"
	"interactkit/factories"
	_ "net/http/pprof"
	"os"
	"strconv"
	"time"

	activitycontrol "interactkit/handlers/activity_control"
	"interactkit/handlers/transport"
	"interactkit/handlers/vad"

	silerovad "interactkit/vad/silero"

	"github.com/joho/godotenv"
)

func main() {
	var connectURL string
	flag.StringVar(&connectURL, "connect", "", "WebSocket URL of UI control plane (e.g. ws://ui:8888/ws/agent)")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if connectURL != "" {
		runConnectedMode(ctx, cancel, connectURL)
	} else {
		// Standalone mode: load config from files, same as before.
		if err := godotenv.Load(".env.local"); err != nil {
			core.GetLogger().With(map[string]any{"error": err}).Warn("No .env.local file found or failed to load")
		}
		settings, apiKeys := loadSettingsFromEnv()
		runWorkerMode(ctx, settings, apiKeys)
	}

	<-ctx.Done()
	core.GetLogger().Info("Shutting down...")
	time.Sleep(2 * time.Second)
}

// runConnectedMode connects to the UI control plane via WebSocket, receives
// configuration, and runs the worker. The agent dies when the WS drops.
func runConnectedMode(ctx context.Context, cancel context.CancelFunc, connectURL string) {
	logger := core.GetLogger().With(map[string]any{"component": "connected"})

	agentID := os.Getenv("AGENT_ID")
	if agentID == "" {
		hostname, _ := os.Hostname()
		agentID = hostname
	}

	client := controlplane.NewClient(controlplane.ClientConfig{
		ConnectURL: connectURL,
		AgentID:    agentID,
		Version:    "1.0.0",
		Metadata: map[string]string{
			"hostname": func() string { h, _ := os.Hostname(); return h }(),
		},
		Logger: logger,
	})

	client.OnShutdown = func(reason string) {
		logger.With(map[string]any{"reason": reason}).Info("shutdown requested by control plane")
		cancel()
	}

	client.OnConfigUpdate = func(settings json.RawMessage, keys map[string]string) {
		logger.Info("received config update from control plane (hot-reload not yet implemented)")
	}

	client.OnRestartPipeline = func() {
		logger.Info("pipeline restart requested by control plane (not yet implemented)")
	}

	if err := client.Connect(ctx); err != nil {
		logger.With(map[string]any{"error": err}).Error("failed to connect to control plane")
		cancel()
		return
	}

	// In connected mode, config can come from env vars set by the spawner
	// at container creation time. The spawner passes settings as env vars.
	settings, apiKeys := loadSettingsFromEnv()
	runWorkerMode(ctx, settings, apiKeys)

	// When the control plane connection drops, the agent shuts down.
	go func() {
		client.Wait()
		logger.Info("control plane connection lost, shutting down")
		cancel()
	}()
}

// loadSettingsFromEnv loads SettingsConfig from file or SETTINGS_JSON_B64 env var, and API keys from env vars.
func loadSettingsFromEnv() (factories.SettingsConfig, factories.APIKeys) {
	var settings factories.SettingsConfig
	var err error

	if b64 := os.Getenv("SETTINGS_JSON_B64"); b64 != "" {
		data, decErr := base64.StdEncoding.DecodeString(b64)
		if decErr != nil {
			core.GetLogger().With(map[string]any{"error": decErr}).Error("failed to decode SETTINGS_JSON_B64")
			settings = factories.DefaultSettingsConfig()
		} else {
			settings, err = factories.SettingsConfigFromJSON(data)
			if err != nil {
				core.GetLogger().With(map[string]any{"error": err}).Error("failed to parse SETTINGS_JSON_B64")
				settings = factories.DefaultSettingsConfig()
			} else {
				core.GetLogger().Info("loaded settings from SETTINGS_JSON_B64")
			}
		}
	} else {
		settingsPath := getEnv("SETTINGS_PATH", "./settings.json")
		settings, err = factories.SettingsConfigFromFile(settingsPath)
		if err != nil {
			core.GetLogger().With(map[string]any{"path": settingsPath, "error": err}).Warn("failed to load settings, using defaults")
			settings = factories.DefaultSettingsConfig()
		}
	}
	settings.Transport.InjectProviderKeys(factories.ProviderKeys{
		LiveKitURL:       os.Getenv("LIVEKIT_URL"),
		LiveKitAPIKey:    os.Getenv("LIVEKIT_API_KEY"),
		LiveKitAPISecret: os.Getenv("LIVEKIT_API_SECRET"),
		DailyAPIKey:      os.Getenv("DAILY_API_KEY"),
	})

	apiKeys := factories.APIKeys{
		Deepgram:   getEnv("DEEPGRAM_API_KEY", ""),
		OpenAI:     getEnv("OPENAI_API_KEY", ""),
		Together:   getEnv("TOGETHER_API_KEY", ""),
		Groq:       getEnv("GROQ_API_KEY", ""),
		DeepSeek:   getEnv("DEEPSEEK_API_KEY", ""),
		OpenRouter: getEnv("OPENROUTER_API_KEY", ""),
		Fireworks:  getEnv("FIREWORKS_API_KEY", ""),
		Cerebras:   getEnv("CEREBRAS_API_KEY", ""),
		XAI:        getEnv("XAI_API_KEY", ""),
		Mistral:    getEnv("MISTRAL_API_KEY", ""),
		Perplexity: getEnv("PERPLEXITY_API_KEY", ""),
		ElevenLabs: getEnv("ELEVENLABS_API_KEY", ""),
		Cartesia:   getEnv("CARTESIA_API_KEY", ""),
	}

	return settings, apiKeys
}

// runWorkerMode starts the agent using the configured TransportProvider pattern.
func runWorkerMode(ctx context.Context, settings factories.SettingsConfig, apiKeys factories.APIKeys) {
	logger := core.GetLogger().With(map[string]any{"component": "worker"})
	logger.Info("starting in worker mode")

	timeoutSeconds := getEnvAsInt("WORKER_TIMEOUT_SECONDS", 3000)

	worker, err := settings.Transport.GetProvider(logger)
	if err != nil {
		logger.With(map[string]any{"error": err}).Error("failed to create worker")
		return
	}

	pipeline := factories.NewPipeline(func(svc transport.ITransportService, jobCtx context.Context) ([]core.IHandler, error) {
		// Use per-session logger if injected by the transport provider.
		sessionLogger := core.SessionLoggerFromContext(jobCtx)
		if sessionLogger == nil {
			sessionLogger = logger
		}

		// --- Transport ---
		transportWrapper := transport.NewTransportHandlerWrapper(svc, transport.DefaultConfig(), sessionLogger)

		// --- VAD (Silero — not part of SessionConfig) ---
		vadServiceConfig := silerovad.DefaultConfig()
		vadServiceConfig.OnnxPath = getEnv("SILERO_MODEL_PATH", vadServiceConfig.OnnxPath)
		vadServiceConfig.OnnxRuntimePath = getEnv("ONNX_RUNTIME_PATH", vadServiceConfig.OnnxRuntimePath)
		vadService, err := silerovad.NewSileroVadService(vadServiceConfig, sessionLogger)
		if err != nil {
			return nil, err
		}
		vadHandler := vad.NewVADHandler(vadService, vad.DefaultConfig(), sessionLogger)

		// --- Session (STT + LLM + TTS + Context) ---
		var sessionCfg factories.SessionConfig
		switch {
		case settings.SessionAPI != nil:
			sessionCfg, err = settings.SessionAPI.Fetch()
			if err != nil {
				sessionLogger.With(map[string]any{"error": err}).Error("failed to fetch session config from API, ending session")
				return nil, fmt.Errorf("session api fetch failed: %w", err)
			}
		case settings.Session != nil:
			sessionCfg = *settings.Session
		default:
			return nil, fmt.Errorf("no session config: set session_config or session_api in settings.json")
		}
		sessionCfg.InjectAPIKeys(apiKeys)
		handlers, err := sessionCfg.BuildHandlers(sessionLogger)
		if err != nil {
			return nil, err
		}

		// Register globally available tools before loading the task group.
		handlers.ContextManager.RegisterTool(core.LLMTool{
			ToolId:      "transfer_to_human",
			Name:        "transfer_to_human",
			Description: "Transfer the caller to a live human agent. Use only when the caller explicitly requests it or the situation is urgent and cannot be handled by AI.",
			Parameters: []core.Parameter{
				{
					Name:        "reason",
					Description: "Brief reason for the transfer.",
					Required:    true,
					Type:        core.LLMParameterTypeString,
				},
			},
		}, func(call core.LLMToolCall) string {
			reason := "unspecified"
			if call.Parameters != nil {
				if v, ok := (*call.Parameters)["reason"]; ok {
					reason = v.(string)
				}
			}
			sessionLogger.With(map[string]any{"reason": reason}).Info("transfer_to_human requested")
			return "Transfer initiated. A live agent will be with the caller shortly."
		})

		// --- Activity control ---
		activityControlHandler := activitycontrol.NewActivityControlHandler(activitycontrol.DefaultConfig(), sessionLogger)

		// TransportInput → VAD → STT → UserContext → LLM → AssistantContext → TTS → ActivityControl → TransportOutput
		return []core.IHandler{
			transportWrapper.GetInputHandler(),
			vadHandler,
			handlers.STT,
			handlers.ContextManager.GetUserContextAggregator(),
			handlers.LLM,
			handlers.ContextManager.GetAssistantContextAggregator(),
			handlers.TTS,
			activityControlHandler,
			transportWrapper.GetOutputHandler(),
		}, nil
	}, factories.PipelineConfig{
		Timeout: time.Duration(timeoutSeconds) * time.Second,
	}, logger)

	pipeline.Serve(worker, ctx)
}

// getEnv gets an environment variable with a default fallback
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsInt gets an environment variable as integer with a default fallback
func getEnvAsInt(key string, defaultValue int) int {
	valStr := getEnv(key, "")
	if valStr == "" {
		return defaultValue
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return defaultValue
	}
	return val
}
