class Envmove < Formula
  desc "Carry the project context git refuses to: .env, handover docs, agent state"
  homepage "https://github.com/atalayhuryasar/envmove"
  url "https://github.com/atalayhuryasar/envmove/archive/refs/heads/main.tar.gz"
  version "0.1.0"
  license "MIT"

  # envmove is a plain Go binary with no cgo and no runtime dependencies beyond git,
  # which is the property that makes it installable this way at all. Built from source
  # until tagged release binaries exist.
  head "https://github.com/atalayhuryasar/envmove.git", branch: "main"

  depends_on "go" => :build
  depends_on "git"

  def install
    ENV["CGO_ENABLED"] = "0"
    system "go", "build", *std_go_args(version: version, ldflags: "-s -w")
    bin.install "envmove"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/envmove version")

    # setup must refuse politely outside a repository rather than create state somewhere
    # unexpected.
    output = shell_output("cd #{tmp} && #{bin}/envmove setup", 1)
    assert_match "git", output
  end
end
