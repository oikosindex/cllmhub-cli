# cLLMHub CLI

The command-line interface for [cLLMHub](https://github.com/cllmhub/cllmhub) — turn your local LLM into a production API.

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

# Download and publish a Hugging Face model
cllmhub models --search mistral
cllmhub download TheBloke/Mistral-7B-v0.1-GGUF
cllmhub publish Mistral-7B-v0.1

# Or publish from an external backend
cllmhub publish -m llama3 -b ollama
```

## Commands

### Model management

#### `cllmhub models`

List downloaded models, or search Hugging Face for GGUF models.

```bash
cllmhub models                    # List downloaded models
cllmhub models --search mistral   # Search Hugging Face
```

```
Flags:
  --search, -s   Search Hugging Face for GGUF models
```

#### `cllmhub download <repo...>`

Download GGUF model files from Hugging Face repositories. Lists available GGUF files and lets you pick which quantization to download.

For faster downloads and access to gated models, pass a Hugging Face token with `--hf-token` (it will be saved for future use). Without a token, downloads may be slower and rate-limited.

```bash
cllmhub download TheBloke/Mistral-7B-v0.1-GGUF
cllmhub download --hf-token <token> TheBloke/Mistral-7B-v0.1-GGUF
```

```
Flags:
  --hf-token   Hugging Face token (saved for future use)
```

#### `cllmhub delete <model...>`

Delete one or more downloaded models. Prevents deletion of currently published models.

```bash
cllmhub delete mistral-7b
cllmhub delete m1 m2   # Use aliases
```

### Daemon

#### `cllmhub start`

Start the cLLMHub daemon with hardware auto-detection (Apple Silicon, NVIDIA GPU, CPU).

```bash
cllmhub start                                          # Auto-detect everything
cllmhub start --ctx-size 8192 --flash-attn --slots 2   # Custom settings
```

```
Flags:
  --ctx-size       Context size for inference (0 = auto-detect)
  --flash-attn     Enable flash attention (auto-enabled on Apple Silicon/NVIDIA)
  --slots          Number of concurrent inference slots (0 = auto-detect)
  --n-gpu-layers   Number of layers to offload to GPU (-1 = auto, 0 = CPU only)
  --batch-size     Batch size for prompt processing (0 = auto-detect)
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

### Publishing

#### `cllmhub publish`

Publish models to the cLLMHub network. Supports two modes:

**Daemon mode** — publish downloaded GGUF models via the daemon:

```bash
cllmhub publish llama3-8b mistral-7b
```

**Foreground mode** — connect to an external inference backend:

```bash
cllmhub publish -m llama3-70b -b ollama
cllmhub publish -m mixtral-8x7b -b vllm
```

```
Flags (foreground mode):
  --model,          -m   Model name to publish
  --backend,        -b   Backend type: ollama | vllm | lmstudio | llamacpp | mlx (default: ollama)
  --backend-url          Backend endpoint URL (overrides default for the backend type)
  --max-concurrent, -c   Maximum concurrent requests (default: 1)
  --log-file             Path to audit log file (JSON lines)
  --rate-limit           Max requests per minute (0 = unlimited)
```

#### `cllmhub unpublish <model...>`

Stop serving one or more published models. The models remain downloaded locally.

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

For foreground-mode publishing (`cllmhub publish -m <model> -b <backend>`):

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
