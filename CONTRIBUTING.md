# Contributing to CHUM

Thank you for your interest in contributing to CHUM! This document provides guidelines and instructions for contributing.

## Development Setup

### Prerequisites

- Go 1.24 or later
- SQLite 3
- Make
- [Beads (`bd`)](https://github.com/steveyegge/beads) - For issue tracking
- [OpenClaw](https://github.com/openclaw/openclaw) - For agent runtime (optional)

### Quick Start

```bash
# Clone the repository
git clone git@github.com:Heikkila-Pty-Ltd/chum.git
cd chum

# Build the project
make build

# Run tests
make test

# Run linting
make lint
```

## Project Structure

Please familiarize yourself with the [Project Structure](./PROJECT_STRUCTURE.md). Key points:

- `cmd/` - Main application entry points
- `internal/` - Private application code
- `pkg/` - Public library code (if any)
- `scripts/` - Utility scripts
- `docs/` - Documentation

## Development Workflow

### 1. Create an Issue

Before starting work, create or find an issue in the [beads tracker](./.beads):

```bash
bd create "Add feature X to Y"
```

### 2. Create a Branch

Before starting any code changes:

```bash
# Ensure you are on up-to-date master
git checkout master
git pull --rebase
./scripts/hooks/install.sh

# Create a feature workflow branch
git checkout -b feature/your-feature-name
```

Branch naming conventions:
- `feature/<description>` - New features
- `fix/<description>` - Bug fixes
- `chore/<description>` - Maintenance tasks
- `refactor/<description>` - Code refactoring

Hotfix exception:
- `hotfix/<description>` is allowed only for approved production hotfixes.
- Open a short-lived `hotfix/*` branch and use the normal PR path (no direct master commits).
- Include approver signoff in the linked issue/PR before merging.

### 3. Install local git hook (first-time / periodic)

Install the branch policy hook so direct master commits are blocked locally:

```bash
./scripts/hooks/install.sh
```

### 4. Make Changes

- Use branch-safe workflow commands (`git status`, `git add`, `git commit`) only on your working branch.

- Write clear, concise commit messages
- Follow Go best practices and idioms
- Add tests for new functionality
- Update documentation as needed

### 5. Run Quality Checks

```bash
# Format code
make fmt

# Run linters
make lint

# Run tests
make test

# Run race tests (for concurrency changes)
make test-race
```

### 6. Submit a Pull Request

1. Push your branch: `git push origin feature/your-feature-name`
2. Create a pull request on GitHub
3. Fill out the PR template
4. Request review from maintainers

### 7. Worktree workflow (parallel features)

Use isolated worktrees to avoid branch switching:

```bash
# From the main repository
git checkout master
git pull --rebase
git worktree add -b feature/your-feature-name ../chum-feature-x
cd ../chum-feature-x
```

Work each feature in its own worktree directory. Close worktrees when done:

```bash
git worktree remove ../chum-feature-x
```

Team training checkpoint:

Before starting parallel work, complete this quick check:
- Confirm hook is installed and blocks master commits:
  `./scripts/hooks/install.sh`
- Confirm branch checks trigger before the first push:
  `git checkout master` and verify a commit attempt is blocked.
- Open one isolated worktree and verify branch name checks on first commit.
- Open a draft PR and confirm the PR branch-policy check runs in CI.

For the full walkthrough, see [Git worktree workflow](docs/development/GIT_WORKTREE_WORKFLOW.md).

## Code Standards

### Go Code Style

- Follow [Effective Go](https://golang.org/doc/effective_go)
- Use `gofmt` for formatting
- Use meaningful variable names
- Write comprehensive documentation comments
- Keep functions focused and small

### Testing

- Write unit tests for new functions
- Add integration tests for new features
- Maintain test coverage for critical paths
- Use table-driven tests where appropriate

### Documentation

- Update `docs/` for user-facing changes
- Update `README.md` for major features
- Add comments for complex logic
- Update `AGENTS.md` if changing agent interfaces

## Commit Message Guidelines

Format:
```
type(scope): Short description

Longer explanation if needed. Wrap at 72 characters.

- Bullet points are okay
- Use imperative mood: "Add feature" not "Added feature"
```

Types:
- `feat` - New feature
- `fix` - Bug fix
- `docs` - Documentation only
- `style` - Formatting, no code change
- `refactor` - Code restructuring
- `test` - Adding or updating tests
- `chore` - Maintenance tasks

Examples:
```
feat(scheduler): Add priority-based task queue

Implements a priority queue for tasks based on project
priority and bead urgency. Tasks are now sorted by:
1. Project priority
2. Bead priority
3. Creation time

fix(store): Correct race condition in dispatch tracking

The dispatch tracker had a race condition when multiple
goroutines updated status simultaneously. Added mutex
to protect shared state.
```

## Release Process

See [docs/development/RELEASE.md](./docs/development/RELEASE.md) for release procedures.

## Questions?

- Check [docs/development/](./docs/development/) for guides
- Review [AGENTS.md](./AGENTS.md) for agent context
- Open an issue for discussion

## Code of Conduct

This project follows the [Contributor Covenant Code of Conduct](https://www.contributor-covenant.org/version/2/1/code_of_conduct/).

By participating, you agree to uphold this code.
