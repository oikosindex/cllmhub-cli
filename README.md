# cLLMHub CLI

The command-line interface for [cLLMHub](https://github.com/cllmhub/cllmhub) — publish local LLMs to the cLLMHub network.

## What it does

- **Publish** models from any local inference backend to the hub so anyone with an API key can use them
- **Connect** external backends (Ollama, vLLM, LM Studio, llama.cpp, MLX) to the hub
- **Authenticate** via OAuth device flow, manage credentials

## Quick start

```bash
# Install (pick one)
npm install -g cllmhub          # npm
brew install cllmhub/tap/cllmhub # Homebrew
curl -fsSL https://raw.githubusercontent.com/cllmhub/cllmhub-cli/main/install.sh | sh  # shell script

# Authenticate
cllmhub login

# Publish from an external backend
cllmhub publish -m llama3 -b ollama

# Or discover and select interactively
cllmhub publish
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

### Publishing

#### `cllmhub publish`

Publish models to the cLLMHub network. All publishing goes through the background daemon.

Use flags to specify a model and backend directly, or run without flags for interactive selection from detected backends.

```bash
# Direct publish
cllmhub publish -m llama3-70b -b ollama
cllmhub publish -m mixtral-8x7b -b vllm
cllmhub publish -m my-model -b mlx --api-key sk-xxx

# Interactive selection
cllmhub publish
```

```
Flags:
  --model,          -m   Model name to publish
  --backend,        -b   Backend type: ollama | vllm | lmstudio | llamacpp | mlx (default: ollama)
  --backend-url          Backend endpoint URL (overrides default for the backend type)
  --api-key              API key for the backend server
  --description,    -d   Model description
  --max-concurrent, -c   Maximum concurrent requests (0 = auto-detect, default: 0)
```

#### `cllmhub unpublish <model...>`

Stop serving one or more published models.

```bash
cllmhub unpublish llama3-70b
```

### Daemon

The daemon runs in the background and manages model publishing bridges.

#### `cllmhub start`

Start the cLLMHub daemon.

```bash
cllmhub start
```

#### `cllmhub stop`

Stop the running cLLMHub daemon.

#### `cllmhub status`

Show daemon status, including PID, uptime, and currently published models.

#### `cllmhub logs`

Show daemon logs.

```
Flags:
  --follow, -f   Follow log output
  --lines,  -n   Number of lines to show (default: 50)
```

### Account

#### `cllmhub login`

Authenticate with cLLMHub using OAuth 2.0 device flow. Opens a browser to complete authorization.

#### `cllmhub whoami`

Show the currently logged-in user.

#### `cllmhub logout`

Revoke credentials on the server and remove the local credentials file.

#### `cllmhub update`

Update the CLI to the latest version. The CLI also checks for updates automatically after each command.

## Supported backends

| Backend    | Default endpoint       | Notes |
|------------|------------------------|-------|
| `ollama`   | http://localhost:11434 | Default backend, most common |
| `vllm`     | http://localhost:8000  | High throughput, GPU optimized |
| `lmstudio` | http://localhost:1234  | Desktop app for running local LLMs |
| `llamacpp` | http://localhost:8080  | CPU-friendly, quantized models |
| `mlx`      | http://localhost:8080  | Apple Silicon optimized via mlx-lm |

## License

Apache License 2.0 — see [LICENSE](LICENSE).
