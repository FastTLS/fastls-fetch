# Consolidated Go candidate

The core revision is pinned with the coordinated Go services in `go.mod`.
Runtime API and application Cookie/redirect ownership remain unchanged.

CI distinguishes development feedback (Linux/Windows, current toolchain),
docs-only checks, and a frozen candidate (the original three platforms and both
toolchains, plus Linux race). The aggregate gate rejects missing, failed,
cancelled or unexpectedly skipped planned jobs. Race and coverage run together.

Release archives are generated outside the checkout under RUNNER_TEMP. The
workflow verifies source cleanliness around all target builds and preserves real
VCS metadata. Published v0.1.1 artifacts are not modified by this source change.
