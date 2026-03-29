# cLLMHub CLI

The command-line interface for [cLLMHub](https://github.com/cllmhub/cllmhub) — publish local LLMs to the cLLMHub network.

## Install

```bash
npm install -g cllmhub
```

Or run directly without installing:

```bash
npx cllmhub --help
```

The npm package automatically downloads the correct pre-built binary for your platform (macOS, Linux, Windows).

## Quick start

```bash
# Authenticate
cllmhub login

# Publish from an external backend
cllmhub publish -m llama3 -b ollama

# Or discover and select interactively
cllmhub publish
```

## Commands

### Publishing

#### `cllmhub publish`

Publish models to the cLLMHub network. Use flags to specify a model and backend directly, or run without flags for interactive selection from detected backends.

```bash
cllmhub publish -m llama3-70b -b ollama
cllmhub publish -m mixtral-8x7b -b vllm
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

### Daemon

#### `cllmhub start`

Start the cLLMHub daemon.

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

Authenticate with cLLMHub using OAuth 2.0 device flow.

#### `cllmhub whoami`

Show the currently logged-in user.

#### `cllmhub logout`

Revoke credentials on the server and remove the local credentials file.

#### `cllmhub update`

Update the CLI to the latest version.

## Supported backends

| Backend    | Default endpoint       | Notes |
|------------|------------------------|-------|
| `ollama`   | http://localhost:11434 | Default backend, most common |
| `vllm`     | http://localhost:8000  | High throughput, GPU optimized |
| `lmstudio` | http://localhost:1234  | Desktop app for running local LLMs |
| `llamacpp` | http://localhost:8080  | CPU-friendly, quantized models |
| `mlx`      | http://localhost:8080  | Apple Silicon optimized via mlx-lm |

## Other installation methods

- **Homebrew:** `brew install cllmhub/tap/cllmhub`
- **Shell script:** `curl -fsSL https://raw.githubusercontent.com/cllmhub/cllmhub-cli/main/install.sh | sh`
- **Pre-built binaries:** [GitHub Releases](https://github.com/cllmhub/cllmhub-cli/releases)

## License

Apache License 2.0 — see [LICENSE](https://github.com/cllmhub/cllmhub-cli/blob/main/LICENSE).
