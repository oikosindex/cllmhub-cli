# cLLMHub CLI

The command-line interface for [cLLMHub](https://github.com/cllmhub/cllmhub) — turn your local LLM into a production API.

## What it does

- **Login** to your cLLMHub account via OAuth device flow
- **Publish** models from your machine to the hub so anyone with an API key can use them
- **Whoami** to see the currently logged-in user
- **Logout** to revoke credentials
- **Update** the CLI to the latest version

## Quick start

```bash
# Install (pick one)
npm install -g cllmhub          # npm
brew install cllmhub/tap/cllmhub # Homebrew
curl -fsSL https://raw.githubusercontent.com/cllmhub/cllmhub-cli/main/install.sh | sh  # shell script

# Authenticate
cllmhub login

# Publish a model (interactive — picks from local backends)
cllmhub publish

# Or specify the model directly
cllmhub publish -m llama3 -b ollama
```

## Installation

### npm / npx

```bash
# Install globally
npm install -g cllmhub

# Or run without installing
npx cllmhub --help
```

The npm package automatically downloads the correct pre-built binary for your platform on install.

### Homebrew

```bash
brew tap cllmhub/tap
brew install cllmhub
```

### Pre-built binaries

Download from your hub's Settings > Downloads page, or grab from [GitHub Releases](https://github.com/cllmhub/cllmhub-cli/releases). Available for:

| Platform | Architecture |
|----------|-------------|
| macOS    | Apple Silicon (arm64), Intel (amd64) |
| Linux    | x86_64 (amd64), ARM64 |
| Windows  | x86_64 (amd64) |

### Install script

```bash
curl -fsSL https://raw.githubusercontent.com/cllmhub/cllmhub-cli/main/install.sh | sh
```

### Build from source

Requires Go 1.22+.

```bash
git clone https://github.com/cllmhub/cllmhub-cli.git
cd cllmhub-cli
make build
# Binary is at bin/cllmhub
```

Cross-compile for all platforms:

```bash
make build-all
```

## Commands

### `cllmhub login`

Authenticate with cLLMHub using OAuth 2.0 device flow. Opens a browser to complete authorization.

After login, the CLI discovers models from local backends and lets you select one to publish immediately.

### `cllmhub whoami`

Show the currently logged-in user.

### `cllmhub publish`

Publish a local model to the hub. Keeps a persistent WebSocket connection — your model is online as long as the CLI is running.

When run without `-m`, it discovers models from local backends (Ollama, vLLM, LM Studio) and lets you pick one interactively using arrow keys.

**Features:**
- Auto-reconnect on WebSocket disconnect (up to 5 retries)
- Model server health monitoring
- Heartbeat to keep your provider registered on the hub
- Rate limiting and concurrency control
- Request audit logging

```
Flags:
  --model,          -m   Model name to publish (omit for interactive selection)
  --backend,        -b   Backend type: ollama | vllm | lmstudio | llamacpp | custom (default: ollama)
  --backend-url          Backend endpoint URL (overrides default for the backend type)
  --description,    -d   Model description (max 500 chars)
  --max-concurrent, -c   Maximum concurrent requests (default: 1)
  --log-file             Path to audit log file (JSON lines)
  --rate-limit           Max requests per minute (0 = unlimited)
```

### `cllmhub logout`

Revoke credentials on the server and remove the local credentials file.

### `cllmhub update`

Update the CLI to the latest version. The CLI also checks for updates automatically after each command.

## Supported backends

| Backend    | Default endpoint       | Notes |
|------------|------------------------|-------|
| `ollama`   | http://localhost:11434 | Default backend, most common |
| `vllm`     | http://localhost:8000  | High throughput, GPU optimized |
| `lmstudio` | http://localhost:1234  | Desktop app for running local LLMs |
| `llamacpp` | http://localhost:8080  | CPU-friendly, quantized models |
| `custom`   | (user-specified)       | Any OpenAI-compatible HTTP server |

## License

Apache License 2.0 — see [LICENSE](LICENSE).
