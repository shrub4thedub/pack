package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRepo = "https://github.com/shrub4thedub/pack-repo"
	packDir     = ".pack"
)

type Config struct {
	Sources []string
}

func main() {
	// Ensure pack directory structure exists
	if err := ensurePackDirExists(); err != nil {
		fmt.Printf("failed to create pack directory: %v\n", err)
		os.Exit(1)
	}

	// Bootstrap box interpreter if missing
	if err := ensureBoxExists(); err != nil {
		fmt.Printf("failed to bootstrap box interpreter: %v\n", err)
		os.Exit(1)
	}

	args := os.Args[1:]
	if len(args) == 0 {
		showHelp()
		return
	}

	command := args[0]
	switch command {
	case "open":
		if len(args) < 2 {
			fmt.Println("error: package name required")
			fmt.Println("usage: pack open <package>")
			os.Exit(1)
		}
		openPackage(args[1:])
	case "close":
		if len(args) < 2 {
			fmt.Println("error: package name required")
			fmt.Println("usage: pack close <package>")
			os.Exit(1)
		}
		closePackage(args[1:])
	case "peek":
		if len(args) < 2 {
			fmt.Println("error: package name required")
			fmt.Println("usage: pack peek <package>")
			os.Exit(1)
		}
		peekPackage(args[1:])
	case "list":
		listPackages(args[1:])
	case "update":
		updatePackages(args[1:])
	case "add-source":
		if len(args) < 2 {
			fmt.Println("error: source URL required")
			fmt.Println("usage: pack add-source <url>")
			os.Exit(1)
		}
		addSource(args[1:])
	case "help":
		showHelp()
	case "keygen":
		generateKeys()
	case "sign":
		if len(args) < 2 {
			fmt.Println("error: private key and file/directory required")
			fmt.Println("usage: pack sign <private_key_b64> <file_or_directory>")
			os.Exit(1)
		}
		signFiles(args[1], args[2])
	default:
		fmt.Printf("error: unknown command '%s'\n", command)
		fmt.Println("run 'pack help' for usage information")
		os.Exit(1)
	}
}


func openPackage(args []string) {
	if args[0] == "help" {
		showOpenHelp()
		return
	}
	
	packageName := args[0]
	fmt.Printf("opening package: %s\n", packageName)
	
	if err := executePackageScript(packageName, false); err != nil {
		fmt.Printf("error opening package %s: %v\n", packageName, err)
		os.Exit(1)
	}
}

func closePackage(args []string) {
	if args[0] == "help" {
		showCloseHelp()
		return
	}
	
	packageName := args[0]
	fmt.Printf("closing package: %s\n", packageName)
	
	if err := executeUninstallScript(packageName); err != nil {
		fmt.Printf("error closing package %s: %v\n", packageName, err)
		os.Exit(1)
	}
}

func peekPackage(args []string) {
	if args[0] == "help" {
		showPeekHelp()
		return
	}
	
	packageName := args[0]
	
	if err := showPackageInfo(packageName); err != nil {
		fmt.Printf("error showing package info for %s: %v\n", packageName, err)
		os.Exit(1)
	}
}

func addSource(args []string) {
	if args[0] == "help" {
		showAddSourceHelp()
		return
	}
	
	sourceURL := args[0]
	
	if err := addSourceToConfig(sourceURL); err != nil {
		fmt.Printf("error adding source: %v\n", err)
		os.Exit(1)
	}
	
	fmt.Printf("added source: %s\n", sourceURL)
}

func executePackageScript(packageName string, uninstall bool) error {
	// Uninstall is now handled by executeUninstallScript
	if uninstall {
		return executeUninstallScript(packageName)
	}
	// Create temporary directory for script
	tempDir, err := os.MkdirTemp("", "pack-"+packageName)
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Download or copy script using multi-source selection
	scriptPath := filepath.Join(tempDir, packageName+".box")
	
	selectedSource, err := downloadFromSources(packageName, scriptPath)
	if err != nil {
		return fmt.Errorf("failed to download script: %v", err)
	}
	
	// Note: selectedSource.Name used to be stored as sourceURL, now using repoName instead

	// Verify recipe integrity
	fmt.Println("verifying recipe integrity...")
	if err := verifyRecipeIntegrity(scriptPath, selectedSource.Name); err != nil {
		fmt.Printf("⚠️  warning: %v\n", err)
		fmt.Print("continue anyway? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %v", err)
		}
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			return fmt.Errorf("installation cancelled due to verification failure")
		}
	} else {
		fmt.Println("✓ recipe integrity verified")
	}

	// Show recipe and get user confirmation
	if err := showRecipeAndConfirm(scriptPath); err != nil {
		return err
	}

	// Find box executable
	boxPath, err := findBoxExecutable()
	if err != nil {
		return fmt.Errorf("box executable not found: %v", err)
	}

	// Execute script
	cmdArgs := []string{scriptPath}

	execCmd := exec.Command(boxPath, cmdArgs...)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execCmd.Stdin = os.Stdin

	err = execCmd.Run()
	if err != nil {
		return err
	}

	// Create or update lock file after successful installation
	fmt.Println("creating lock file...")
	
	// Extract source information from canonical schema
	sourceType, recipeSourceURL, sourceRef, sourceVersion, err := detectSourceTypeAndVersion(scriptPath)
	if err != nil {
		fmt.Printf("warning: failed to extract source info: %v\n", err)
		// Fall back to legacy extraction
		recipeSourceURL, err = extractRecipeURL(scriptPath)
		if err != nil {
			fmt.Printf("warning: failed to extract source URL: %v\n", err)
			recipeSourceURL = "unknown"
		}
		sourceType = "unknown"
		sourceVersion = "unknown"
		sourceRef = "unknown"
	}
	
	
	// Calculate recipe version (content hash)
	recipeVersion, err := calculateRecipeVersion(scriptPath)
	if err != nil {
		fmt.Printf("warning: failed to calculate recipe version: %v\n", err)
		recipeVersion = "unknown"
	}
	
	// Construct recipe URL from selected source
	recipeURL := constructRecipeURL(selectedSource, packageName)
	
	// Calculate actual SHA256 for lock file
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		fmt.Printf("warning: failed to read script for hash: %v\n", err)
	}
	contentWithoutSHA256, err := removeCSHA256Field(content)
	if err != nil {
		fmt.Printf("warning: failed to remove SHA256 field: %v\n", err)
	}
	recipeSHA256 := calculateSHA256(contentWithoutSHA256)
	
	// Get repo name from selected source
	repoName := selectedSource.Name
	
	if err := createLockFile(packageName, repoName, recipeSourceURL, sourceType, sourceRef, sourceVersion, recipeVersion, recipeURL, recipeSHA256); err != nil {
		fmt.Printf("warning: failed to create lock file: %v\n", err)
	} else {
		fmt.Println("✓ lockfile created")
	}

	return nil
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

func downloadFile(url, filepath string) error {
	return downloadFileWithCache(url, filepath, false)
}

// downloadFileWithCache downloads a file with optional ETag caching
func downloadFileWithCache(url, filepath string, useCache bool) error {
	client := &http.Client{Timeout: 30 * time.Second}
	
	// Prepare request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	
	// Use ETag caching if enabled and cache exists
	if useCache {
		if etag, err := loadETag(url); err == nil && etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
	}
	
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	// Handle 304 Not Modified
	if resp.StatusCode == http.StatusNotModified {
		// File hasn't changed, use cached version
		return nil
	}
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Create output file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Download content
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	
	// Save ETag for future requests if caching is enabled
	if useCache {
		if etag := resp.Header.Get("ETag"); etag != "" {
			saveETag(url, etag)
		}
	}
	
	return nil
}

