# Contributing to KektorDB

First off, thank you for considering contributing to KektorDB! 🚀

## 🛠️ Getting Started

### Prerequisites
*   **Go 1.24.6+**
*   **Make** (Essential for the build workflow)
*   **Rust/Cargo** (Optional, only required if you touch the compute kernels)
*   **Zig** (Optional, only for cross-compilation releases)

### Setup Environment
1.  **Fork** and **Clone** the repository.
2.  **Run Tests** to ensure your environment is ready:
    ```bash
    make test
    ```

---

## 🏃 Running KektorDB

KektorDB has two build modes. For most contributions, the **Pure Go** mode is sufficient and faster to iterate on.

### 1. Pure Go Mode (Default)
This uses the AVX optimized Assembly (via `avo`) or generic Go code. No Rust required.
```bash
make run
```

### 2. Rust Optimized Mode (Advanced)
This uses CGO to link against the Rust compute kernels. Use this if you are working on performance optimization.
```bash
make run-rust
```

> **Important:** If you switch between modes, always run `make clean` to remove conflicting artifacts.

---

## 📐 Coding Standards

We follow standard Go idioms.

*   **Formatting:** Most editors (VSCode, Neovim, GoLand) handle this automatically. If yours doesn't, please run `make fmt` before committing.
*   **Architecture:**
    *   `pkg/core`: Pure data structures (HNSW, KV). No HTTP dependencies.
    *   `pkg/engine`: Persistence and Orchestration logic.
    *   `internal/server`: HTTP API Layer and wiring.

---
### Commit Messages
We follow the **Conventional Commits** specification to automate release notes.

*   `feat: ...` -> New feature
*   `fix: ...` -> Bug fix
*   `docs: ...` -> Documentation changes
*   `perf: ...` -> Performance improvements
*   `refactor: ...` -> Code change that neither fixes a bug nor adds a feature

**Example:**
`feat(rag): add support for .docx file ingestion`

---

## 🧪 Testing Policy

We understand that writing tests takes time, but they are crucial for a database project.

*   **Fixing a Bug?** Please include a minimal test case that reproduces the bug (fails before your fix, passes after). This prevents regressions.
*   **New Feature?** Please include basic unit tests to demonstrate that the feature works as expected.
*   **Refactoring?** Ensure existing tests pass.

We don't demand 100% coverage, but PRs with tests are merged much faster because they give us confidence.

---

## 📬 Submitting a Pull Request (PR)

1.  **Create a Branch:** Use a descriptive name (e.g., `feat/epub-support`, `fix/race-condition`).
2.  **Make your Changes.**
3.  **Run Tests:** Ensure everything passes in Pure Go mode (`make test`).
4.  **🧹 CLEANUP (Crucial):** Before staging your files, run:
    ```bash
    make clean
    ```
    *This removes generated assembly files (AVO) and Rust build artifacts to prevent them from polluting your PR.*
5.  **Commit & Push:**
    ```bash
    git add .
    git commit -m "feat: description of your change"
    git push origin your-branch
    ```
6.  **Open the PR:** Link any relevant issues.

Thank you for helping build KektorDB!
