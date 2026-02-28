# cLLMHub CLI

The command-line interface for [cLLMHub](https://github.com/oikosindex/cllmhub) — turn your local LLM into a production API.

## What it does

- **Publish** models from your machine to the hub so anyone with an API key can use them
- **Update** the CLI to the latest version

## Quick start

```bash
# Install
curl -fsSL https://raw.githubusercontent.com/oikosindex/cllmhub-cli/main/install.sh | sh

# Publish a model (requires Ollama or another backend running)
cllmhub publish --model llama3 --backend ollama --token YOUR_PROVIDER_TOKEN
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

### `cllmhub update`

Update the CLI to the latest version.

## Supported backends

| Backend    | Default endpoint       | Notes |
|------------|------------------------|-------|
| `ollama`   | http://localhost:11434 | Default backend, most common |
| `vllm`     | http://localhost:8000  | High throughput, GPU optimized |
| `llamacpp` | http://localhost:8080  | CPU-friendly, quantized models |
| `custom`   | (user-specified)       | Any OpenAI-compatible HTTP server |

## License

Apache License 2.0 — see [LICENSE](LICENSE).
