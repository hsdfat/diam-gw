# CI/CD Pipeline Documentation

This document describes the GitHub Actions CI/CD pipeline for the Diameter Gateway project.

## Workflows

### 1. Gateway CI/CD with Performance Testing

**File**: `.github/workflows/gateway-ci.yml`

**Triggers**:
- Push to `main` or `dev` branches
- Pull requests to `main` or `dev` branches
- Manual workflow dispatch with configurable parameters

**Jobs**:

#### Build Job
- Sets up Go environment
- Caches dependencies
- Runs unit tests with race detection
- Generates code coverage report
- Uploads coverage to Codecov
- Builds all binaries (gateway, DRA, app simulator)
- Uploads build artifacts

#### Multi-App Test Job
- Builds Docker images
- Runs multi-application interface test
- Validates interface-based routing
- Checks connection affinity
- Collects and uploads test logs

#### Performance Test Job
- Builds Docker images
- Runs performance test with configurable duration and rate
- Extracts performance metrics:
  - Total messages processed
  - Actual throughput (req/s)
  - Routing errors
  - Affinity violations
  - Connection errors
- Generates performance grade (A, B, C, F)
- Creates performance report
- Comments on PR with results (for pull requests)
- Validates performance thresholds

#### Summary Job
- Downloads all test reports
- Creates comprehensive job summary
- Shows status of all jobs
- Displays performance results

### 2. Performance Badge Update

**File**: `.github/workflows/performance-badge.yml`

**Triggers**:
- When main CI/CD workflow completes successfully on `main` branch

**Jobs**:

#### Update Badge Job
- Downloads performance report from CI/CD workflow
- Parses metrics (throughput, grade)
- Generates badge JSON files
- Commits badges to repository
- Badges automatically update on README

## Manual Workflow Execution

You can manually trigger the CI/CD workflow with custom parameters:

```bash
# Via GitHub UI:
# 1. Go to Actions tab
# 2. Select "Gateway CI/CD with Performance Testing"
# 3. Click "Run workflow"
# 4. Set parameters:
#    - performance_duration: Test duration in seconds (default: 60)
#    - performance_rate: Target request rate in req/s (default: 1000)

# Via GitHub CLI:
gh workflow run gateway-ci.yml \
  -f performance_duration=120 \
  -f performance_rate=2000
```

## Performance Metrics

### Grading System

The performance test assigns a grade based on results:

| Grade | Criteria |
|-------|----------|
| **A** | Zero errors + Throughput ≥ 95% of target |
| **B** | Zero errors + Throughput < 95% of target |
| **F** | Any routing/affinity/connection errors |

### Thresholds

The pipeline validates:

✅ **Pass Criteria**:
- Routing errors = 0
- Affinity violations = 0
- Connection errors = 0
- Throughput ≥ 95% of target (warning only)

❌ **Fail Criteria**:
- Any routing errors detected
- Any affinity violations detected
- Any connection errors detected

## Test Scenarios

### Default Configuration
```yaml
Duration: 60 seconds
Target Rate: 1000 req/s
Interface Distribution:
  - S13: 40% (400 req/s)
  - S6a: 40% (400 req/s)
  - Gx: 20% (200 req/s)
Applications: 12 (4 per interface)
```

### Stress Test Configuration
```yaml
Duration: 300 seconds (5 minutes)
Target Rate: 5000 req/s
Interface Distribution:
  - S13: 40% (2000 req/s)
  - S6a: 40% (2000 req/s)
  - Gx: 20% (1000 req/s)
Applications: 12 (4 per interface)
```

## Artifacts

### Build Artifacts
- **binaries**: Compiled gateway, DRA, and app binaries
- **Retention**: 1 day

### Test Artifacts
- **multi-app-test-logs**: Logs from multi-app test
- **performance-test-logs**: Logs from performance test
- **performance-report**: Markdown report with metrics
- **Retention**: 7-30 days

## Badge Integration

The README displays real-time status badges:

1. **CI/CD Status**: Shows if latest build passed/failed
2. **Performance**: Shows actual throughput (req/s)
3. **Grade**: Shows performance grade (A, B, F)
4. **Go Report Card**: Code quality metrics

Badges are automatically updated after successful runs on `main` branch.

## Performance Report Format

Generated reports include:

```markdown
# Performance Test Report

## Test Configuration
- Duration: Xs
- Target Rate: X req/s
- Branch: branch-name
- Commit: sha
- Run ID: run-id

## Results

### Throughput
- Total Messages: X
- Actual Rate: X req/s
- Target Rate: X req/s

### Error Analysis
- Routing Errors: 0
- Affinity Violations: 0
- Connection Errors: 0

### Overall Grade
**A**

## Interface Distribution
- S13: 40% (Equipment Check)
- S6a: 40% (Authentication)
- Gx: 20% (Policy Control)

## Test Setup
- 1 DRA (Load Generator)
- 1 Gateway
- 12 Applications (4 per interface)
- Total: 24 concurrent connections
```

## Pull Request Integration

For pull requests, the workflow:

1. Runs all tests automatically
2. Posts performance report as PR comment
3. Shows detailed metrics comparison
4. Blocks merge if tests fail

## Local Testing

You can run the same tests locally:

```bash
# Multi-app test
./tests/multi-app/test-multi-app-podman.sh

# Performance test (same as CI default)
./tests/performance/test-performance-podman.sh --duration 60 --rate 1000

# Quick performance test
./tests/performance/test-performance-podman.sh --duration 30 --rate 500

# Stress test
./tests/performance/test-performance-podman.sh --stress
```

## Troubleshooting

### Failed Build
- Check Go version (requires 1.21+)
- Verify all dependencies in go.mod
- Check for compilation errors in logs

### Failed Multi-App Test
- Review test logs in artifacts
- Check Docker image builds
- Verify container networking
- Look for "routing" or "affinity" errors in gateway logs

### Failed Performance Test
- Check if all containers started properly
- Review DRA load generator logs
- Verify gateway metrics in logs
- Look for throughput bottlenecks

### Badge Not Updating
- Ensure workflow completed successfully on `main` branch
- Check performance-badge workflow logs
- Verify GitHub token permissions
- Allow a few minutes for badge cache to refresh

## Requirements

- GitHub Actions enabled
- Docker support in runners
- Go 1.21+
- Codecov token (optional, for coverage reports)

## Future Enhancements

Planned improvements:

- [ ] Benchmark comparison across commits
- [ ] Performance trend graphs
- [ ] Custom metrics dashboards
- [ ] Multi-architecture builds (arm64, amd64)
- [ ] Release automation
- [ ] Docker image publishing
- [ ] Load testing with different scenarios
- [ ] Latency percentile tracking (P95, P99)