// loadETag loads the cached ETag for a URL
func loadETag(url string) (string, error) {
	packPath, err := getPackDir()
	if err != nil {
		return "", err
	}
	
	cacheDir := filepath.Join(packPath, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", err
	}
	
	// Create filename from URL hash
	hash := sha256.Sum256([]byte(url))
	filename := hex.EncodeToString(hash[:])[:16] + ".etag"
	etagPath := filepath.Join(cacheDir, filename)
	
	content, err := os.ReadFile(etagPath)
	if err != nil {
		return "", err
	}
	
	return strings.TrimSpace(string(content)), nil
}

// saveETag saves the ETag for a URL
func saveETag(url, etag string) error {
	packPath, err := getPackDir()
	if err != nil {
		return err
	}
	
	cacheDir := filepath.Join(packPath, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	
	// Create filename from URL hash
	hash := sha256.Sum256([]byte(url))
	filename := hex.EncodeToString(hash[:])[:16] + ".etag"
	etagPath := filepath.Join(cacheDir, filename)
	
	return os.WriteFile(etagPath, []byte(etag), 0644)
}

func findBoxExecutable() (string, error) {
	// Try to find box in PATH
	if boxPath, err := exec.LookPath("box"); err == nil {
		return boxPath, nil
	}

	// Try relative path to boxlang directory
	currentDir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Try ../boxlang/box (assuming pack is sibling to boxlang)
	relativePath := filepath.Join(filepath.Dir(currentDir), "boxlang", "box")
	if _, err := os.Stat(relativePath); err == nil {
		absPath, err := filepath.Abs(relativePath)
		if err == nil {
			return absPath, nil
		}
	}

	// Try ./box in current directory
	localPath := "./box"
	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	return "", fmt.Errorf("box executable not found in PATH or relative paths")
}

func showRecipeAndConfirm(scriptPath string) error {
	// Read and display the script content
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read script: %v", err)
	}

	fmt.Println("recipe:")
	fmt.Println("-------")
	fmt.Println(string(content))
	fmt.Println("-------")
	
	for {
		fmt.Print("proceed? [y/e/n]: ")
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %v", err)
		}
		
		response = strings.TrimSpace(strings.ToLower(response))
		
		switch response {
		case "y", "yes", "":
			return nil
		case "n", "no":
			return fmt.Errorf("cancelled")
		case "e", "edit":
			if err := editScript(scriptPath); err != nil {
				fmt.Printf("error editing script: %v\n", err)
				continue
			}
			// Show the updated script and ask again
			return showRecipeAndConfirm(scriptPath)
		default:
			fmt.Println("enter y/e/n")
		}
	}
}

func editScript(scriptPath string) error {
	// Try to find an editor
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		// Try common editors
		for _, e := range []string{"vim", "nano", "vi"} {
			if _, err := exec.LookPath(e); err == nil {
				editor = e
				break
			}
		}
	}
	if editor == "" {
		return fmt.Errorf("no editor found")
	}

	// Launch editor
	cmd := exec.Command(editor, scriptPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func showPackageInfo(packageName string) error {
	// Create temporary directory for script
	tempDir, err := os.MkdirTemp("", "pack-peek-"+packageName)
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Download or copy script using multi-source selection
	scriptPath := filepath.Join(tempDir, packageName+".box")
	
	_, err = downloadFromSources(packageName, scriptPath)
	if err != nil {
		return fmt.Errorf("failed to download script: %v", err)
	}

	// Parse and display package information
	return parseAndDisplayPackageInfo(scriptPath, packageName)
}

func parseAndDisplayPackageInfo(scriptPath, packageName string) error {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read script: %v", err)
	}

	lines := strings.Split(string(content), "\n")
	var pkgData map[string]string
	var inDataBlock bool
	var blockIndent int

	// Parse data block
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		// Check for data block start (specifically looking for -c pkg block)
		if strings.HasPrefix(trimmed, "[data") && strings.Contains(trimmed, "pkg") {
			inDataBlock = true
			pkgData = make(map[string]string)
			// Calculate indentation level
			blockIndent = len(line) - len(strings.TrimLeft(line, " \t"))
			continue
		}
		
		// Check for block end
		if inDataBlock && trimmed == "end" {
			break
		}
		
		// Check for any other block start (should end data block)
		if inDataBlock && strings.HasPrefix(trimmed, "[") {
			break
		}
		
		// Parse data block content
		if inDataBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			// Only process lines that are indented more than the block header
			lineIndent := len(line) - len(strings.TrimLeft(line, " \t"))
			if lineIndent > blockIndent {
				parts := strings.SplitN(trimmed, " ", 2)
				if len(parts) >= 1 {
					key := parts[0]
					var value string
					if len(parts) == 2 {
						value = strings.Trim(parts[1], "\"")
					}
					pkgData[key] = value
				}
			}
		}
	}

	// Display package information
	fmt.Printf("package: %s\n", packageName)
	fmt.Println("--------")
	
	if pkgData == nil || len(pkgData) == 0 {
		fmt.Println("no package information available")
		return nil
	}

	// Display fields in the new canonical order
	if name, ok := pkgData["name"]; ok {
		fmt.Printf("name: %s\n", name)
	}
	if desc, ok := pkgData["desc"]; ok {
		fmt.Printf("desc: %s\n", desc)
	}
	if ver, ok := pkgData["ver"]; ok {
		fmt.Printf("version: %s\n", ver)
	}
	if srcType, ok := pkgData["src-type"]; ok {
		fmt.Printf("source type: %s\n", srcType)
	}
	if srcURL, ok := pkgData["src-url"]; ok {
		fmt.Printf("source url: %s\n", srcURL)
	}
	if srcRef, ok := pkgData["src-ref"]; ok {
		fmt.Printf("source ref: %s\n", srcRef)
	}
	if bin, ok := pkgData["bin"]; ok {
		fmt.Printf("binary: %s\n", bin)
	}
	if license, ok := pkgData["license"]; ok {
		fmt.Printf("license: %s\n", license)
	}

	// Display any other fields not in the canonical schema
	canonicalFields := map[string]bool{
		"name": true, "desc": true, "ver": true, "src-type": true,
		"src-url": true, "src-ref": true, "bin": true, "license": true,
	}
	for key, value := range pkgData {
		if !canonicalFields[key] {
			fmt.Printf("%s: %s\n", key, value)
		}
	}

	return nil
}

func getPackDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	
	packPath := filepath.Join(homeDir, packDir)
	return packPath, nil
}

func getConfigPath() (string, error) {
	packPath, err := getPackDir()
	if err != nil {
		return "", err
	}
	
	configPath := filepath.Join(packPath, "config")
	return configPath, nil
}

func getLocalRepoPath() (string, error) {
	packPath, err := getPackDir()
	if err != nil {
		return "", err
	}
	
	localPath := filepath.Join(packPath, "local")
	return localPath, nil
}

func getStorePath() (string, error) {
	packPath, err := getPackDir()
	if err != nil {
		return "", err
	}
	
	storePath := filepath.Join(packPath, "store")
	return storePath, nil
}

func ensurePackDirExists() error {
	packPath, err := getPackDir()
	if err != nil {
		return err
	}
	
	// Create main pack directory
	if err := os.MkdirAll(packPath, 0755); err != nil {
		return err
	}
	
	// Create subdirectories
	subdirs := []string{"locks", "config", "tmp", "local", "store", "cache"}
	for _, subdir := range subdirs {
		subdirPath := filepath.Join(packPath, subdir)
		if err := os.MkdirAll(subdirPath, 0755); err != nil {
			return err
		}
	}
	
	return nil
}

func ensureConfigExists() error {
	configPath, err := getConfigPath()
	if err != nil {
		return err
	}
	
	configFile := filepath.Join(configPath, "sources.box")
	
	// Check if config file exists
	if _, err := os.Stat(configFile); err == nil {
		return nil // Config exists
	}
	
	// Create default config with public key for pack-repo
	defaultConfig := `[data -c sources]
  repo ` + defaultRepo + `
  pubkey YxW0H7AepuqxI8izjqFi1sfEcdDvudLWS5ezYX0GVT0=
end`
	
	return os.WriteFile(configFile, []byte(defaultConfig), 0644)
}

