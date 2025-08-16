package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Key rotation automation for pack repositories
func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run rotate_keys.go <repo_directory> [version]")
		fmt.Println("  repo_directory: Path to the pack repository")
		fmt.Println("  version: Optional version number (auto-increments if not specified)")
		os.Exit(1)
	}
	
	repoDir := os.Args[1]
	keysDir := filepath.Join(repoDir, "keys")
	
	// Create keys directory if it doesn't exist
	if err := os.MkdirAll(keysDir, 0755); err != nil {
		log.Fatalf("Failed to create keys directory: %v", err)
	}
	
	// Determine new version number
	var newVersion int
	if len(os.Args) >= 3 {
		var err error
		newVersion, err = strconv.Atoi(os.Args[2])
		if err != nil {
			log.Fatalf("Invalid version number: %v", err)
		}
	} else {
		// Auto-increment from current version
		newVersion = getCurrentVersion(keysDir) + 1
	}
	
	fmt.Printf("Rotating keys to version %d...\n", newVersion)
	
	// Step 1: Backup current key as previous version
	if err := backupCurrentKey(keysDir, newVersion-1); err != nil {
		log.Printf("Warning: Failed to backup current key: %v", err)
	}
	
	// Step 2: Generate new key pair
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatalf("Failed to generate key pair: %v", err)
	}
	
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKey)
	privKeyB64 := base64.StdEncoding.EncodeToString(privKey)
	
	fmt.Printf("Generated new key pair:\n")
	fmt.Printf("Public key:  %s\n", pubKeyB64)
	fmt.Printf("Private key: %s\n", privKeyB64)
	
	// Step 3: Create new key metadata file
	if err := createKeyMetadata(keysDir, newVersion, pubKeyB64); err != nil {
		log.Fatalf("Failed to create key metadata: %v", err)
	}
	
	// Step 4: Update legacy .pub file for backward compatibility
	if err := updateLegacyPubFile(keysDir, pubKeyB64); err != nil {
		log.Printf("Warning: Failed to update legacy .pub file: %v", err)
	}
	
	// Step 5: Re-sign all packages with new key
	if err := resignAllPackages(repoDir, privKey); err != nil {
		log.Fatalf("Failed to re-sign packages: %v", err)
	}
	
	// Step 6: Save private key securely (for manual backup)
	privKeyFile := filepath.Join(keysDir, fmt.Sprintf("pack_v%d_private.key", newVersion))
	if err := os.WriteFile(privKeyFile, []byte(privKeyB64), 0600); err != nil {
		log.Printf("Warning: Failed to save private key file: %v", err)
	} else {
		fmt.Printf("Private key saved to: %s\n", privKeyFile)
		fmt.Printf("⚠️  IMPORTANT: Backup this private key securely and remove it from the repository!\n")
	}
	
	fmt.Printf("✓ Key rotation completed successfully!\n")
	fmt.Printf("New version: %d\n", newVersion)
	fmt.Printf("Public key: %s\n", pubKeyB64)
}

// getCurrentVersion reads the current key version from pack.box
func getCurrentVersion(keysDir string) int {
	packBoxFile := filepath.Join(keysDir, "pack.box")
	content, err := os.ReadFile(packBoxFile)
	if err != nil {
		return 0 // Start from version 1 if no current file
	}
	
	// Simple parsing to extract version
	lines := splitLines(string(content))
	for _, line := range lines {
		if len(line) > 10 && line[:10] == "  version " {
			if version, err := strconv.Atoi(line[11:]); err == nil {
				return version
			}
		}
	}
	
	return 0
}

// backupCurrentKey saves the current key as a versioned backup
func backupCurrentKey(keysDir string, version int) error {
	currentFile := filepath.Join(keysDir, "pack.box")
	if _, err := os.Stat(currentFile); os.IsNotExist(err) {
		return nil // No current key to backup
	}
	
	backupFile := filepath.Join(keysDir, fmt.Sprintf("pack_v%d.box", version))
	content, err := os.ReadFile(currentFile)
	if err != nil {
		return err
	}
	
	return os.WriteFile(backupFile, content, 0644)
}

// createKeyMetadata creates a new pack.box file with metadata
func createKeyMetadata(keysDir string, version int, pubKeyB64 string) error {
	now := time.Now()
	issuedAt := now.Unix()
	expiresAt := now.Add(2 * 365 * 24 * time.Hour).Unix() // 2 years
	
	content := fmt.Sprintf(`[data -c keyinfo]
  version     %d
  issued_at   %d
  expires_at  %d
  algorithm   ed25519
end

[data -c pubkey]
  key %s
end`, version, issuedAt, expiresAt, pubKeyB64)
	
	packBoxFile := filepath.Join(keysDir, "pack.box")
	return os.WriteFile(packBoxFile, []byte(content), 0644)
}

// updateLegacyPubFile updates pack.pub for backward compatibility
func updateLegacyPubFile(keysDir string, pubKeyB64 string) error {
	pubFile := filepath.Join(keysDir, "pack.pub")
	return os.WriteFile(pubFile, []byte(pubKeyB64+"\n"), 0644)
}

// resignAllPackages re-signs all .box files in the repository
func resignAllPackages(repoDir string, privKey ed25519.PrivateKey) error {
	return filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		
		if !info.IsDir() && filepath.Ext(path) == ".box" {
			// Skip key files
			if filepath.Dir(path) == filepath.Join(repoDir, "keys") {
				return nil
			}
			
			if err := signFile(privKey, path); err != nil {
				return fmt.Errorf("failed to sign %s: %v", path, err)
			}
			fmt.Printf("Signed: %s\n", path)
		}
		
		return nil
	})
}

// signFile creates a signature for a single file
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
	return os.WriteFile(sigPath, []byte(signatureB64), 0644)
}

// splitLines splits a string into lines
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}