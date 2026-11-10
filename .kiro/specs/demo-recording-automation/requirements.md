# Requirements Document: Demo Recording Automation

## Introduction

This document specifies requirements for an automated demo recording system for Genesis. Genesis is a high-performance, single-binary, local-first telemetry engine with zero external dependencies, capable of ingesting over 1,000,000 lines/second into volatile RAM with sub-millisecond query latency and zero disk footprint.

The demo recording system enables reproducible generation of visual demonstrations (`docs/assets/genesis-demo.gif` and `docs/assets/genesis-demo.mp4`) showcasing Genesis's core value proposition to both technical developers and general audiences. The demonstration tells a compelling story of modern telemetry: the invisible "digital exhaust" of modern life (mobile apps, ad trackers, IoT sensors, microservices) flooding systems with millions of events, Genesis drinking from this firehose effortlessly in local memory, instant needle-in-a-haystack discovery (<1ms), and an instant Amnesiac Purge (`genesis burn`) that completely vaporizes the logs from RAM with zero traces left behind.

The resulting artifact is regeneratable via `make demo-record` and replaces the committed asset at `docs/assets/genesis-demo.gif`.

## Glossary

- **Demo_Recording_System**: The automated tooling that generates browser and terminal session recordings demonstrating Genesis capabilities.
- **Genesis_Server**: The high-performance log collection engine being demonstrated (`bin/genesis server`).
- **Digital_Exhaust**: The realistic high-velocity stream of modern machine logs (mobile tracking, ad beacons, IoT pings, auth tokens, system errors).
- **Data_Generator**: The script (`scripts/demo-data-generator.sh`) that produces high-volume, realistic telemetry streams with current timestamps.
- **Demo_Artifact**: The generated GIF and MP4 files at `docs/assets/genesis-demo.gif` and `docs/assets/genesis-demo.mp4`.
- **Three_Stage_Narrative**: The narrative arc: **The Flood** (high-volume ingestion) $\rightarrow$ **The Needle** (instant sub-millisecond search & filtering) $\rightarrow$ **The Erasure** (Amnesiac Purge / zero-trace destruction).
- **Portfolio_Recorder**: The headless browser recording pipeline using Playwright and bundled FFmpeg (`scripts/record-demo.js`), matching KnowThankYew portfolio standards.
- **Make_Target**: The `make demo-record` command entry point in the Genesis Makefile.
- **Amnesiac_Purge**: The `genesis burn` command or UI action that atomically wipes all memory partitions and indices.

---

## Requirements

### Requirement 1: Automated Demo Artifact Generation

**User Story:** As a Genesis maintainer, I want to regenerate the demo recording automatically via a single command, so that documentation and visual assets remain in sync with feature improvements.

#### Acceptance Criteria

1. WHEN a maintainer executes `make demo-record`, THE Demo_Recording_System SHALL generate `docs/assets/genesis-demo.gif` and `docs/assets/genesis-demo.mp4`.
2. THE Demo_Recording_System SHALL complete generation within 120 seconds on standard developer hardware.
3. WHEN generation completes successfully, THE Demo_Recording_System SHALL exit with status code 0 and report artifact paths and file sizes.
4. IF generation fails, THEN THE Demo_Recording_System SHALL exit with a non-zero status code and display an actionable error message to standard error.
5. THE Demo_Recording_System SHALL produce a GIF Demo_Artifact with file size between 500 KB and 10 MB (optimized for web embedding).

### Requirement 2: High-Volume "Digital Exhaust" Narrative

**User Story:** As a prospective user or viewer, I want to see realistic high-volume log ingestion, so that I grasp the overwhelming scale of modern telemetry and Genesis's ability to handle it.

#### Acceptance Criteria

1. THE Data_Generator SHALL emit realistic multi-category digital telemetry including mobile app analytics, ad-tracking beacons, IoT device telemetry, and critical service error events.
2. THE Data_Generator SHALL stream at least 10,000 log entries (targeting 25,000+ entries) into Genesis during the recording session.
3. THE Data_Generator SHALL generate timestamps relative to current execution time (`time.Now()` or equivalent UTC ISO 8601 timestamps), ensuring queries within standard windows (e.g., `--since 1h`) accurately match all generated logs.
4. THE Demo_Recording_System SHALL show the Genesis server UI status counters (`Indexed`, `Active`, `Heap`) incrementing rapidly as the flood is ingested.

### Requirement 3: Instant Needle-in-a-Haystack Discovery

**User Story:** As a viewer, I want to see how quickly specific critical events can be discovered in a massive flood of logs, so that I appreciate Genesis's sub-millisecond retrieval performance.

#### Acceptance Criteria

1. THE Demo_Recording_System SHALL demonstrate filtering by severity level (e.g. `ERROR` or `FATAL`).
2. THE Demo_Recording_System SHALL demonstrate substring/grep search identifying a specific needle in the haystack (e.g., an exposed credential, token, or connection failure).
3. THE Demo_Recording_System SHALL show query results updating immediately with zero perceived latency.
4. THE Demo_Recording_System SHALL showcase live tail streaming (`SSE`) with real-time incoming log updates.

### Requirement 4: Amnesiac Purge (Privacy & Control)

**User Story:** As a viewer, I want to see the Amnesiac Purge in action, so that I understand Genesis provides total privacy and zero disk footprint.

#### Acceptance Criteria

1. THE Demo_Recording_System SHALL trigger the Amnesiac Purge via the UI or `genesis burn -y`.
2. WHEN the purge executes, THE Demo_Recording_System SHALL verify that all log entries are instantly removed and the active record counter drops to 0.
3. THE Demo_Recording_System SHALL show that subsequent queries return empty results, confirming zero persistent data remains.

### Requirement 5: Portfolio-Standard Recording Architecture

**User Story:** As a maintainer across the KnowThankYew portfolio, I want Genesis demo recording to align with portfolio patterns, so that tooling and maintenance are unified.

#### Acceptance Criteria

1. THE Demo_Recording_System SHALL utilize Playwright with Chromium to record the Genesis Web UI session.
2. THE Demo_Recording_System SHALL resolve `playwright` and `ffmpeg-static` from either the repo root or the shared portfolio `node_modules`.
3. THE Demo_Recording_System SHALL transcode recorded WebM video into high-quality MP4 (`H.264`) and an animated GIF with an optimized color palette.
4. THE Demo_Recording_System SHALL ensure proper cleanup of temporary video chunks and processes on exit.

### Requirement 6: Robust Process Lifecycle and Clean Teardown

**User Story:** As a developer, I want demo generation to manage background server processes cleanly, so that ports are never blocked by orphaned processes.

#### Acceptance Criteria

1. THE Demo_Recording_System SHALL start the Genesis server with correct flags (e.g., `--tcp`, `--http`, `--syslog`).
2. THE Demo_Recording_System SHALL wait for HTTP readiness (`GET /stats`) before initiating data ingestion or browser navigation.
3. WHEN the recording script finishes (either successfully or via interrupt/error), THE Demo_Recording_System SHALL terminate all child Genesis processes via signal traps.
4. THE Demo_Recording_System SHALL verify that no orphaned Genesis processes remain bound to the demo ports after execution.

### Requirement 7: Make Integration and Prerequisite Verification

**User Story:** As a developer, I want `make demo-record` to verify prerequisites before running, so that missing tools are clearly communicated.

#### Acceptance Criteria

1. THE Makefile SHALL define the `demo-record` target.
2. THE Makefile SHALL depend on the `build` target to ensure `bin/genesis` is freshly compiled.
3. THE Make target SHALL verify that Node.js and Playwright dependencies are accessible, printing guidance if missing.