func ensureBoxExists() error {
	// Check if box is available in PATH
	if _, err := exec.LookPath("box"); err == nil {
		return nil // box is available
	}

	fmt.Println("box interpreter not found, bootstrapping...")

	// Get user's home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	// Check if box exists in ~/.local/bin
	localBinDir := filepath.Join(homeDir, ".local", "bin")
	boxPath := filepath.Join(localBinDir, "box")
	
	if _, err := os.Stat(boxPath); err == nil {
		fmt.Printf("found box at %s\n", boxPath)
		return nil // box exists in ~/.local/bin
	}

	// Bootstrap box by installing it via pack
	fmt.Println("installing box interpreter...")
	
	// Create a minimal box bootstrap without using box itself
	return bootstrapBoxMinimal()
}

func bootstrapBoxMinimal() error {
	// For bootstrapping, we'll download and build box directly
	tempDir, err := os.MkdirTemp("", "box-bootstrap-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Clone boxlang repository
	fmt.Println("cloning boxlang repository...")
	cloneCmd := exec.Command("git", "clone", "https://github.com/shrub4thedub/boxlang.git", tempDir)
	if err := cloneCmd.Run(); err != nil {
		return fmt.Errorf("failed to clone boxlang repository: %v", err)
	}

	// Build box
	fmt.Println("building box interpreter...")
	buildCmd := exec.Command("go", "build", "-o", "box", "cmd/box/main.go")
	buildCmd.Dir = tempDir
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("failed to build box: %v", err)
	}

	// Get user's home directory and create ~/.local/bin
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	localBinDir := filepath.Join(homeDir, ".local", "bin")
	if err := os.MkdirAll(localBinDir, 0755); err != nil {
		return fmt.Errorf("failed to create ~/.local/bin: %v", err)
	}

	// Copy box to ~/.local/bin
	boxSrc := filepath.Join(tempDir, "box")
	boxDst := filepath.Join(localBinDir, "box")
	
	srcFile, err := os.Open(boxSrc)
	if err != nil {
		return fmt.Errorf("failed to open source box binary: %v", err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(boxDst)
	if err != nil {
		return fmt.Errorf("failed to create destination box binary: %v", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy box binary: %v", err)
	}

	// Make box executable
	if err := os.Chmod(boxDst, 0755); err != nil {
		return fmt.Errorf("failed to make box executable: %v", err)
	}

	fmt.Printf("box interpreter installed to %s\n", boxDst)
	fmt.Printf("add %s to your PATH if it's not already included\n", localBinDir)
	
	return nil
}

func loadConfig() (*Config, error) {
	if err := ensureConfigExists(); err != nil {
		return nil, err
	}
	
	configPath, err := getConfigPath()
	if err != nil {
		return nil, err
	}
	
	configFile := filepath.Join(configPath, "sources.box")
	content, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}
	
	config := &Config{}
	lines := strings.Split(string(content), "\n")
	var inDataBlock bool
	var blockIndent int
	
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		if strings.HasPrefix(trimmed, "[data") && strings.Contains(trimmed, "sources") {
			inDataBlock = true
			blockIndent = len(line) - len(strings.TrimLeft(line, " \t"))
			continue
		}
		
		if inDataBlock && trimmed == "end" {
			break
		}
		
		if inDataBlock && strings.HasPrefix(trimmed, "[") && !strings.Contains(trimmed, "sources") {
			break
		}
		
		if inDataBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			lineIndent := len(line) - len(strings.TrimLeft(line, " \t"))
			if lineIndent > blockIndent {
				parts := strings.SplitN(trimmed, " ", 2)
				if len(parts) >= 2 {
					config.Sources = append(config.Sources, parts[1])
				}
			}
		}
	}
	
	return config, nil
}

func saveConfig(config *Config) error {
	configPath, err := getConfigPath()
	if err != nil {
		return err
	}
	
	configFile := filepath.Join(configPath, "sources.box")
	
	var content strings.Builder
	content.WriteString("[data -c sources]\n")
	
	for _, source := range config.Sources {
		content.WriteString("  repo " + source + "\n")
	}
	
	content.WriteString("end\n")
	
	return os.WriteFile(configFile, []byte(content.String()), 0644)
}

func addSourceToConfig(sourceURL string) error {
	config, err := loadConfig()
	if err != nil {
		return err
	}
	
	// Check if source already exists
	for _, existing := range config.Sources {
		if existing == sourceURL {
			return fmt.Errorf("source already exists")
		}
	}
	
	config.Sources = append(config.Sources, sourceURL)
	return saveConfig(config)
}

// PackageSource represents a source where a package is available
type PackageSource struct {
	Name string
	URL  string
	Type string // "remote" or "local"
}

// findAvailableSources finds all sources where a package is available
func findAvailableSources(packageName string) ([]PackageSource, error) {
	var availableSources []PackageSource
	
	// Check local source if it exists
	localRepoPath, err := getLocalRepoPath()
	if err == nil {
		localPackagePath := filepath.Join(localRepoPath, packageName+".box")
		if _, err := os.Stat(localPackagePath); err == nil {
			availableSources = append(availableSources, PackageSource{
				Name: "local",
				URL:  localPackagePath,
				Type: "local",
			})
		}
	}
	
	// Check configured remote sources
	config, err := loadConfig()
	if err == nil && len(config.Sources) > 0 {
		for _, source := range config.Sources {
			// Try raw github content URL format
			scriptURL := fmt.Sprintf("%s/raw/main/%s.box", source, packageName)
			if strings.Contains(source, "raw.githubusercontent.com") {
				scriptURL = fmt.Sprintf("%s/%s.box", source, packageName)
			}
			
			// Test if package exists at this source (lightweight check)
			if testPackageExists(scriptURL) {
				availableSources = append(availableSources, PackageSource{
					Name: source,
					URL:  scriptURL,
					Type: "remote",
				})
			}
		}
	}
	
	return availableSources, nil
}

// testPackageExists does a lightweight test to see if a package exists at a URL
func testPackageExists(url string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Head(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// promptSourceSelection lets user choose from multiple sources
func promptSourceSelection(packageName string, sources []PackageSource) (PackageSource, error) {
	fmt.Printf("\nPackage '%s' is available from multiple sources:\n\n", packageName)
	
	for i, source := range sources {
		if source.Type == "local" {
			fmt.Printf("%d) Local repository\n", i+1)
		} else {
			fmt.Printf("%d) %s\n", i+1, source.Name)
		}
	}
	
	fmt.Print("\nSelect source [1]: ")
	
	var input string
	fmt.Scanln(&input)
	
	if input == "" {
		input = "1"
	}
	
	choice, err := strconv.Atoi(input)
	if err != nil || choice < 1 || choice > len(sources) {
		return PackageSource{}, fmt.Errorf("invalid selection")
	}
	
	return sources[choice-1], nil
}

func downloadFromSources(packageName string, scriptPath string) (PackageSource, error) {
	// Find all available sources
	sources, err := findAvailableSources(packageName)
	if err != nil {
		return PackageSource{}, err
	}
	
	if len(sources) == 0 {
		return PackageSource{}, fmt.Errorf("package '%s' not found in any configured source", packageName)
	}
	
	var selectedSource PackageSource
	
	if len(sources) == 1 {
		// Only one source available, use it
		selectedSource = sources[0]
		if selectedSource.Type == "local" {
			fmt.Printf("Found package in local repository\n")
		} else {
			fmt.Printf("Found package at %s\n", selectedSource.Name)
		}
	} else {
		// Multiple sources available, let user choose
		selectedSource, err = promptSourceSelection(packageName, sources)
		if err != nil {
			return PackageSource{}, err
		}
	}
	
	fmt.Printf("Using source: %s\n", selectedSource.Name)
	
	// Download from selected source
	if selectedSource.Type == "local" {
		err = copyFile(selectedSource.URL, scriptPath)
	} else {
		err = downloadFile(selectedSource.URL, scriptPath)
	}
	
	return selectedSource, err
}

// calculateSHA256 calculates the SHA256 hash of file contents
func calculateSHA256(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

// extractSHA256FromRecipe parses the sha256 field from a recipe's data block
func extractSHA256FromRecipe(scriptPath string) (string, error) {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", fmt.Errorf("failed to read script: %v", err)
	}

	lines := strings.Split(string(content), "\n")
	var inDataBlock bool
	var blockIndent int

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		// Check for data block start
		if strings.HasPrefix(trimmed, "[data") {
			inDataBlock = true
			blockIndent = len(line) - len(strings.TrimLeft(line, " \t"))
			continue
		}
		
		// Check for block end
		if inDataBlock && trimmed == "end" {
			break
		}
		
		// Check for any other block start
		if inDataBlock && strings.HasPrefix(trimmed, "[") {
			break
		}
		
		// Parse data block content for sha256 field
		if inDataBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			lineIndent := len(line) - len(strings.TrimLeft(line, " \t"))
			if lineIndent > blockIndent {
				parts := strings.SplitN(trimmed, " ", 2)
				if len(parts) >= 2 && parts[0] == "sha256" {
					return strings.TrimSpace(parts[1]), nil
				}
			}
		}
	}
	
	return "", fmt.Errorf("sha256 field not found in recipe data block")
}

