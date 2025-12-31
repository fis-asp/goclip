# Copilot Instructions for goclip

## Project Overview

goclip is a cross-platform (Windows & macOS) clipboard typing tool that simulates real keyboard events to type text into any focused window, including web/VNC/VM consoles. It's built with Go and uses Fyne for the GUI framework.

## Architecture

- **Language**: Go 1.24+
- **GUI Framework**: Fyne v2
- **Build System**: Go modules
- **Platform-specific code**: Build tags (`//go:build windows` and `//go:build darwin`)
- **Windows Implementation**: Uses Windows API (SendInput, scan codes) via golang.org/x/sys/windows
- **macOS Implementation**: Uses Core Graphics events (CGEvent) for keyboard simulation
- **Localization**: Multi-language support via `localization` package

## Key Files

- `main.go` - Windows-specific implementation (requires `//go:build windows` tag)
- `main_darwin.go` - macOS-specific implementation (requires `//go:build darwin` tag)
- `localization/localization.go` - Internationalization support
- `go.mod` - Go module dependencies
- `.github/workflows/build-windows.yml` - Windows build pipeline
- `.github/workflows/build-macos.yml` - macOS build pipeline
- `.gitlab-ci.yml` - GitLab CI/CD for SBOM generation

## Build Instructions

### Windows
```powershell
# Prerequisites: MinGW-w64 for CGO
go mod tidy
go build -trimpath -ldflags="-H=windowsgui -s -w" -o goclip.exe .
```

### macOS
```bash
# Prerequisites: Xcode Command Line Tools for CGO
go mod tidy
go build -trimpath -ldflags="-s -w" -o goclip .
```

### Build Flags
- `-H=windowsgui` - Hide console window on Windows
- `-trimpath` - Remove file system paths from binaries
- `-s -w` - Strip debug info and symbol table for smaller binaries

## Testing

Currently, this project does not have automated tests. Manual testing involves:
1. Building the application for the target platform
2. Running the application
3. Testing keyboard layout selection (Windows)
4. Testing window targeting functionality
5. Verifying typing functionality in different target applications

## Code Conventions

### General
- Use standard Go formatting (gofmt)
- Follow Go best practices and idiomatic patterns
- Use build tags for platform-specific code
- Keep Windows and macOS implementations separate

### Platform-Specific Code
- Windows code goes in files with `//go:build windows` tag
- macOS code goes in files with `//go:build darwin` tag
- Use appropriate system APIs via `golang.org/x/sys` package

### Dependencies
- Prefer standard library when possible
- Fyne is used for cross-platform GUI
- Keep dependencies minimal and well-maintained
- CGO is required for Fyne and platform-specific features

### Localization
- All user-facing strings should be localized
- Support currently exists for English and German
- Use the `localization` package for all UI text

## Project Structure

```
goclip/
├── .github/              # GitHub-specific files
│   ├── workflows/        # CI/CD workflows
│   └── ISSUE_TEMPLATE/   # Issue templates
├── assets/               # Application resources
│   └── logo/            # Application icons
├── localization/         # Internationalization
│   └── localization.go  # Localization definitions
├── main.go              # Windows implementation
├── main_darwin.go       # macOS implementation
├── go.mod               # Go module definition
└── go.sum               # Go module checksums
```

## Important Notes

### Windows-Specific
- Uses scan codes via SendInput for keyboard simulation
- Requires MinGW-w64 for CGO compilation
- Supports multiple keyboard layouts via Windows API
- UAC elevation required to type into elevated applications

### macOS-Specific
- Uses Core Graphics CGEvent for keyboard simulation
- Requires Xcode Command Line Tools for compilation
- Automatically uses system keyboard layout
- Requires accessibility permissions at runtime
- Supports both ARM64 (Apple Silicon) and AMD64 (Intel)

### Security
- Never commit secrets or sensitive data
- Be cautious with Windows API calls and memory management
- Validate user input before processing
- Handle system permissions appropriately

## CI/CD

### GitHub Actions
- **build-windows.yml**: Builds Windows binaries on push/PR/tags
- **build-macos.yml**: Builds macOS binaries (universal) on push/PR/tags
- **codeql.yml**: Security scanning with CodeQL
- Automatic GitHub Releases created on version tags (v*)

### GitLab CI
- Generates Software Bill of Materials (SBOM) using CycloneDX
- Uploads SBOM to internal Dependency-Track system

## Common Tasks

### Adding a New Keyboard Layout (Windows)
Edit the `loadHKLByName` function in `main.go` and add the appropriate KLID (keyboard layout ID).

### Adding a New Language
1. Add translations to the `localization` package
2. Update language detection logic
3. Add new language option to UI dropdown

### Debugging Build Issues
- Ensure CGO is enabled: `CGO_ENABLED=1`
- Verify compiler is in PATH (MinGW on Windows, clang on macOS)
- Check Go version compatibility (1.24+)

## Dependencies Management

- Use `go mod tidy` to update dependencies
- Review dependency changes carefully
- Test thoroughly after dependency updates
- Monitor for security vulnerabilities via Dependabot
