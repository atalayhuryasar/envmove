require "English"
class Envmove < Formula
  desc "Carry the project context git refuses to: .env, handover docs, AI agent state"
  homepage "https://github.com/atalayhuryasar/envmove"
  url "https://github.com/atalayhuryasar/envmove/archive/refs/tags/v0.2.0.tar.gz"
  version "0.2.0"
  license "MIT"
  head "https://github.com/atalayhuryasar/envmove.git", branch: "main"

  # This file is a mirror of the one in the tap, which is what brew actually reads.
  # Keep both in step when cutting a release; the tap copy is canonical.
  #
  # The tag is written out rather than interpolated from `version`. Homebrew orders
  # url before version, so "#{version}" in the class body would expand to an empty
  # string and the download would 404 on "v.tar.gz". Both lines are literal and have to
  # be bumped together.
  #
  # Found by running brew install rather than brew style.

  # A plain Go binary, no cgo, nothing needed at runtime except git. That is what makes
  # building it from source acceptable here. Built from the tagged source so a given
  # formula version always produces the same commit.
  depends_on "go" => :build
  depends_on "git"

  def install
    ENV["CGO_ENABLED"] = "0"
    # Two things have to be right here, and neither is obvious:
    #
    #   - the package must be named, or go build targets the module root and fails with
    #     "no Go files";
    #   - output must be a plain name, not bin/, or the binary lands in the Cellar and
    #     the bin.install below cannot find it.
    #
    # std_go_args already supplies -s -w and takes no version keyword, so the ldflag
    # carries only the version.
    system "go", "build",
           *std_go_args(output: "envmove", ldflags: "-X main.version=#{version}"),
           "./cmd/envmove"
    bin.install "envmove"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/envmove version")

    outside = testpath/"outside"
    outside.mkpath

    # Backticks rather than shell_output. envmove writes diagnostics to stderr, which is
    # where a CLI should put them, and the assertion needs both streams; shell_output
    # only captures stdout and returned an empty string here.
    output = `cd #{outside.to_s.shellescape} && #{bin}/envmove setup 2>&1`
    status = $CHILD_STATUS

    assert_equal 1, status.exitstatus, "setup must fail outside a git repository"
    assert_match "git", output, "the failure has to say what was wrong"
  end
end