// verifyRecipeIntegrity verifies Ed25519 signature - no fallback to unsafe SHA256
func verifyRecipeIntegrity(scriptPath string, sourceRepo string) error {
	// Only use Ed25519 signature verification
	if err := verifyEd25519Signature(scriptPath, sourceRepo); err != nil {
		return fmt.Errorf("Ed25519 signature verification failed: %v", err)
	}
	
	return nil
}

// verifyEd25519Signature verifies a detached Ed25519 signature
func verifyEd25519Signature(scriptPath string, sourceRepo string) error {
	// Get public key for source
	pubkey, err := getPublicKeyForSource(sourceRepo)
	if err != nil {
		return fmt.Errorf("no public key configured for source: %v", err)
	}
	
	if pubkey == "" {
		return fmt.Errorf("empty public key for source %s", sourceRepo)
	}
	
	// Decode base64 public key
	pubkeyBytes, err := base64.StdEncoding.DecodeString(pubkey)
	if err != nil {
		return fmt.Errorf("invalid base64 public key: %v", err)
	}
	
	if len(pubkeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key size: expected %d, got %d", ed25519.PublicKeySize, len(pubkeyBytes))
	}
	
	// Download signature file
	sigPath := scriptPath + ".sig"
	sigURL := strings.Replace(getScriptURL(sourceRepo, filepath.Base(scriptPath)), ".box", ".box.sig", 1)
	
	if err := downloadFile(sigURL, sigPath); err != nil {
		return fmt.Errorf("failed to download signature: %v", err)
	}
	defer os.Remove(sigPath)
	
	// Read signature
	sigBytes, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("failed to read signature: %v", err)
	}
	
	// Decode base64 signature
	signature, err := base64.StdEncoding.DecodeString(string(sigBytes))
	if err != nil {
		return fmt.Errorf("invalid base64 signature: %v", err)
	}
	
	// Read recipe content
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read recipe: %v", err)
	}
	
	// Verify signature
	if !ed25519.Verify(pubkeyBytes, content, signature) {
		return fmt.Errorf("signature verification failed")
	}
	
	return nil
}

// verifySHA256Hash performs legacy SHA256 self-verification
func verifySHA256Hash(scriptPath string) error {
	// Read the file content
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read script: %v", err)
	}
	
	// Extract expected hash from recipe
	expectedHash, err := extractSHA256FromRecipe(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to extract expected hash: %v", err)
	}
	
	// Calculate actual hash excluding the SHA256 field
	contentWithoutSHA256, err := removeCSHA256Field(content)
	if err != nil {
		return fmt.Errorf("failed to remove SHA256 field: %v", err)
	}
	actualHash := calculateSHA256(contentWithoutSHA256)
	
	// Compare hashes
	if actualHash != expectedHash {
		return fmt.Errorf("SHA256 verification failed: expected %s, got %s", expectedHash, actualHash)
	}
	
	return nil
}

// getPublicKeyForSource gets the public key for a given source repository
func getPublicKeyForSource(sourceRepo string) (string, error) {
	
	// Parse sources config to find pubkey for this repo
	configPath, err := getConfigPath()
	if err != nil {
		return "", err
	}
	
	configFile := filepath.Join(configPath, "sources.box")
	content, err := os.ReadFile(configFile)
	if err != nil {
		return "", err
	}
	
	lines := strings.Split(string(content), "\n")
	var inDataBlock bool
	var currentRepo string
	
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		if strings.HasPrefix(trimmed, "[data") && strings.Contains(trimmed, "sources") {
			inDataBlock = true
			continue
		}
		
		if inDataBlock && trimmed == "end" {
			break
		}
		
		if inDataBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			parts := strings.SplitN(trimmed, " ", 2)
			if len(parts) >= 2 {
				switch parts[0] {
				case "repo":
					currentRepo = parts[1]
				case "pubkey":
					if currentRepo == sourceRepo {
						return strings.Trim(parts[1], "\""), nil
					}
				}
			}
		}
	}
	
	return "", fmt.Errorf("public key not found for source %s", sourceRepo)
}

// getScriptURL constructs the script URL for a given source and package
func getScriptURL(sourceRepo, packageName string) string {
	return fmt.Sprintf("%s/raw/main/%s", sourceRepo, packageName)
}

// getLockFilePath returns the path to a package's lock file
func getLockFilePath(packageName string) (string, error) {
	packPath, err := getPackDir()
	if err != nil {
		return "", err
	}
	
	lockPath := filepath.Join(packPath, "locks", packageName+".lock")
	return lockPath, nil
}

// createLockFile creates a lock file with unambiguous field names and trust state
func createLockFile(packageName, repo, sourceURL, sourceType, sourceRef, sourceVersion, recipeVersion, recipeURL, recipeSHA256 string) error {
	lockFilePath, err := getLockFilePath(packageName)
	if err != nil {
		return err
	}
	
	// Get store and symlink paths
	storePath, err := getStorePath()
	if err != nil {
		return err
	}
	packageStorePath := filepath.Join(storePath, packageName)
	
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	symlinkPath := filepath.Join(homeDir, ".local/bin", packageName)
	configDir := filepath.Join(homeDir, ".config", packageName)
	
	// Determine trust state - always ed25519 now
	trustState := "ed25519"
	
	// Create comprehensive lock file content with unambiguous field names
	lockContent := fmt.Sprintf(`[data -c lock]
  package %s
  repo %s
  src_url %s
  src_type %s
  src_ref %s
  src_ref_used %s
  recipe_sha256 %s
  recipe_url %s
  installed_at %s
  store_path %s
  symlink_path %s
  config_dir %s
  trust_state %s
end
`, packageName, repo, sourceURL, sourceType, sourceRef, sourceVersion, recipeVersion, recipeURL, time.Now().UTC().Format(time.RFC3339), packageStorePath, symlinkPath, configDir, trustState)
	
	return os.WriteFile(lockFilePath, []byte(lockContent), 0644)
}

