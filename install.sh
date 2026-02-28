#!/bin/sh
set -e

REPO="oikosindex/cllmhub-cli"
BINARY="cllmhub"
INSTALL_DIR="${HOME}/.cllmhub/bin"

main() {
    os=$(detect_os)
    arch=$(detect_arch)

    echo "Detected platform: ${os}/${arch}"

    mkdir -p "$INSTALL_DIR"

    # Try go install first
    if command -v go >/dev/null 2>&1; then
        echo "Go found, installing via go install..."
        GOBIN="$INSTALL_DIR" go install "github.com/${REPO}/cmd/${BINARY}@latest"
    else
        # Fall back to pre-built binary
        echo "Downloading pre-built binary..."
        download_binary "$os" "$arch"
    fi

    setup_path
    echo ""
    echo "cllmhub installed successfully!"
    echo ""
    echo "Run 'cllmhub --help' to get started."
    echo ""
    echo "If the command is not found, restart your terminal or run:"
    echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
}

download_binary() {
    os=$1
    arch=$2

    version=$(get_latest_version)
    if [ -z "$version" ]; then
        echo "Error: could not determine latest version." >&2
        exit 1
    fi
    echo "Latest version: ${version}"

    filename="${BINARY}-${os}-${arch}"
    if [ "$os" = "windows" ]; then
        filename="${filename}.exe"
    fi

    url="https://github.com/${REPO}/releases/download/${version}/${filename}"
    tmpdir=$(mktemp -d)
    tmpfile="${tmpdir}/${BINARY}"

    echo "Downloading ${url}..."
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -o "$tmpfile" "$url"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$tmpfile" "$url"
    else
        echo "Error: curl or wget is required." >&2
        exit 1
    fi

    chmod +x "$tmpfile"
    mv "$tmpfile" "${INSTALL_DIR}/${BINARY}"
    rm -rf "$tmpdir"
}

setup_path() {
    path_entry="export PATH=\"${INSTALL_DIR}:\$PATH\""

    # Already in PATH, nothing to do
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*) return ;;
    esac

    # Detect shell profile
    profiles=""
    case "$(basename "${SHELL:-}")" in
        zsh)  profiles="$HOME/.zshrc" ;;
        bash)
            if [ -f "$HOME/.bash_profile" ]; then
                profiles="$HOME/.bash_profile"
            else
                profiles="$HOME/.bashrc"
            fi
            ;;
        fish) ;;
        *)
            if [ -f "$HOME/.profile" ]; then
                profiles="$HOME/.profile"
            fi
            ;;
    esac

    # Handle fish separately
    if [ "$(basename "${SHELL:-}")" = "fish" ]; then
        fish_conf_dir="${HOME}/.config/fish/conf.d"
        mkdir -p "$fish_conf_dir"
        fish_file="${fish_conf_dir}/cllmhub.fish"
        if [ ! -f "$fish_file" ] || ! grep -q "$INSTALL_DIR" "$fish_file" 2>/dev/null; then
            echo "set -gx PATH ${INSTALL_DIR} \$PATH" > "$fish_file"
            echo "Added ${INSTALL_DIR} to PATH in ${fish_file}"
        fi
        return
    fi

    # Add to shell profile(s)
    for profile in $profiles; do
        if [ -f "$profile" ]; then
            if grep -q "$INSTALL_DIR" "$profile" 2>/dev/null; then
                continue
            fi
        fi
        echo "" >> "$profile"
        echo "# cLLMHub CLI" >> "$profile"
        echo "$path_entry" >> "$profile"
        echo "Added ${INSTALL_DIR} to PATH in ${profile}"
    done
}

detect_os() {
    case "$(uname -s)" in
        Linux*)  echo "linux" ;;
        Darwin*) echo "darwin" ;;
        MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
        *) echo "Error: unsupported OS $(uname -s)" >&2; exit 1 ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *) echo "Error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
    esac
}

get_latest_version() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed 's/.*"tag_name": *"//;s/".*//'
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed 's/.*"tag_name": *"//;s/".*//'
    fi
}

main
