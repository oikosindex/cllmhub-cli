# cLLMHub CLI

The command-line interface for [cLLMHub](https://github.com/oikosindex/cllmhub) — turn your local LLM into a production API.

## What it does

- **Publish** models from your machine to the hub so anyone with an API key can use them
- **Ask** a single question and get a response
- **Chat** interactively with any model on the hub
- **List** all available models

## Quick start

```bash
# Download (macOS Apple Silicon example)
curl -fSL https://your-hub-url/downloads/cllmhub-darwin-arm64 -o cllmhub
chmod +x cllmhub

# Publish a model (requires Ollama or another backend running)
./cllmhub publish --model llama3 --backend ollama --token YOUR_PROVIDER_TOKEN

# Ask a question
./cllmhub ask -m llama3 "What is quantum computing?"

# Start a chat session
./cllmhub chat -m llama3
```

## Installation

### Pre-built binaries

Download from your hub's Settings > Downloads page. Available for:

| Platform | Architecture |
|----------|-------------|
| macOS    | Apple Silicon (arm64), Intel (amd64) |
| Linux    | x86_64 (amd64), ARM64 |
| Windows  | x86_64 (amd64) |

### Build from source

Requires Go 1.22+.

```bash
git clone https://github.com/oikosindex/cllmhub-cli.git
cd cllmhub-cli
make build
# Binary is at bin/cllmhub
```

Cross-compile for all platforms:

```bash
make build-all
```

## Commands

### `cllmhub publish`

Publish a local model to the hub. Keeps a persistent connection — your model is online as long as the CLI is running.

```
Flags:
  --model,   -m   Model name to publish (required)
  --backend, -b   Backend type: ollama | vllm | llamacpp | custom (default: ollama)
  --token,   -t   Provider token from your dashboard (required)
  --hub-url       Hub gateway URL (default: https://cllmhub.com)
```

### `cllmhub ask`

Send a single prompt and get a response.

```
Flags:
  --model,       -m   Model to query (required)
  --temperature, -t   Sampling temperature (default: 0.7)
  --max-tokens        Max response tokens (default: 512)
  --stream,      -s   Stream tokens as they arrive
  --hub-url            Hub gateway URL
```

### `cllmhub chat`

Interactive chat session. Type `exit` to quit.

```
Flags:
  --model, -m   Model to chat with (required)
  --hub-url     Hub gateway URL
```

### `cllmhub models`

List all available models on the hub.

### `cllmhub status`

Check connectivity to the hub.

## Supported backends

| Backend    | Default endpoint       | Notes |
|------------|------------------------|-------|
| `ollama`   | http://localhost:11434 | Default backend, most common |
| `vllm`     | http://localhost:8000  | High throughput, GPU optimized |
| `llamacpp` | http://localhost:8080  | CPU-friendly, quantized models |
| `custom`   | (user-specified)       | Any OpenAI-compatible HTTP server |

## License

Apache License 2.0 — see [LICENSE](LICENSE).