// extractRecipeURL extracts the src-url from the recipe data block
func extractRecipeURL(scriptPath string) (string, error) {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(content), "\n")
	inDataBlock := false
	
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		if strings.HasPrefix(trimmed, "[data") && strings.Contains(trimmed, "pkg") {
			inDataBlock = true
			continue
		}
		
		if inDataBlock && trimmed == "end" {
			break
		}
		
		// Look for src-url field in canonical schema
		if inDataBlock && strings.HasPrefix(trimmed, "src-url ") {
			url := strings.TrimSpace(strings.TrimPrefix(trimmed, "src-url"))
			// Remove surrounding quotes if present
			url = strings.Trim(url, "\"'")
			return url, nil
		}
		
		// Fallback to old url field for compatibility
		if inDataBlock && strings.HasPrefix(trimmed, "url ") {
			url := strings.TrimSpace(strings.TrimPrefix(trimmed, "url"))
			// Remove surrounding quotes if present
			url = strings.Trim(url, "\"'")
			return url, nil
		}
	}
	
	return "", fmt.Errorf("src-url not found in recipe")
}

// detectSourceTypeAndVersion extracts and validates source info from canonical recipe schema
func detectSourceTypeAndVersion(scriptPath string) (sourceType, sourceURL, sourceRef, sourceVersion string, err error) {
	// Extract source fields from canonical recipe schema
	sourceType, sourceURL, sourceRef, err = extractSourceFields(scriptPath)
	if err != nil {
		// Fall back to legacy url field
		legacyURL, legacyErr := extractRecipeURL(scriptPath)
		if legacyErr != nil {
			return "", "", "", "", fmt.Errorf("failed to extract source info: %v", err)
		}
		
		// Infer source type from legacy URL
		if strings.Contains(legacyURL, "github.com") || strings.Contains(legacyURL, "gitlab.com") || strings.HasSuffix(legacyURL, ".git") {
			sourceType = "git"
			sourceURL = legacyURL
			sourceRef = "HEAD"
		} else {
			sourceType = "archive"
			sourceURL = legacyURL
			sourceRef = "latest"
		}
	}
	
	// Get actual version based on source type
	switch sourceType {
	case "git":
		sourceVersion, err = getGitRefCommit(sourceURL, sourceRef)
		if err != nil {
			sourceVersion = "unknown"
			err = nil // Don't fail installation for version detection issues
		}
	case "archive", "file":
		// For archives and files, use the ref as version
		sourceVersion = sourceRef
	default:
		sourceVersion = "unknown"
	}
	
	return sourceType, sourceURL, sourceRef, sourceVersion, nil
}

// extractSourceFields extracts src-type, src-url, and src-ref from canonical recipe schema
func extractSourceFields(scriptPath string) (srcType, srcURL, srcRef string, err error) {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", "", "", err
	}

	lines := strings.Split(string(content), "\n")
	inDataBlock := false
	
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		if strings.HasPrefix(trimmed, "[data") && strings.Contains(trimmed, "pkg") {
			inDataBlock = true
			continue
		}
		
		if inDataBlock && trimmed == "end" {
			break
		}
		
		if inDataBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			parts := strings.SplitN(trimmed, " ", 2)
			if len(parts) >= 2 {
				key := parts[0]
				value := strings.Trim(parts[1], "\"'")
				
				switch key {
				case "src-type":
					srcType = strings.TrimSpace(value)
				case "src-url":
					srcURL = strings.TrimSpace(value)
				case "src-ref":
					srcRef = strings.TrimSpace(value)
				}
			}
		}
	}
	
	if srcType == "" || srcURL == "" {
		return "", "", "", fmt.Errorf("missing required src-type or src-url fields")
	}
	
	if srcRef == "" {
		srcRef = "HEAD" // Default reference
	}
	
	return srcType, srcURL, srcRef, nil
}

// getGitRefCommit gets the commit hash for a specific reference
func getGitRefCommit(repoURL, ref string) (string, error) {
	cmd := exec.Command("git", "ls-remote", repoURL, ref)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git commit for ref %s: %v", ref, err)
	}
	
	// Parse the output to get the commit hash
	parts := strings.Fields(string(output))
	if len(parts) > 0 {
		return parts[0][:8], nil // Return short commit hash (8 chars)
	}
	
	return "", fmt.Errorf("no commit hash found for ref %s", ref)
}

// getGitHeadCommit gets the HEAD commit hash from a git repository
func getGitHeadCommit(repoURL string) (string, error) {
	cmd := exec.Command("git", "ls-remote", repoURL, "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git commit: %v", err)
	}
	
	// Parse the output to get the commit hash
	parts := strings.Fields(string(output))
	if len(parts) > 0 {
		return parts[0][:8], nil // Return short commit hash (8 chars)
	}
	
	return "", fmt.Errorf("no commit hash found")
}

// calculateRecipeVersion calculates the hash of recipe content excluding sha256 field
func calculateRecipeVersion(scriptPath string) (string, error) {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", err
	}
	
	contentWithoutSHA256, err := removeCSHA256Field(content)
	if err != nil {
		return "", err
	}
	
	return calculateSHA256(contentWithoutSHA256), nil
}

// constructRecipeURL constructs the recipe URL based on selected source
func constructRecipeURL(selectedSource PackageSource, packageName string) string {
	if selectedSource.Type == "local" {
		return "local"
	}
	
	// For remote sources, construct the recipe URL
	return fmt.Sprintf("%s/raw/main/%s.box", selectedSource.Name, packageName)
}

// getSourceCommit gets the latest commit hash from a git repository URL
func getSourceCommit(sourceURL string) (string, error) {
	// For now, return a placeholder. In a real implementation, this would
	// use git ls-remote or similar to get the actual commit hash
	cmd := exec.Command("git", "ls-remote", sourceURL, "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "unknown", nil // Don't fail if we can't get commit info
	}
	
	// Parse the output to get the commit hash
	parts := strings.Fields(string(output))
	if len(parts) > 0 {
		return parts[0][:8], nil // Return short commit hash
	}
	
	return "unknown", nil
}

// extractPackageInfo extracts version and SHA256 from a recipe
func extractPackageInfo(scriptPath string) (version, sha256 string, err error) {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read script: %v", err)
	}

	lines := strings.Split(string(content), "\n")
	var inDataBlock bool
	var blockIndent int

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		// Check for data block start
		if strings.HasPrefix(trimmed, "[data") {
			inDataBlock = true
			blockIndent = len(line) - len(strings.TrimLeft(line, " \t"))
			continue
		}
		
		// Check for block end
		if inDataBlock && trimmed == "end" {
			break
		}
		
		// Check for any other block start
		if inDataBlock && strings.HasPrefix(trimmed, "[") {
			break
		}
		
		// Parse data block content
		if inDataBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			lineIndent := len(line) - len(strings.TrimLeft(line, " \t"))
			if lineIndent > blockIndent {
				parts := strings.SplitN(trimmed, " ", 2)
				if len(parts) >= 2 {
					key := parts[0]
					value := strings.TrimSpace(parts[1])
					switch key {
					case "ver":
						version = value
					case "sha256":
						sha256 = value
					}
				}
			}
		}
	}
	
	if version == "" {
		version = "unknown"
	}
	if sha256 == "" {
		sha256 = "unknown"
	}
	
	return version, sha256, nil
}

// removeCSHA256Field removes the sha256 field from file content for hash calculation
func removeCSHA256Field(content []byte) ([]byte, error) {
	lines := strings.Split(string(content), "\n")
	var filteredLines []string

	for _, line := range lines {
		// Simple approach: skip any line that contains "sha256" as the first field after whitespace
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && strings.HasPrefix(trimmed, "sha256 ") {
			// Skip this line
			continue
		}
		// Add all other lines
		filteredLines = append(filteredLines, line)
	}
	
	return []byte(strings.Join(filteredLines, "\n")), nil
}

