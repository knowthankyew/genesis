class Genesis < Formula
  desc "High-performance in-memory telemetry engine with zero external network egress"
  homepage "https://github.com/knowthankyew/genesis"
  url "https://github.com/knowthankyew/genesis.git",
      tag:      "v0.4.0",
      revision: "d0537b66039afe35d3518c6d8981c163762a01a1"
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
