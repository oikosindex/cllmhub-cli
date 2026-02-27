#!/bin/sh
set -e

REPO="oikosindex/cllmhub-cli"
BINARY="cllmhub"
INSTALL_DIR="/usr/local/bin"

main() {
    os=$(detect_os)
    arch=$(detect_arch)

    echo "Detected platform: ${os}/${arch}"

    # Try go install first
    if command -v go >/dev/null 2>&1; then
        echo "Go found, installing via go install..."
        go install "github.com/${REPO}/cmd/${BINARY}@latest"
        echo ""
        echo "Installed ${BINARY} to $(go env GOPATH)/bin/${BINARY}"
        echo "Make sure $(go env GOPATH)/bin is in your PATH."
        exit 0
    fi

    # Fall back to pre-built binary
    echo "Go not found, downloading pre-built binary..."

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

    # Install to INSTALL_DIR, use sudo if needed
    if [ -w "$INSTALL_DIR" ]; then
        mv "$tmpfile" "${INSTALL_DIR}/${BINARY}"
    else
        echo "Installing to ${INSTALL_DIR} (requires sudo)..."
        sudo mv "$tmpfile" "${INSTALL_DIR}/${BINARY}"
    fi

    rm -rf "$tmpdir"

    echo ""
    echo "${BINARY} ${version} installed to ${INSTALL_DIR}/${BINARY}"
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