// executeUninstallScript calls the recipe's uninstall function directly
func executeUninstallScript(packageName string) error {
	// Check if lock file exists
	lockFilePath, err := getLockFilePath(packageName)
	if err != nil {
		return fmt.Errorf("failed to get lock file path: %v", err)
	}
	
	if _, err := os.Stat(lockFilePath); os.IsNotExist(err) {
		return fmt.Errorf("package %s is not installed (no lock file found)", packageName)
	}
	
	// Read lock file to get original recipe URL
	lockData, err := parseLockFile(lockFilePath)
	if err != nil {
		return fmt.Errorf("failed to parse lock file: %v", err)
	}
	
	// Create temporary directory for uninstall
	tempDir, err := os.MkdirTemp("", "pack-uninstall-"+packageName)
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	// Download the original recipe
	scriptPath := filepath.Join(tempDir, packageName+".box")
	originalRepo := lockData["repo"]
	
	if originalRepo == "local" {
		// Use local source
		localRepoPath, err := getLocalRepoPath()
		if err != nil {
			return fmt.Errorf("failed to get local repo path: %v", err)
		}
		localPackagePath := filepath.Join(localRepoPath, packageName+".box")
		if err := copyFile(localPackagePath, scriptPath); err != nil {
			return fmt.Errorf("failed to copy from local source: %v", err)
		}
	} else {
		// Use remote source
		scriptURL := fmt.Sprintf("%s/raw/main/%s.box", originalRepo, packageName)
		if err := downloadFile(scriptURL, scriptPath); err != nil {
			return fmt.Errorf("failed to download recipe: %v", err)
		}
	}
	
	// Find box executable
	boxPath, err := findBoxExecutable()
	if err != nil {
		return fmt.Errorf("box executable not found: %v", err)
	}
	
	// Execute recipe with uninstall verb
	execCmd := exec.Command(boxPath, scriptPath, "uninstall")
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execCmd.Stdin = os.Stdin
	execCmd.Dir = tempDir
	
	err = execCmd.Run()
	if err != nil {
		return fmt.Errorf("uninstall failed: %v", err)
	}
	
	// Remove lock file after successful uninstall
	fmt.Println("removing lockfile...")
	if err := os.Remove(lockFilePath); err != nil {
		fmt.Printf("warning: failed to remove lockfile: %v\n", err)
	} else {
		fmt.Println("✓ lockfile removed")
	}
	
	return nil
}

// listPackages displays all installed packages with their version information
func listPackages(args []string) {
	if len(args) > 0 && args[0] == "help" {
		showListHelp()
		return
	}
	
	packPath, err := getPackDir()
	if err != nil {
		fmt.Printf("error getting pack directory: %v\n", err)
		os.Exit(1)
	}

	locksDir := filepath.Join(packPath, "locks")
	if _, err := os.Stat(locksDir); os.IsNotExist(err) {
		fmt.Println("no packages installed")
		return
	}

	files, err := os.ReadDir(locksDir)
	if err != nil {
		fmt.Printf("error reading locks directory: %v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Println("no packages installed")
		return
	}

	fmt.Printf("%-15s %-12s %-30s %s\n", "package", "version", "source", "installed")
	fmt.Printf("%-15s %-12s %-30s %s\n", "-------", "-------", "------", "---------")

	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".lock") {
			continue
		}

		packageName := strings.TrimSuffix(file.Name(), ".lock")
		lockData, err := parseLockFile(filepath.Join(locksDir, file.Name()))
		if err != nil {
			fmt.Printf("%-15s %-12s %-30s %s\n", packageName, "error", "error", "error")
			continue
		}

		// Format the display
		version := lockData["src_ref_used"]
		if len(version) > 12 {
			version = version[:12]
		}
		source := lockData["src_url"]
		if len(source) > 30 {
			source = source[:27] + "..."
		}
		installDate := lockData["installed_at"]
		if installDate != "" {
			// Parse and format the date
			if t, err := time.Parse(time.RFC3339, installDate); err == nil {
				installDate = t.Format("2006-01-02")
			}
		}

		fmt.Printf("%-15s %-12s %-30s %s\n", packageName, version, source, installDate)
	}
}

// updatePackages scans for updates and installs them with confirmation
func updatePackages(args []string) {
	if len(args) > 0 && args[0] == "help" {
		showUpdateHelp()
		return
	}
	
	fmt.Println("scanning for package updates...")
	
	availableUpdates, err := scanForUpdates()
	if err != nil {
		fmt.Printf("error scanning for updates: %v\n", err)
		os.Exit(1)
	}

	if len(availableUpdates) == 0 {
		fmt.Println("all packages are up to date")
		return
	}

	// Display available updates
	fmt.Printf("\navailable updates:\n")
	for _, update := range availableUpdates {
		fmt.Printf("- %s: %s → %s (%s)\n", 
			update.PackageName, 
			update.CurrentVersion, 
			update.NewVersion, 
			update.UpdateType)
	}

	// Ask for confirmation
	fmt.Printf("\nupdate %d package(s)? [y/N]: ", len(availableUpdates))
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		fmt.Printf("error reading input: %v\n", err)
		os.Exit(1)
	}
	
	response = strings.TrimSpace(strings.ToLower(response))
	if response != "y" && response != "yes" {
		fmt.Println("update cancelled")
		return
	}

	// Perform updates
	fmt.Println("\nupdating packages...")
	for _, update := range availableUpdates {
		fmt.Printf("Updating %s...\n", update.PackageName)
		if err := updatePackageFromOriginalSource(update.PackageName); err != nil {
			fmt.Printf("error updating %s: %v\n", update.PackageName, err)
		} else {
			fmt.Printf("✓ %s updated successfully\n", update.PackageName)
		}
	}
	
	fmt.Println("\nupdate complete!")
}

// PackageUpdate represents an available update
type PackageUpdate struct {
	PackageName    string
	CurrentVersion string
	NewVersion     string
	UpdateType     string // "source updated", "recipe updated", "both updated"
}

// scanForUpdates checks all installed packages for available updates
func scanForUpdates() ([]PackageUpdate, error) {
	var updates []PackageUpdate

	packPath, err := getPackDir()
	if err != nil {
		return nil, err
	}

	locksDir := filepath.Join(packPath, "locks")
	if _, err := os.Stat(locksDir); os.IsNotExist(err) {
		return updates, nil // No packages installed
	}

	files, err := os.ReadDir(locksDir)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".lock") {
			continue
		}

		packageName := strings.TrimSuffix(file.Name(), ".lock")
		lockData, err := parseLockFile(filepath.Join(locksDir, file.Name()))
		if err != nil {
			continue // Skip problematic lock files
		}

		update, hasUpdate, err := checkPackageForUpdate(packageName, lockData)
		if err != nil {
			fmt.Printf("warning: failed to check updates for %s: %v\n", packageName, err)
			continue
		}

		if hasUpdate {
			updates = append(updates, update)
		}
	}

	return updates, nil
}

// parseLockFile parses a lock file and returns a map of key-value pairs
func parseLockFile(lockFilePath string) (map[string]string, error) {
	content, err := os.ReadFile(lockFilePath)
	if err != nil {
		return nil, err
	}

	lockData := make(map[string]string)
	lines := strings.Split(string(content), "\n")
	inDataBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		if strings.HasPrefix(trimmed, "[data") && strings.Contains(trimmed, "lock") {
			inDataBlock = true
			continue
		}
		
		if inDataBlock && trimmed == "end" {
			break
		}
		
		if inDataBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			parts := strings.SplitN(trimmed, " ", 2)
			if len(parts) >= 2 {
				key := parts[0]
				value := strings.TrimSpace(parts[1])
				lockData[key] = value
				
				// Handle legacy field name mapping for backward compatibility
				switch key {
				case "source":
					if _, exists := lockData["src_url"]; !exists {
						lockData["src_url"] = value
					}
				case "source_type":
					if _, exists := lockData["src_type"]; !exists {
						lockData["src_type"] = value
					}
				case "source_version":
					if _, exists := lockData["src_ref_used"]; !exists {
						lockData["src_ref_used"] = value
					}
				case "sha256":
					if _, exists := lockData["recipe_sha256"]; !exists {
						lockData["recipe_sha256"] = value
					}
				}
			}
		}
	}

	return lockData, nil
}

