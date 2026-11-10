# Implementation Plan: Demo Recording Automation

## Overview

This implementation plan delivers an automated, reproducible demo recording system for Genesis. The system showcases Genesis's core value proposition—ingesting high-volume digital telemetry into volatile memory with sub-millisecond retrieval and an instant Amnesiac Purge—matching the KnowThankYew portfolio recording standards.

## Tasks

- [x] 1. Core Recording Infrastructure & Scripts
  - [x] 1.1 Implement high-velocity digital exhaust generator
    - Create `scripts/demo-data-generator.sh`
    - Generate multi-category digital telemetry: mobile analytics, ad beacons, IoT sensor pings, checkout auth, and leaked security tokens
    - Ensure dynamic ISO 8601 UTC timestamps relative to `time.Now()`
    - Support streaming 25,000+ records in seconds over TCP / Genesis pipe
    - _Requirements: 2.1, 2.2, 2.3_

  - [x] 1.2 Implement Playwright UI recorder script
    - Create `scripts/record-demo.js`
    - Use portfolio Playwright and FFmpeg resolvers (`resolvePlaywright`, `resolveFfmpeg`)
    - Automate the 3-stage narrative: The Flood (live SSE log stream & stats climbing) $\rightarrow$ The Needle (severity filter & grep for exposed token) $\rightarrow$ The Erasure (Amnesiac Burn confirmation & instant wipe)
    - Transcode recorded WebM to web-standard MP4 and palette-optimized GIF (`docs/assets/genesis-demo.gif`)
    - _Requirements: 1.1, 1.5, 3.1, 3.2, 3.4, 4.1, 4.2, 5.1, 5.2, 5.3_

  - [x] 1.3 Implement orchestration runner script
    - Create `scripts/record-demo.sh`
    - Ensure binaries are built (`bin/genesis`)
    - Start Genesis server with correct flags (`--tcp=127.0.0.1:9000`, `--syslog=127.0.0.1:5514`, `--http=127.0.0.1:8080`)
    - Wait for server HTTP health check
    - Concurrently stream data and execute Playwright recorder
    - Implement shell signal traps (`SIGTERM`, `EXIT`) to guarantee zero orphaned server processes
    - _Requirements: 6.1, 6.2, 6.3, 6.4_

- [x] 2. Makefile Integration
  - [x] 2.1 Add `demo-record` and `check-demo-prereqs` targets to `Makefile`
    - Integrate `demo-record` to invoke `scripts/record-demo.sh`
    - Add prerequisite check for Node.js
    - Add `.PHONY` declarations
    - _Requirements: 7.1, 7.2, 7.3_

- [x] 3. Execution & Verification
  - [x] 3.1 Run `make demo-record` to generate recording artifacts
    - Execute end-to-end recording pipeline
    - Verify `docs/assets/genesis-demo.gif` and `docs/assets/genesis-demo.mp4` are generated
    - Verify GIF file size is within 500 KB to 10 MB range (Measured: 1.86 MB)
    - Verify no lingering Genesis server processes remain
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 6.4_

- [x] 4. Checkpoint & Review
  - [x] 4.1 Review generated demo recording and verify narrative quality
