class Genesis < Formula
  desc "High-performance in-memory telemetry engine with zero external network egress"
  homepage "https://github.com/knowthankyew/genesis"
  url "https://github.com/knowthankyew/genesis/archive/refs/tags/v0.3.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000" # Replace with release SHA
  license "MIT"
  head "https://github.com/knowthankyew/genesis.git", branch: "main"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w"), "./cmd/server"
  end

  service do
    run [opt_bin/"genesis", "server", "-tcp=127.0.0.1:9000", "-http=127.0.0.1:8080"]
    keep_alive true
    log_path var/"log/genesis.log"
    error_log_path var/"log/genesis.error.log"
  end

  test do
    output = shell_output("#{bin}/genesis help")
    assert_match "Genesis - High-Performance In-Memory Telemetry Engine", output
  end
end