// checkPackageForUpdate checks if a package has available updates
func checkPackageForUpdate(packageName string, lockData map[string]string) (PackageUpdate, bool, error) {
	update := PackageUpdate{
		PackageName:    packageName,
		CurrentVersion: lockData["src_ref_used"],
	}

	var updateReasons []string

	// Check recipe for updates
	recipeURL := lockData["recipe_url"]
	if recipeURL != "" && recipeURL != "local" {
		// Download current recipe and compare hash
		currentRecipeVersion, err := getCurrentRecipeVersion(recipeURL)
		if err == nil && currentRecipeVersion != lockData["recipe_sha256"] {
			updateReasons = append(updateReasons, "recipe updated")
		}
	}

	// Check source for updates
	sourceURL := lockData["src_url"]
	sourceType := lockData["src_type"]
	if sourceURL != "" && sourceURL != "unknown" {
		newSourceVersion, err := getCurrentSourceVersion(sourceURL, sourceType)
		if err == nil && newSourceVersion != lockData["src_ref_used"] {
			updateReasons = append(updateReasons, "source updated")
			update.NewVersion = newSourceVersion
		}
	}

	if len(updateReasons) == 0 {
		return update, false, nil
	}

	update.UpdateType = strings.Join(updateReasons, ", ")
	if update.NewVersion == "" {
		update.NewVersion = "latest"
	}

	return update, true, nil
}

// getCurrentRecipeVersion downloads a recipe with ETag caching and calculates its version hash
func getCurrentRecipeVersion(recipeURL string) (string, error) {
	// Create temp file to download recipe
	tempFile, err := os.CreateTemp("", "recipe-*.box")
	if err != nil {
		return "", err
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Download recipe without ETag caching for update checks
	if err := downloadFileWithCache(recipeURL, tempFile.Name(), false); err != nil {
		return "", err
	}

	// Calculate version hash
	return calculateRecipeVersion(tempFile.Name())
}

// getCurrentSourceVersion gets the current version of a source
func getCurrentSourceVersion(sourceURL, sourceType string) (string, error) {
	switch sourceType {
	case "git":
		return getGitHeadCommit(sourceURL)
	case "download":
		// For downloads, we'd need to download and hash, but that's expensive
		// For now, just indicate that we can't easily check
		return "latest", nil
	default:
		return "", fmt.Errorf("unknown source type: %s", sourceType)
	}
}

// updatePackageFromOriginalSource updates a package using the same source it was originally installed from
func updatePackageFromOriginalSource(packageName string) error {
	// Read the lock file to get original source info
	lockFilePath, err := getLockFilePath(packageName)
	if err != nil {
		return fmt.Errorf("failed to get lock file path: %v", err)
	}

	lockData, err := parseLockFile(lockFilePath)
	if err != nil {
		return fmt.Errorf("failed to parse lock file: %v", err)
	}

	originalRepo := lockData["repo"]
	if originalRepo == "" {
		return fmt.Errorf("no original repo found in lock file")
	}

	// Create temporary directory for script
	tempDir, err := os.MkdirTemp("", "pack-update-"+packageName)
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Download script from original source
	scriptPath := filepath.Join(tempDir, packageName+".box")
	
	var selectedSource PackageSource
	if originalRepo == "local" {
		// Use local source
		localRepoPath, err := getLocalRepoPath()
		if err != nil {
			return fmt.Errorf("failed to get local repo path: %v", err)
		}
		localPackagePath := filepath.Join(localRepoPath, packageName+".box")
		if _, err := os.Stat(localPackagePath); err != nil {
			return fmt.Errorf("package not found in local repository: %v", err)
		}
		
		selectedSource = PackageSource{
			Name: "local",
			URL:  localPackagePath,
			Type: "local",
		}
		
		if err := copyFile(localPackagePath, scriptPath); err != nil {
			return fmt.Errorf("failed to copy from local source: %v", err)
		}
	} else {
		// Use remote source
		scriptURL := fmt.Sprintf("%s/raw/main/%s.box", originalRepo, packageName)
		
		selectedSource = PackageSource{
			Name: originalRepo,
			URL:  scriptURL,
			Type: "remote",
		}
		
		if err := downloadFile(scriptURL, scriptPath); err != nil {
			return fmt.Errorf("failed to download from original source: %v", err)
		}
	}
	
	fmt.Printf("Using original source: %s\n", selectedSource.Name)

	// Verify recipe integrity
	fmt.Println("Verifying recipe integrity...")
	if err := verifyRecipeIntegrity(scriptPath, originalRepo); err != nil {
		fmt.Printf("⚠️  Warning: %v\n", err)
		fmt.Print("Continue anyway? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %v", err)
		}
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			return fmt.Errorf("update cancelled due to verification failure")
		}
	} else {
		fmt.Println("✓ recipe integrity verified")
	}

	// Show recipe and get user confirmation
	if err := showRecipeAndConfirm(scriptPath); err != nil {
		return err
	}

	// Find box executable
	boxPath, err := findBoxExecutable()
	if err != nil {
		return fmt.Errorf("box executable not found: %v", err)
	}

	// Execute script
	cmdArgs := []string{scriptPath}

	execCmd := exec.Command(boxPath, cmdArgs...)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execCmd.Stdin = os.Stdin

	err = execCmd.Run()
	if err != nil {
		return err
	}

	// Create or update lock file after successful installation
	fmt.Println("updating lockfile...")
	
	// Extract source information from canonical schema
	sourceType, recipeSourceURL, sourceRef, sourceVersion, err := detectSourceTypeAndVersion(scriptPath)
	if err != nil {
		fmt.Printf("warning: failed to extract source info: %v\n", err)
		// Fall back to legacy extraction
		recipeSourceURL, err = extractRecipeURL(scriptPath)
		if err != nil {
			fmt.Printf("warning: failed to extract source URL: %v\n", err)
			recipeSourceURL = "unknown"
		}
		sourceType = "unknown"
		sourceVersion = "unknown"
		sourceRef = "unknown"
	}
	
	// Calculate recipe version (content hash)
	recipeVersion, err := calculateRecipeVersion(scriptPath)
	if err != nil {
		fmt.Printf("warning: failed to calculate recipe version: %v\n", err)
		recipeVersion = "unknown"
	}
	
	// Construct recipe URL from selected source
	recipeURL := constructRecipeURL(selectedSource, packageName)
	
	// Calculate actual SHA256 for lock file
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		fmt.Printf("warning: failed to read script for hash: %v\n", err)
	}
	contentWithoutSHA256, err := removeCSHA256Field(content)
	if err != nil {
		fmt.Printf("warning: failed to remove SHA256 field: %v\n", err)
	}
	recipeSHA256 := calculateSHA256(contentWithoutSHA256)
	
	// Get repo name from selected source
	repoName := selectedSource.Name
	
	if err := createLockFile(packageName, repoName, recipeSourceURL, sourceType, sourceRef, sourceVersion, recipeVersion, recipeURL, recipeSHA256); err != nil {
		fmt.Printf("warning: failed to update lock file: %v\n", err)
	} else {
		fmt.Println("✓ lockfile updated")
	}

	return nil
}

// showHelp displays general help information
func showHelp() {
	fmt.Println("pack - a package manager using boxlang")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack <command> [arguments]")
	fmt.Println()
	fmt.Println("COMMANDS:")
	fmt.Println("  open <package>     install a package")
	fmt.Println("  close <package>    uninstall a package")
	fmt.Println("  list               list installed packages")
	fmt.Println("  update             check for and install package updates")
	fmt.Println("  peek <package>     show package information")
	fmt.Println("  add-source <url>   add a repository source")
	fmt.Println("  keygen             generate Ed25519 key pair for recipe signing")
	fmt.Println("  sign <key> <file>  sign recipe files with Ed25519")
	fmt.Println("  help               show this help information")
	fmt.Println()
	fmt.Println("For command-specific help, use: pack <command> help")
	fmt.Println("Example: pack open help")
}

