# Hercules Pure Go Rewrite Completion Plan

## Project Overview

This plan outlines the steps to complete the Hercules pure Go rewrite, which modernizes the original `gopkg.in/src-d/hercules.v10` to use `github.com/meko-christian/hercules` with updated dependencies and improved architecture.

## Current State Analysis

### ✅ Completed Components

- [x] **Module Structure**: Successfully migrated from `gopkg.in/src-d/go-git.v4` to `github.com/go-git/go-git/v5`
- [x] **Build System**: Makefile updated to use new module path
- [x] **Core Pipeline**: Pipeline system and dependency resolution working
- [x] **Analysis Items**: All major analysis types ported (burndown, couples, devs, commits, etc.)
- [x] **Basic Functionality**: CLI interface and help system working
- [x] **Compilation Fixes**: Fixed string conversion errors in test files

### 🔄 Architectural Improvements Made

- **Burndown Refactoring**: Split monolithic `internal/burndown/file.go` into modular components:
  - `internal/burndown/matrices.go` - Matrix operations
  - `internal/linehistory/` - Line history tracking
  - `internal/join/` - Data joining operations
- **Identity Management**: Enhanced from single file to modular system:
  - `internal/plumbing/identity/people.go` - People detection
  - `internal/plumbing/identity/stories.go` - Story tracking
  - `internal/plumbing/identity/identity_shared.go` - Shared utilities
- **New Analysis Types**: Added `codechurn.go` and `linedump.go` analyses

## Completion Plan

### Phase 1: Core Infrastructure Validation ⏳

#### 1.1 Test Suite Completion

- [ ] **Fix remaining compilation errors**
  - [x] Fixed `couples_test.go` string conversion issue
  - [ ] Verify all test files compile without errors
  - [ ] Run `go test ./...` successfully
- [ ] **Validate module consistency**
  - [x] Updated Makefile to use correct module path
  - [ ] Ensure all import statements use new module path
  - [ ] Verify go.mod dependencies are correctly resolved

#### 1.2 Core Pipeline Testing

- [ ] **Pipeline execution verification**
  - [ ] Test basic pipeline functionality: `./hercules --dry-run .`
  - [ ] Verify dependency resolution works correctly
  - [ ] Test pipeline with multiple analysis items
- [ ] **Advanced features validation**
  - [ ] Test hibernation feature: `--hibernation-distance 10`
  - [ ] Verify merge tracking functionality
  - [ ] Test plugin system compatibility

### Phase 2: Analysis Feature Validation 🔍

#### 2.1 Burndown Analysis Testing

- [ ] **New modular architecture**
  - [ ] Test burndown analysis: `./hercules --burndown .`
  - [ ] Verify line history tracking works correctly
  - [ ] Test matrix operations functionality
  - [ ] Compare output format with original implementation
- [ ] **Hibernation integration**
  - [ ] Test `--burndown-hibernation-threshold`
  - [ ] Test `--burndown-hibernation-disk` mode
  - [ ] Verify memory optimization works

#### 2.2 All Analysis Types Testing

- [ ] **Core analyses**
  - [ ] Burndown: `--burndown --burndown-people --burndown-files`
  - [ ] Couples: `--couples`
  - [ ] Devs: `--devs`
  - [ ] Commits: `--commits`
  - [ ] File history: `--file-history`
  - [ ] Shotness: `--shotness` (if Babelfish available)
  - [ ] Comment sentiment: `--sentiment` (if TensorFlow available)
- [ ] **New analyses**
  - [ ] Code churn analysis functionality
  - [ ] Line dump analysis functionality
  - [ ] Verify these integrate properly with pipeline

#### 2.3 Output Format Validation

- [ ] **YAML output testing**
  - [ ] Verify YAML format matches original
  - [ ] Test output parsing with `labours` Python tools
  - [ ] Validate Unicode handling with `fix_yaml_unicode.py`
- [ ] **Protocol Buffers testing**
  - [ ] Test `--pb` flag functionality
  - [ ] Verify binary format compatibility
  - [ ] Test `hercules combine` command for merging results

### Phase 3: Integration & Compatibility Testing 🔗

#### 3.1 CLI Interface Validation

- [ ] **Command line flags**
  - [ ] Test all documented flags from `--help`
  - [ ] Verify flag combinations work correctly
  - [ ] Test edge cases and error handling
- [ ] **Repository handling**
  - [ ] Test with local repositories
  - [ ] Test with remote repositories (HTTPS/SSH)
  - [ ] Test with different repository sizes
  - [ ] Test caching functionality

#### 3.2 Python Integration Testing

