class Cllmhub < Formula
  desc "Turn your local LLM into a production API"
  homepage "https://github.com/cllmhub/cllmhub-cli"
  version "0.4.12"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/cllmhub/cllmhub-cli/releases/download/v#{version}/cllmhub-darwin-arm64"
      sha256 "150fef565045fd3698bbcee8d969a6d280aadabfb4fe40f30a17672644d4dec3"
    else
      url "https://github.com/cllmhub/cllmhub-cli/releases/download/v#{version}/cllmhub-darwin-amd64"
      sha256 "150fef565045fd3698bbcee8d969a6d280aadabfb4fe40f30a17672644d4dec3"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/cllmhub/cllmhub-cli/releases/download/v#{version}/cllmhub-linux-arm64"
      sha256 "150fef565045fd3698bbcee8d969a6d280aadabfb4fe40f30a17672644d4dec3"
    else
      url "https://github.com/cllmhub/cllmhub-cli/releases/download/v#{version}/cllmhub-linux-amd64"
      sha256 "150fef565045fd3698bbcee8d969a6d280aadabfb4fe40f30a17672644d4dec3"
    end
  end

  def install
    binary = Dir.glob("cllmhub-*").first || "cllmhub"
    bin.install binary => "cllmhub"
  end

  test do
    assert_match "cllmhub", shell_output("#{bin}/cllmhub --version")
  end
end