// showOpenHelp displays help for the open command
func showOpenHelp() {
	fmt.Println("pack open - install a package")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack open <package>")
	fmt.Println("  pack open help")
	fmt.Println()
	fmt.Println("DESCRIPTION:")
	fmt.Println("  downloads and installs a package from configured sources.")
	fmt.Println("  if multiple sources have the package, you'll be prompted to choose.")
	fmt.Println()
	fmt.Println("  the installation process:")
	fmt.Println("  1. finds the package in available sources")
	fmt.Println("  2. downloads and verifies the recipe")
	fmt.Println("  3. shows the recipe for review")
	fmt.Println("  4. executes the installation script")
	fmt.Println("  5. creates a lockfile for tracking")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  pack open vim      # Install vim text editor")
	fmt.Println("  pack open pfetch   # Install pfetch system info tool")
}

// showCloseHelp displays help for the close command
func showCloseHelp() {
	fmt.Println("pack close - uninstall a package")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack close <package>")
	fmt.Println("  pack close help")
	fmt.Println()
	fmt.Println("DESCRIPTION:")
	fmt.Println("  uninstalls a previously installed package using the universal")
	fmt.Println("  uninstaller with information from the package's lock file.")
	fmt.Println()
	fmt.Println("  the uninstallation process:")
	fmt.Println("  1. reads the package lock file")
	fmt.Println("  2. removes the installed binary")
	fmt.Println("  3. preserves configuration files")
	fmt.Println("  4. removes the lockfile")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  pack close vim     # Uninstall vim")
	fmt.Println("  pack close pfetch  # Uninstall pfetch")
}

// showListHelp displays help for the list command
func showListHelp() {
	fmt.Println("pack list - list installed packages")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack list")
	fmt.Println("  pack list help")
	fmt.Println()
	fmt.Println("DESCRIPTION:")
	fmt.Println("  shows all installed packages with their version information,")
	fmt.Println("  source repository, and installation date.")
	fmt.Println()
	fmt.Println("  columns displayed:")
	fmt.Println("  - PACKAGE: Package name")
	fmt.Println("  - VERSION: Source version (git commit or content hash)")
	fmt.Println("  - SOURCE: Origin URL of the package")
	fmt.Println("  - INSTALLED: Installation date")
	fmt.Println()
	fmt.Println("EXAMPLE:")
	fmt.Println("  pack list")
}

// showUpdateHelp displays help for the update command
func showUpdateHelp() {
	fmt.Println("pack update - check for and install package updates")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack update")
	fmt.Println("  pack update help")
	fmt.Println()
	fmt.Println("DESCRIPTION:")
	fmt.Println("  scans all installed packages for available updates by comparing")
	fmt.Println("  current versions with remote sources. Updates use the same")
	fmt.Println("  source repository that was used for original installation.")
	fmt.Println()
	fmt.Println("  the update process:")
	fmt.Println("  1. checks all packages for updates")
	fmt.Println("  2. shows available updates")
	fmt.Println("  3. asks for confirmation")
	fmt.Println("  4. updates all confirmed packages")
	fmt.Println()
	fmt.Println("  update detection:")
	fmt.Println("  - git packages: compares commit hashes")
	fmt.Println("  - recipe changes: compares recipe content")
	fmt.Println()
	fmt.Println("EXAMPLE:")
	fmt.Println("  pack update")
}

// showPeekHelp displays help for the peek command
func showPeekHelp() {
	fmt.Println("pack peek - show package information")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack peek <package>")
	fmt.Println("  pack peek help")
	fmt.Println()
	fmt.Println("DESCRIPTION:")
	fmt.Println("  downloads and displays information about a package without")
	fmt.Println("  installing it. shows package metadata from the recipe.")
	fmt.Println()
	fmt.Println("  information displayed:")
	fmt.Println("  - package name and description")
	fmt.Println("  - version and supported operating systems")
	fmt.Println("  - source URL and license")
	fmt.Println("  - author information")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  pack peek vim      # Show vim package info")
	fmt.Println("  pack peek pfetch   # Show pfetch package info")
}

// showAddSourceHelp displays help for the add-source command
func showAddSourceHelp() {
	fmt.Println("pack add-source - add a repository source")
	fmt.Println()
	fmt.Println("USAGE:")
	fmt.Println("  pack add-source <url>")
	fmt.Println("  pack add-source help")
	fmt.Println()
	fmt.Println("DESCRIPTION:")
	fmt.Println("  adds a new repository source for package discovery.")
	fmt.Println("  sources are searched when installing packages.")
	fmt.Println()
	fmt.Println("  supported source types:")
	fmt.Println("  - github repositories")
	fmt.Println("  - other git repositories with web access")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  pack add-source https://github.com/user/pack-repo")
	fmt.Println("  pack add-source https://gitlab.com/user/packages")
}

// generateKeys generates a new Ed25519 key pair for recipe signing
func generateKeys() {
	fmt.Println("Generating Ed25519 key pair for recipe signing...")
	
	// Generate key pair
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Printf("Failed to generate keys: %v\n", err)
		os.Exit(1)
	}
	
	// Base64 encode keys
	publicB64 := base64.StdEncoding.EncodeToString(publicKey)
	privateB64 := base64.StdEncoding.EncodeToString(privateKey)
	
	fmt.Println()
	fmt.Println("🔑 Key pair generated successfully!")
	fmt.Println()
	fmt.Printf("Public key:  %s\n", publicB64)
	fmt.Printf("Private key: %s\n", privateB64)
	fmt.Println()
	fmt.Println("📋 Next steps:")
	fmt.Println("1. Add the public key to your sources.box config")
	fmt.Println("2. Keep the private key secure - you'll need it to sign recipes")
	fmt.Println("3. Sign your recipes with: pack sign <private_key> <recipe_files>")
}

// signFiles signs recipe files with the provided private key
func signFiles(privateKeyB64, target string) {
	fmt.Printf("Signing recipes with Ed25519...\n")
	
	// Decode private key
	privateKeyBytes, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil {
		fmt.Printf("Failed to decode private key: %v\n", err)
		os.Exit(1)
	}
	
	if len(privateKeyBytes) != ed25519.PrivateKeySize {
		fmt.Printf("Invalid private key size: expected %d, got %d\n", ed25519.PrivateKeySize, len(privateKeyBytes))
		os.Exit(1)
	}
	
	privateKey := ed25519.PrivateKey(privateKeyBytes)
	
	// Check if target is directory or file
	stat, err := os.Stat(target)
	if err != nil {
		fmt.Printf("Failed to access target: %v\n", err)
		os.Exit(1)
	}
	
	var signedCount int
	
	if stat.IsDir() {
		// Sign all .box files in directory
		err = filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			
			if strings.HasSuffix(path, ".box") {
				if err := signFile(privateKey, path); err != nil {
					fmt.Printf("Failed to sign %s: %v\n", path, err)
					return err
				}
				signedCount++
			}
			
			return nil
		})
		
		if err != nil {
			fmt.Printf("Failed to sign files: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Sign single file
		if err := signFile(privateKey, target); err != nil {
			fmt.Printf("Failed to sign file: %v\n", err)
			os.Exit(1)
		}
		signedCount++
	}
	
	fmt.Printf("✓ Successfully signed %d file(s)\n", signedCount)
}

// signFile signs a single file with Ed25519
func signFile(privateKey ed25519.PrivateKey, filePath string) error {
	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %v", filePath, err)
	}
	
	// Sign content
	signature := ed25519.Sign(privateKey, content)
	
	// Base64 encode signature
	signatureB64 := base64.StdEncoding.EncodeToString(signature)
	
	// Write signature file
	sigPath := filePath + ".sig"
	if err := os.WriteFile(sigPath, []byte(signatureB64), 0644); err != nil {
		return fmt.Errorf("failed to write signature %s: %v", sigPath, err)
	}
	
	fmt.Printf("Signed: %s -> %s\n", filePath, sigPath)
	return nil
}