- [ ] **Labours compatibility**
  - [ ] Install Python requirements: `pip3 install -e ./python`
  - [ ] Test basic plotting: `./hercules --burndown . | labours -m burndown-project`
  - [ ] Test Protocol Buffers mode: `./hercules --burndown --pb . | labours -f pb -m burndown-project`
  - [ ] Verify all plotting modes work

#### 3.3 Performance & Memory Testing

- [ ] **Large repository testing**
  - [ ] Test with Linux kernel or similar large repo
  - [ ] Monitor memory usage during analysis
  - [ ] Verify hibernation prevents OOM errors
  - [ ] Compare performance with original implementation

### Phase 4: Output Validation & Comparison 📊

#### 4.1 Results Accuracy Testing

- [ ] **Side-by-side comparison**
  - [ ] Run same analysis on both versions
  - [ ] Compare YAML outputs (allowing for minor formatting differences)
  - [ ] Verify numerical results match
  - [ ] Document any intentional differences

#### 4.2 Edge Case Testing

- [ ] **Repository edge cases**
  - [ ] Empty repositories
  - [ ] Single commit repositories
  - [ ] Repositories with complex merge histories
  - [ ] Repositories with renames and moves
  - [ ] Repositories with binary files

### Phase 5: Documentation & Polish 📚

#### 5.1 Documentation Updates

- [ ] **README.md updates**
  - [ ] Update installation instructions
  - [ ] Update module path references
  - [ ] Update Go version requirements (1.18+)
  - [ ] Update example commands
- [ ] **Code documentation**
  - [ ] Update package documentation
  - [ ] Add documentation for new modular architecture
  - [ ] Document new analysis types

#### 5.2 Final cleanup

- [ ] **Code quality**
  - [ ] Run `go fmt` on all packages
  - [ ] Run `go vet` and fix warnings
  - [ ] Remove any dead code
  - [ ] Optimize imports

- [ ] **Release preparation**
  - [ ] Update version information
  - [ ] Update CLAUDE.md with final architecture
  - [ ] Create migration guide from original

## Testing Strategy

### Automated Testing

```bash
# Core test suite
just test

# Individual package testing
go test ./internal/core
go test ./internal/plumbing
go test ./leaves
go test ./internal/burndown
go test ./internal/linehistory

# Integration testing
./hercules --dry-run .
./hercules --burndown --dry-run .
```

### Manual Testing Commands

```bash
# Basic functionality
./hercules --help
./hercules --version

# Core analyses
./hercules --burndown .
./hercules --burndown --burndown-people .
./hercules --couples .
./hercules --devs .

# With Python plotting
./hercules --burndown . | labours -m burndown-project
./hercules --burndown --pb . | labours -f pb -m burndown-project

# Performance testing
./hercules --burndown --hibernation-distance 10 /path/to/large/repo
```

### Validation Criteria

#### Functionality ✅

- [ ] All CLI commands work without errors
- [ ] All analysis types produce expected output
- [ ] Python integration works correctly
- [ ] Protocol Buffers format is compatible

#### Performance ✅

- [ ] Memory usage is reasonable (hibernation works)
- [ ] Analysis speed is comparable to original
- [ ] Large repositories can be processed

#### Compatibility ✅

- [ ] Output format matches original (YAML/PB)
- [ ] Labours plotting works with new output
- [ ] Result merging works correctly

## Risk Mitigation

### High Risk Items

1. **Burndown Architecture Changes**: The split from monolithic to modular design
   - **Mitigation**: Thorough side-by-side testing with original
2. **Module Path Migration**: Potential import path issues
   - **Mitigation**: Systematic verification of all imports

3. **Protocol Buffers Compatibility**: Schema changes
   - **Mitigation**: Binary compatibility testing

### Medium Risk Items

1. **Hibernation Feature**: Complex memory management
   - **Mitigation**: Memory profiling and stress testing
2. **New Analysis Types**: Code churn and line dump
   - **Mitigation**: Unit testing and validation

## Success Metrics

- [ ] **100% test suite passing**
- [ ] **All documented CLI flags working**
- [ ] **Python labours integration working**
- [ ] **Memory usage within acceptable bounds**
- [ ] **Output format compatibility maintained**
- [ ] **Performance within 10% of original**

## Timeline Estimate

- **Phase 1**: 1-2 days (Core infrastructure)
- **Phase 2**: 2-3 days (Analysis validation)
- **Phase 3**: 1-2 days (Integration testing)
- **Phase 4**: 1 day (Comparison testing)
- **Phase 5**: 1 day (Documentation)

**Total**: 6-9 days for complete validation and completion

## Next Steps

1. Start with Phase 1.1: Fix remaining compilation errors
2. Complete test suite validation
3. Move systematically through each phase
4. Document any issues or deviations found
5. Create final migration documentation
