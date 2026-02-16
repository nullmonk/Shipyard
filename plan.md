# Project Update Plan - Shipyard

This plan outlines the core goals and subgoals for updating the Shipyard project to improve its functionality, maintainability, and user experience.

## Core Goals

1.  **Refine Core CLI & Remove `shipyard-build`**
    *   Focus the `shipyard` CLI on local patch management (generation, import, export).
    *   **Remove `shipyard-build`**: Deprecate and remove the in-container python script. All build logic will be orchestrated from the host.
    *   **Zero-Dependency Local Mode**: Ensure `shipyard` (init, import, export) runs without the `dagger` SDK installed. The `dagger` import should be lazy or guarded.

2.  **Integrate Dagger for Host-Side Orchestration**
    *   **Strategy: Abstracted Patch Application (The Solution to the Conundrum)**.
        *   Create a `PatchBackend` interface with methods like `apply_patch(patch_content, target_dir)`.
        *   **Host Logic (The Brain):** The local `shipyard` CLI parses the `Shipfile`, resolves versions, and compiles the final patch content in memory.
        *   **Dagger Backend (The Muscle):** Instead of running `shipyard` inside the container, this backend *injects* the generated patch content into the container (effectively "generating" the file there) and executes standard build commands (`patch`, `make`, etc.) via the Dagger API.
    *   Implement "Distro Drivers": Classes that know how to generate Dagger commands for specific distros (Debian vs. RPM).

3.  **Decouple Build Logic**
    *   Abstract the "Build Definition" (dependencies, build commands, patch application method) from the execution.
    *   **Host-Generator / Container-Executor Model**:
        *   Ensure the `Shipfile` is processed entirely on the host to produce a portable `BuildSpec`.
        *   This ensures that complex python logic (Shipyard) stays on the host, while the container only needs standard system tools.

4.  **Improve Code Quality & Documentation**
    *   Add type hinting, linting, and comprehensive examples.
    *   Support modern Python versions (3.8+) and update dependencies.
    *   **Verify Optional Dependencies**: Test the CLI in an environment where `dagger` is not installed to confirm no `ImportError` occurs on startup.

---

## Subgoals

### 1. Refine Core CLI & Remove `shipyard-build`
- [x] **Refactor CLI**: Clean up `shipyard/cli.py` to focus on the developer workflow (init, import, export).
- [x] **Remove `bin/shipyard-build`**: Delete the script and its associated logic in `shipyard/builder.py` (if any).
- [x] **Modernize Packaging**: Update `pyproject.toml` to strictly define the `shipyard` entry point.

### 2. Integrate Dagger for Host-Side Orchestration
- [x] **Create `shipyard.engines.dagger`**: A module responsible for translating `Shipfile` instructions into Dagger graphs.
- [x] **Implement `PatchBackend` Interface**: Define the contract for applying patches (Local vs Dagger).
- [x] **Implement "Patch Injector"**: Logic to mount local patch files into the Dagger container at runtime.
- [x] **Implement "Distro Drivers"**: Classes that know how to generate Dagger commands for specific distros (Debian vs. RPM).S

### 4. Improve Code Quality & Documentation
- [ ] **Static Analysis**: Integrate `ruff` for linting and `mypy` for type checking.
- [ ] **Example Repository**: Create an `examples/` folder showing how to manage a real-world project.
- [ ] **Documentation**: Update `docs/` to explain the new "Agentless" build architecture.