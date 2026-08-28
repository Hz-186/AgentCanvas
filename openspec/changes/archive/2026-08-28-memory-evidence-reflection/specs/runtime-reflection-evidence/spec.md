# Capability: runtime-reflection-evidence

## Purpose

Defines how online and terminal reflection consume structured tool evidence: full-trajectory signal scanning, fingerprinted repeated failures, evidence-rich prompt windows, complete terminal lesson structure, and observable enqueue failure.

## ADDED Requirements

### Requirement: Reflection scans the full tool trajectory

The reflection signal scan MUST examine all tool result steps of the run instead of returning at the first error. Classification MUST cover tool error, tool-not-found, denial, and argument/schema failure (using the structured `error_code` carried on the run step — sourced from tool-result metadata — first, substring fallback second, with deterministic non-overwriting precedence). A fingerprint `tool_name + normalized(arguments) + error_code` MUST be computed for each failed step; a fingerprint occurring in two or more failed steps with no same-fingerprint success between them MUST produce a `repeated_failure/no_progress` signal.

#### Scenario: Later root failure is not masked by an earlier error

- **GIVEN** a run with two distinct tool failures in different batches
- **WHEN** the signal scan runs
- **THEN** both failures are classified and fingerprinted
- **AND** the selected signal reflects the highest-priority fingerprint rather than only the first error

#### Scenario: Same wrong arguments twice trigger repeated failure

- **GIVEN** the same tool failing twice with normalized-equal arguments and equal error code, with no success between
- **WHEN** the signal scan runs
- **THEN** the signal type is `repeated_failure/no_progress`

#### Scenario: Success resets the fingerprint run

- **GIVEN** failure, then success with the same tool and arguments, then another failure
- **WHEN** the signal scan runs
- **THEN** no `repeated_failure` signal is produced

### Requirement: Reflection prompt carries arguments and recovery context

The reflection prompt MUST include up to 12 steps around the selected signal step, including tool call arguments (`ArgumentsJSON`, truncated to the same 1200-character cap as content), outputs, error codes, error text, and any later successful recovery for the same tool. Existing strict-JSON output validation, required fields, action enum, and minimum confidence checks MUST remain unchanged, and tool output MUST remain untrusted user-role evidence.

#### Scenario: Schema error is diagnosable from the prompt

- **GIVEN** a failed call whose error is malformed arguments
- **WHEN** the prompt is built
- **THEN** the call's arguments, error code, and error text appear in the prompt within their truncation caps

#### Scenario: Window is capped

- **GIVEN** 30 steps around the signal
- **WHEN** the prompt is built
- **THEN** exactly 12 steps are included, centered on the signal step as far as the trajectory allows

### Requirement: Terminal reflection persists full lesson structure

Terminal reflection content MUST persist root cause, corrective action, lesson, and applicability — not only lesson and corrective action. The existing write path (`TerminalReflectionWriter` → `MemoryWritePipeline`, idempotency `reflection:run:<run-id>`) MUST remain.

#### Scenario: Persisted lesson keeps structure

- **GIVEN** inline reflections carrying RootCause and Applicability
- **WHEN** terminal reflection content is assembled
- **THEN** the persisted content contains root cause, corrective action, lesson, and applicability sections

### Requirement: Terminal reflection enqueue failure is observable

A failed terminal reflection enqueue MUST NOT fail the run. It MUST emit a structured `slog` warning carrying run and agent identifiers and MUST emit a best-effort `AgentStep` event reusing the existing error-step payload pattern established by citation finalization. No new event type is introduced.

#### Scenario: Enqueue failure does not break the run

- **GIVEN** the write pipeline returns an enqueue error
- **WHEN** run finalization completes
- **THEN** the run result remains successful
- **AND** a structured warning log with run_id and agent_id exists
- **AND** an AgentStep event with the error-step payload pattern was attempted

#### Scenario: Successful enqueue stays quiet

- **GIVEN** a successful enqueue
- **WHEN** run finalization completes
- **THEN** no warning event and no warning log are produced
